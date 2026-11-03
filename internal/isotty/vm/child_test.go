package vmcfg

import (
	"errors"
	"strings"
	"testing"
)

// fakeGcloud records the gcloud commands and answers the capture ones.
type fakeGcloud struct {
	runs     []string
	captures []string
	answer   func(args string) (string, error)
}

func installFakeGcloud(t *testing.T, answer func(args string) (string, error)) *fakeGcloud {
	t.Helper()
	f := &fakeGcloud{answer: answer}
	originalRun, originalCapture := runGcloud, captureGcloud
	runGcloud = func(debug bool, args ...string) error {
		f.runs = append(f.runs, strings.Join(args, " "))
		return nil
	}
	captureGcloud = func(args ...string) (string, error) {
		joined := strings.Join(args, " ")
		f.captures = append(f.captures, joined)
		return f.answer(joined)
	}
	t.Cleanup(func() { runGcloud, captureGcloud = originalRun, originalCapture })
	return f
}

var testParent = GCPConnection{ProjectID: "demo-project", Zone: "us-central1-f", InstanceName: "isotty-abc123"}

func TestCreateChildInstanceBuildsRulesThenInstance(t *testing.T) {
	f := installFakeGcloud(t, func(args string) (string, error) {
		switch {
		case strings.Contains(args, "describe isotty-abc123 "):
			return "10.128.0.2\tdefault\n", nil
		case strings.Contains(args, "describe isotty-abc123-web "):
			return "RUNNING\t10.128.0.7\t203.0.113.7\n", nil
		}
		t.Fatalf("unexpected capture %q", args)
		return "", nil
	})
	child, err := CreateChildInstance(ChildSpec{
		Parent: testParent, ProjectHash: "abc123", Name: "web", User: "dev", PublicKey: "ssh-ed25519 AAAA key\n",
		MachineType: "e2-standard-8", BootDiskSize: "100GB", ImageFamily: "ubuntu-2404-lts-amd64", ImageProject: "ubuntu-os-cloud",
		Expose: []int{80, 443},
	}, false)
	if err != nil {
		t.Fatalf("CreateChildInstance() error = %v", err)
	}
	if child.Instance != "isotty-abc123-web" || child.InternalIP != "10.128.0.7" || child.ExternalIP != "203.0.113.7" || child.Status != "RUNNING" {
		t.Fatalf("child = %+v", child)
	}
	if len(f.runs) != 4 {
		t.Fatalf("runs = %v, want three firewall rules then the instance", f.runs)
	}
	for i, want := range []string{
		"firewall-rules create isotty-abc123-web-ssh --quiet --project demo-project --network default --direction INGRESS --priority 800 --action ALLOW --rules tcp:22 --source-ranges 10.128.0.2/32 --target-tags isotty-abc123-web",
		"firewall-rules create isotty-abc123-web-expose --quiet --project demo-project --network default --direction INGRESS --priority 850 --action ALLOW --rules tcp:80,tcp:443 --source-ranges 0.0.0.0/0 --target-tags isotty-abc123-web",
		"firewall-rules create isotty-abc123-web-deny --quiet --project demo-project --network default --direction INGRESS --priority 900 --action DENY --rules all --source-ranges 0.0.0.0/0 --target-tags isotty-abc123-web",
	} {
		if !strings.Contains(f.runs[i], want) {
			t.Errorf("run %d = %q, want %q", i, f.runs[i], want)
		}
	}
	create := f.runs[3]
	for _, want := range []string{
		"compute instances create isotty-abc123-web --quiet --project demo-project --zone us-central1-f --machine-type e2-standard-8 --boot-disk-size 100GB --image-family ubuntu-2404-lts-amd64 --image-project ubuntu-os-cloud --no-service-account --no-scopes",
		"--labels app=isotty,project_hash=abc123,backend=vm,isotty_parent=isotty-abc123,isotty_child=web,isotty_expose=80-443",
		"--tags isotty-abc123-web",
		"--metadata enable-oslogin=false,ssh-keys=dev:ssh-ed25519 AAAA key",
	} {
		if !strings.Contains(create, want) {
			t.Errorf("create = %q, lacks %q", create, want)
		}
	}
}

func TestCreateChildInstanceWithoutExposedPortsMakesNoExposeRule(t *testing.T) {
	f := installFakeGcloud(t, func(args string) (string, error) {
		if strings.Contains(args, "describe isotty-abc123 ") {
			return "10.128.0.2\tdefault\n", nil
		}
		return "RUNNING\t10.128.0.7\t203.0.113.7\n", nil
	})
	if _, err := CreateChildInstance(ChildSpec{Parent: testParent, Name: "big", User: "dev", PublicKey: "k", MachineType: "n2-highmem-4", BootDiskSize: "50GB", ImageFamily: "f", ImageProject: "p"}, false); err != nil {
		t.Fatal(err)
	}
	if len(f.runs) != 3 || strings.Contains(strings.Join(f.runs, "\n"), "-expose") {
		t.Fatalf("runs = %v", f.runs)
	}
}

func TestCreateChildInstanceRemovesTheRulesWhenTheInstanceFails(t *testing.T) {
	f := installFakeGcloud(t, func(args string) (string, error) {
		switch {
		case strings.Contains(args, "describe isotty-abc123 "):
			return "10.128.0.2\tdefault\n", nil
		case strings.Contains(args, "firewall-rules list"):
			return "isotty-abc123-web-ssh\nisotty-abc123-web-deny\n", nil
		}
		t.Fatalf("unexpected capture %q", args)
		return "", nil
	})
	originalRun := runGcloud
	runGcloud = func(debug bool, args ...string) error {
		f.runs = append(f.runs, strings.Join(args, " "))
		if strings.HasPrefix(strings.Join(args, " "), "compute instances create") {
			return errors.New("ZONE_RESOURCE_POOL_EXHAUSTED")
		}
		return nil
	}
	t.Cleanup(func() { runGcloud = originalRun })
	_, err := CreateChildInstance(ChildSpec{Parent: testParent, Name: "web", User: "dev", PublicKey: "k", MachineType: "e2-standard-8", BootDiskSize: "50GB", ImageFamily: "f", ImageProject: "p"}, false)
	if err == nil || !strings.Contains(err.Error(), "ZONE_RESOURCE_POOL_EXHAUSTED") {
		t.Fatalf("err = %v", err)
	}
	last := f.runs[len(f.runs)-1]
	if !strings.Contains(last, "firewall-rules delete isotty-abc123-web-ssh isotty-abc123-web-deny --quiet") {
		t.Fatalf("runs = %v, want the rules removed after the failed create", f.runs)
	}
}

func TestDeleteOrphanRulesKeepsOtherParentsRules(t *testing.T) {
	f := installFakeGcloud(t, func(args string) (string, error) {
		return "isotty-abc123-web-ssh\nisotty-abc123-web-deny\nisotty-abc123-web-2-expose\nisotty-abc123-other\n", nil
	})
	if err := DeleteOrphanRules(testParent, false); err != nil {
		t.Fatal(err)
	}
	if len(f.runs) != 1 || !strings.Contains(f.runs[0], "firewall-rules delete isotty-abc123-web-ssh isotty-abc123-web-deny isotty-abc123-web-2-expose --quiet") {
		t.Fatalf("runs = %v", f.runs)
	}
}

func TestListAndDeleteChildren(t *testing.T) {
	f := installFakeGcloud(t, func(args string) (string, error) {
		switch {
		case strings.Contains(args, "instances list"):
			return "isotty-abc123-web\tRUNNING\te2-standard-8\t10.128.0.7\t203.0.113.7\tweb\t80-443\nisotty-abc123-big\tRUNNING\tn2-highmem-4\t10.128.0.8\t203.0.113.8\tbig\t\n", nil
		case strings.Contains(args, "firewall-rules list") && strings.Contains(args, "--filter=name~^isotty-abc123-web-"):
			// The prefix also matches the rules of a child named web-2.
			return "isotty-abc123-web-ssh\nisotty-abc123-web-deny\nisotty-abc123-web-expose\nisotty-abc123-web-2-ssh\n", nil
		case strings.Contains(args, "firewall-rules list"):
			return "", nil
		}
		t.Fatalf("unexpected capture %q", args)
		return "", nil
	})
	children, err := ListChildInstances(testParent)
	if err != nil || len(children) != 2 {
		t.Fatalf("ListChildInstances() = %v, %v", children, err)
	}
	if children[0].Name != "web" || len(children[0].Expose) != 2 || children[0].Expose[1] != 443 || len(children[1].Expose) != 0 {
		t.Fatalf("children = %+v", children)
	}
	if !strings.Contains(f.captures[0], "--filter=labels.isotty_parent=isotty-abc123") {
		t.Fatalf("list = %q", f.captures[0])
	}

	for _, child := range children {
		if err := DeleteChildInstance(testParent, child.Instance, false); err != nil {
			t.Fatal(err)
		}
	}
	if len(f.runs) != 3 {
		t.Fatalf("runs = %v, want two instance deletes and one rule delete", f.runs)
	}
	if !strings.HasPrefix(f.runs[0], "compute instances delete isotty-abc123-web --quiet") || !strings.Contains(f.runs[1], "firewall-rules delete isotty-abc123-web-ssh isotty-abc123-web-deny isotty-abc123-web-expose --quiet --project demo-project") {
		t.Fatalf("runs = %v", f.runs)
	}
}
