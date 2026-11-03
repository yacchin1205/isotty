package vmcfg

import (
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
)

// ChildSpec describes a VM created next to the project's VM for the agent:
// reachable over SSH from the parent only, with the given ports open to the
// world, gone with the parent.
type ChildSpec struct {
	Parent       GCPConnection
	ProjectHash  string
	Name         string
	User         string
	PublicKey    string
	MachineType  string
	BootDiskSize string
	ImageFamily  string
	ImageProject string
	Expose       []int
}

// ChildInstance is a child VM as GCP reports it.
type ChildInstance struct {
	Name        string
	Instance    string
	Status      string
	MachineType string
	InternalIP  string
	ExternalIP  string
	Expose      []int
}

// ChildInstanceName is the instance name of a child: the parent's name and
// the child's, so a listing shows whose it is and the firewall rules and
// network tag named after it stay unique.
func ChildInstanceName(parent, name string) string {
	return parent + "-" + name
}

// ParentAddress returns the parent's internal IP and the network it is on,
// which the child's firewall rules refer to.
func ParentAddress(parent GCPConnection) (internalIP, network string, err error) {
	output, err := captureGcloud(
		"compute", "instances", "describe", parent.InstanceName,
		"--project", parent.ProjectID,
		"--zone", parent.Zone,
		"--format=value(networkInterfaces[0].networkIP,networkInterfaces[0].network.basename())",
	)
	if err != nil {
		return "", "", err
	}
	fields := strings.Split(strings.TrimSpace(output), "\t")
	if len(fields) != 2 || fields[0] == "" || fields[1] == "" {
		return "", "", fmt.Errorf("unexpected describe output for %s: %q", parent.InstanceName, output)
	}
	return fields[0], fields[1], nil
}

// CreateChildInstance makes the firewall rules first, so the instance is
// never up without them, then the instance, and reads its addresses back.
func CreateChildInstance(spec ChildSpec, debug bool) (ChildInstance, error) {
	instance := ChildInstanceName(spec.Parent.InstanceName, spec.Name)
	parentIP, network, err := ParentAddress(spec.Parent)
	if err != nil {
		return ChildInstance{}, err
	}
	expose := exposeLabel(spec.Expose)
	if len(expose) > 63 {
		return ChildInstance{}, fmt.Errorf("too many ports to record in a label: %s", expose)
	}
	if err := createChildFirewall(spec.Parent, instance, network, parentIP, spec.Expose, debug); err != nil {
		return ChildInstance{}, err
	}
	labels := fmt.Sprintf("app=isotty,project_hash=%s,backend=vm,isotty_parent=%s,isotty_child=%s,isotty_expose=%s",
		spec.ProjectHash, spec.Parent.InstanceName, spec.Name, expose)
	err = runGcloud(debug,
		"compute", "instances", "create", instance,
		"--quiet",
		"--project", spec.Parent.ProjectID,
		"--zone", spec.Parent.Zone,
		"--machine-type", spec.MachineType,
		"--boot-disk-size", spec.BootDiskSize,
		"--image-family", spec.ImageFamily,
		"--image-project", spec.ImageProject,
		"--no-service-account",
		"--no-scopes",
		"--labels", labels,
		"--tags", instance,
		// The key the parent holds is the only way in; OS Login would ignore it.
		"--metadata", "enable-oslogin=false,ssh-keys="+spec.User+":"+strings.TrimSpace(spec.PublicKey),
	)
	if err != nil {
		// Rules without an instance would block the name for good.
		return ChildInstance{}, errors.Join(err, deleteChildFirewall(spec.Parent, instance, debug))
	}
	return describeChild(spec.Parent, instance, spec.Name, spec.MachineType, spec.Expose)
}

func exposeLabel(ports []int) string {
	parts := make([]string, 0, len(ports))
	for _, port := range ports {
		parts = append(parts, strconv.Itoa(port))
	}
	return strings.Join(parts, "-")
}

func parseExposeLabel(label string) []int {
	var ports []int
	for _, part := range strings.Split(label, "-") {
		if port, err := strconv.Atoi(part); err == nil {
			ports = append(ports, port)
		}
	}
	return ports
}

// createChildFirewall admits SSH from the parent and the exposed ports from
// anywhere, and denies every other ingress. The deny rule is what keeps a
// default network's own rules, which admit SSH from everywhere and
// everything from inside, from reaching the child; they rank below it.
func createChildFirewall(parent GCPConnection, instance, network, parentIP string, expose []int, debug bool) error {
	rule := func(name, priority, action, rules, source string) error {
		return runGcloud(debug,
			"compute", "firewall-rules", "create", name,
			"--quiet",
			"--project", parent.ProjectID,
			"--network", network,
			"--direction", "INGRESS",
			"--priority", priority,
			"--action", action,
			"--rules", rules,
			"--source-ranges", source,
			"--target-tags", instance,
		)
	}
	if err := rule(instance+"-ssh", "800", "ALLOW", "tcp:22", parentIP+"/32"); err != nil {
		return err
	}
	if len(expose) > 0 {
		rules := make([]string, 0, len(expose))
		for _, port := range expose {
			rules = append(rules, "tcp:"+strconv.Itoa(port))
		}
		if err := rule(instance+"-expose", "850", "ALLOW", strings.Join(rules, ","), "0.0.0.0/0"); err != nil {
			return err
		}
	}
	return rule(instance+"-deny", "900", "DENY", "all", "0.0.0.0/0")
}

func describeChild(parent GCPConnection, instance, name, machineType string, expose []int) (ChildInstance, error) {
	output, err := captureGcloud(
		"compute", "instances", "describe", instance,
		"--project", parent.ProjectID,
		"--zone", parent.Zone,
		"--format=value(status,networkInterfaces[0].networkIP,networkInterfaces[0].accessConfigs[0].natIP)",
	)
	if err != nil {
		return ChildInstance{}, err
	}
	fields := strings.Split(strings.TrimRight(output, "\n"), "\t")
	if len(fields) != 3 {
		return ChildInstance{}, fmt.Errorf("unexpected describe output for %s: %q", instance, output)
	}
	return ChildInstance{Name: name, Instance: instance, Status: fields[0], MachineType: machineType, InternalIP: fields[1], ExternalIP: fields[2], Expose: expose}, nil
}

// ListChildInstances returns the parent's children, by the label create
// puts on them.
func ListChildInstances(parent GCPConnection) ([]ChildInstance, error) {
	output, err := captureGcloud(
		"compute", "instances", "list",
		"--project", parent.ProjectID,
		"--filter=labels.isotty_parent="+parent.InstanceName,
		"--format=value(name,status,machineType.basename(),networkInterfaces[0].networkIP,networkInterfaces[0].accessConfigs[0].natIP,labels.isotty_child,labels.isotty_expose)",
	)
	if err != nil {
		return nil, err
	}
	var children []ChildInstance
	for _, line := range strings.Split(strings.TrimRight(output, "\n"), "\n") {
		if line == "" {
			continue
		}
		fields := strings.Split(line, "\t")
		if len(fields) != 7 {
			return nil, fmt.Errorf("unexpected instance line from gcloud: %q", line)
		}
		children = append(children, ChildInstance{
			Instance: fields[0], Status: fields[1], MachineType: fields[2], InternalIP: fields[3], ExternalIP: fields[4],
			Name: fields[5], Expose: parseExposeLabel(fields[6]),
		})
	}
	return children, nil
}

// StartChildInstance starts a stopped child; its external IP is a new one.
func StartChildInstance(parent GCPConnection, instance string, debug bool) error {
	return powerChild(parent, instance, "start", debug)
}

// StopChildInstance stops a child without deleting it; the disk, the rules
// and the key stay, and `isotty stop` or `down` still delete it.
func StopChildInstance(parent GCPConnection, instance string, debug bool) error {
	return powerChild(parent, instance, "stop", debug)
}

func powerChild(parent GCPConnection, instance, operation string, debug bool) error {
	return runGcloud(debug,
		"compute", "instances", operation, instance,
		"--quiet",
		"--project", parent.ProjectID,
		"--zone", parent.Zone,
	)
}

// DeleteChildInstance removes the instance and then its firewall rules;
// the rules go last so a half-deleted child is never reachable.
func DeleteChildInstance(parent GCPConnection, instance string, debug bool) error {
	if err := runGcloud(debug,
		"compute", "instances", "delete", instance,
		"--quiet",
		"--project", parent.ProjectID,
		"--zone", parent.Zone,
	); err != nil {
		return err
	}
	return deleteChildFirewall(parent, instance, debug)
}

var ruleSuffixes = []string{"-ssh", "-expose", "-deny"}

func childRuleNames(instance string) []string {
	names := make([]string, 0, len(ruleSuffixes))
	for _, suffix := range ruleSuffixes {
		names = append(names, instance+suffix)
	}
	return names
}

// deleteChildFirewall removes the child's rules that exist. The listing is
// by prefix and the names are matched here: gcloud's filter language has
// no alternation, and another child's name may share the prefix.
func deleteChildFirewall(parent GCPConnection, instance string, debug bool) error {
	output, err := captureGcloud(
		"compute", "firewall-rules", "list",
		"--project", parent.ProjectID,
		"--filter=name~^"+instance+"-",
		"--format=value(name)",
	)
	if err != nil {
		return err
	}
	var names []string
	for _, name := range strings.Fields(output) {
		if slices.Contains(childRuleNames(instance), name) {
			names = append(names, name)
		}
	}
	if len(names) == 0 {
		return nil
	}
	args := append([]string{"compute", "firewall-rules", "delete"}, names...)
	return runGcloud(debug, append(args, "--quiet", "--project", parent.ProjectID)...)
}

// DeleteOrphanRules removes rules of the parent's children whose instance
// is gone, left by a create that failed or a deletion by hand; `stop` and
// `down` call it after the children.
func DeleteOrphanRules(parent GCPConnection, debug bool) error {
	output, err := captureGcloud(
		"compute", "firewall-rules", "list",
		"--project", parent.ProjectID,
		"--filter=name~^"+parent.InstanceName+"-",
		"--format=value(name)",
	)
	if err != nil {
		return err
	}
	var names []string
	for _, name := range strings.Fields(output) {
		for _, suffix := range ruleSuffixes {
			if strings.HasSuffix(name, suffix) && strings.HasPrefix(name, parent.InstanceName+"-") {
				names = append(names, name)
			}
		}
	}
	if len(names) == 0 {
		return nil
	}
	args := append([]string{"compute", "firewall-rules", "delete"}, names...)
	return runGcloud(debug, append(args, "--quiet", "--project", parent.ProjectID)...)
}
