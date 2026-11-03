// Package child creates VMs next to the project's VM for the agent, through
// the host's gcloud login, within the size the human agreed to. A child is
// reachable over SSH from the parent only and goes with it. See docs/CHILD.md.
package child

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"sigs.k8s.io/yaml"
)

var (
	machineTypePattern = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)
	diskSizePattern    = regexp.MustCompile(`^([1-9][0-9]*)GB$`)
	namePattern        = regexp.MustCompile(`^[a-z]([a-z0-9-]{0,18}[a-z0-9])?$`)
	userPattern        = regexp.MustCompile(`^[a-z_][a-z0-9_-]{0,31}$`)
	imagePattern       = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)
)

// ProjectConfig is the agreement: how many children, of which machine
// types, with how much disk, with which ports open to the world. What runs
// on them is the agent's business.
type ProjectConfig struct {
	Max             int      `json:"max"`
	MachineTypes    []string `json:"machine_types"`
	MaxBootDiskSize string   `json:"max_boot_disk_size"`
	Expose          []int    `json:"expose"`
}

func ProjectConfigPath(projectPath string) string {
	return filepath.Join(projectPath, ".isotty", "children.yaml")
}

// LoadProjectConfig reports enabled=false when the project allows no children.
func LoadProjectConfig(projectPath string) (ProjectConfig, bool, error) {
	path := ProjectConfigPath(projectPath)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return ProjectConfig{}, false, nil
		}
		return ProjectConfig{}, false, fmt.Errorf("read %s: %w", path, err)
	}
	cfg, err := ParseProjectConfig(data)
	if err != nil {
		return ProjectConfig{}, false, fmt.Errorf("parse %s: %w", path, err)
	}
	return cfg, true, nil
}

func ParseProjectConfig(data []byte) (ProjectConfig, error) {
	var cfg ProjectConfig
	if err := yaml.UnmarshalStrict(data, &cfg); err != nil {
		return ProjectConfig{}, err
	}
	if err := cfg.Validate(); err != nil {
		return ProjectConfig{}, err
	}
	return cfg, nil
}

func SaveProjectConfig(projectPath string, cfg ProjectConfig) error {
	if err := cfg.Validate(); err != nil {
		return err
	}
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("marshal children config: %w", err)
	}
	path := ProjectConfigPath(projectPath)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create children config directory: %w", err)
	}
	return os.WriteFile(path, data, 0o644)
}

func RemoveProjectConfig(projectPath string) error {
	path := ProjectConfigPath(projectPath)
	if err := os.Remove(path); err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("children are not enabled for this project (%s does not exist)", path)
		}
		return fmt.Errorf("remove %s: %w", path, err)
	}
	return nil
}

func (c ProjectConfig) Validate() error {
	if c.Max < 1 {
		return errors.New("max must be at least 1")
	}
	if len(c.MachineTypes) == 0 {
		return errors.New("machine_types must name at least one machine type")
	}
	for _, machineType := range c.MachineTypes {
		if !machineTypePattern.MatchString(machineType) {
			return fmt.Errorf("invalid machine type %q", machineType)
		}
	}
	if _, err := DiskGB(c.MaxBootDiskSize); err != nil {
		return fmt.Errorf("max_boot_disk_size: %w", err)
	}
	seen := map[int]bool{}
	for _, port := range c.Expose {
		if port < 1 || port > 65535 {
			return fmt.Errorf("invalid port %d", port)
		}
		if seen[port] {
			return fmt.Errorf("port %d is listed twice", port)
		}
		seen[port] = true
	}
	// The ports of a child are recorded in a label, which holds 63 characters.
	if len(strings.Join(strings.Fields(strings.Trim(fmt.Sprint(c.Expose), "[]")), "-")) > 63 {
		return errors.New("expose lists more ports than a label can hold")
	}
	return nil
}

// DiskGB reads a size written as the VM config writes it, such as 200GB.
func DiskGB(size string) (int, error) {
	m := diskSizePattern.FindStringSubmatch(size)
	if m == nil {
		return 0, fmt.Errorf("invalid disk size %q: write it as <number>GB", size)
	}
	return strconv.Atoi(m[1])
}

// Request is what the VM asks for; the relay checks it against the
// agreement before any credential is used.
type Request struct {
	Name         string `json:"name"`
	User         string `json:"user"`
	MachineType  string `json:"machine_type"`
	BootDiskSize string `json:"boot_disk_size"`
	ImageFamily  string `json:"image_family"`
	ImageProject string `json:"image_project"`
	Expose       []int  `json:"expose"`
}

// Allows checks a request against the agreement, `existing` being the
// number of children the parent already has.
func (c ProjectConfig) Allows(req Request, existing int) error {
	if !namePattern.MatchString(req.Name) {
		return fmt.Errorf("invalid name %q: lower-case letters, digits and dashes, 20 characters at most", req.Name)
	}
	if !userPattern.MatchString(req.User) {
		return fmt.Errorf("invalid user %q", req.User)
	}
	if existing >= c.Max {
		return fmt.Errorf("this project may have %d child VM(s) and has %d; delete one first", c.Max, existing)
	}
	if !slices.Contains(c.MachineTypes, req.MachineType) {
		return fmt.Errorf("machine type %s is not one this project may use: %s", req.MachineType, strings.Join(c.MachineTypes, ", "))
	}
	size, err := DiskGB(req.BootDiskSize)
	if err != nil {
		return err
	}
	limit, _ := DiskGB(c.MaxBootDiskSize)
	if size > limit {
		return fmt.Errorf("boot disk %s is over the %s this project may use", req.BootDiskSize, c.MaxBootDiskSize)
	}
	if !imagePattern.MatchString(req.ImageFamily) || !imagePattern.MatchString(req.ImageProject) {
		return fmt.Errorf("invalid image %s/%s", req.ImageProject, req.ImageFamily)
	}
	for _, port := range req.Expose {
		if !slices.Contains(c.Expose, port) {
			return fmt.Errorf("port %d is not one this project may expose: %s", port, portList(c.Expose))
		}
	}
	return nil
}

func portList(ports []int) string {
	if len(ports) == 0 {
		return "none"
	}
	parts := make([]string, 0, len(ports))
	for _, port := range ports {
		parts = append(parts, strconv.Itoa(port))
	}
	return strings.Join(parts, ", ")
}

func (c ProjectConfig) sorted() ProjectConfig {
	c.MachineTypes = slices.Sorted(slices.Values(c.MachineTypes))
	c.Expose = slices.Sorted(slices.Values(c.Expose))
	return c
}

func (c ProjectConfig) equal(other ProjectConfig) bool {
	a, b := c.sorted(), other.sorted()
	return a.Max == b.Max && a.MaxBootDiskSize == b.MaxBootDiskSize &&
		slices.Equal(a.MachineTypes, b.MachineTypes) && slices.Equal(a.Expose, b.Expose)
}

// Summary is the agreement in one line, for `show` and the pin check.
func (c ProjectConfig) Summary() string {
	s := c.sorted()
	return fmt.Sprintf("max %d, machine types %s, boot disk up to %s, ports %s", s.Max, strings.Join(s.MachineTypes, ","), s.MaxBootDiskSize, portList(s.Expose))
}
