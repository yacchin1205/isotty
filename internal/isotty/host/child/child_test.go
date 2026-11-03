package child

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"

	"github.com/yazawa/isotty/internal/isotty/host/pin"
	vmcfg "github.com/yazawa/isotty/internal/isotty/vm"
)

func testConfig() ProjectConfig {
	return ProjectConfig{Max: 2, MachineTypes: []string{"e2-standard-8", "n2-highmem-4"}, MaxBootDiskSize: "200GB", Expose: []int{80, 443}}
}

var testParent = vmcfg.GCPConnection{ProjectID: "demo-project", Zone: "us-central1-f", InstanceName: "isotty-abc123"}

// fakeCloud holds the children and remembers what it was asked to make.
type fakeCloud struct {
	children []vmcfg.ChildInstance
	specs    []vmcfg.ChildSpec
	deleted  []string
	fail     error
}

func (f *fakeCloud) Create(spec vmcfg.ChildSpec, debug bool) (vmcfg.ChildInstance, error) {
	f.specs = append(f.specs, spec)
	if f.fail != nil {
		return vmcfg.ChildInstance{}, f.fail
	}
	child := vmcfg.ChildInstance{Name: spec.Name, Instance: vmcfg.ChildInstanceName(spec.Parent.InstanceName, spec.Name), Status: "RUNNING", MachineType: spec.MachineType, InternalIP: "10.128.0.9", ExternalIP: "203.0.113.9", Expose: spec.Expose}
	f.children = append(f.children, child)
	return child, nil
}

func (f *fakeCloud) List(parent vmcfg.GCPConnection) ([]vmcfg.ChildInstance, error) {
	return f.children, nil
}

func (f *fakeCloud) Delete(parent vmcfg.GCPConnection, instance string, debug bool) error {
	f.deleted = append(f.deleted, instance)
	kept := f.children[:0]
	for _, child := range f.children {
		if child.Instance != instance {
			kept = append(kept, child)
		}
	}
	f.children = kept
	return nil
}

func newTestGateway(t *testing.T, cloud *fakeCloud) (*httptest.Server, *[]Event) {
	t.Helper()
	var events []Event
	relay := NewRelay(testConfig(), cloud, testParent, "abc123", Defaults{BootDiskSize: "50GB", ImageFamily: "ubuntu-2404-lts-amd64", ImageProject: "ubuntu-os-cloud"})
	gateway := httptest.NewServer(Handler(relay, func(event Event) error {
		events = append(events, event)
		return nil
	}))
	t.Cleanup(gateway.Close)
	return gateway, &events
}

func doJSON(t *testing.T, method, url string, body any) (int, map[string]any) {
	t.Helper()
	data := []byte(nil)
	if body != nil {
		data, _ = json.Marshal(body)
	}
	req, _ := http.NewRequest(method, url, bytes.NewReader(data))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var payload map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode, payload
}

func TestParseProjectConfig(t *testing.T) {
	cfg, err := ParseProjectConfig([]byte("max: 2\nmachine_types: [e2-standard-8, n2-highmem-4]\nmax_boot_disk_size: 200GB\nexpose: [80, 443]\n"))
	if err != nil || !cfg.equal(testConfig()) {
		t.Fatalf("ParseProjectConfig() = %+v, %v", cfg, err)
	}
	if _, err := ParseProjectConfig([]byte("max: 1\nmachine_types: [e2-standard-4]\nmax_boot_disk_size: 50GB\n")); err != nil {
		t.Fatalf("no expose should be fine: %v", err)
	}
	for _, bad := range []string{
		"max: 0\nmachine_types: [e2-standard-4]\nmax_boot_disk_size: 50GB\n",
		"max: 1\nmachine_types: []\nmax_boot_disk_size: 50GB\n",
		"max: 1\nmachine_types: [E2]\nmax_boot_disk_size: 50GB\n",
		"max: 1\nmachine_types: [e2-standard-4]\nmax_boot_disk_size: 50\n",
		"max: 1\nmachine_types: [e2-standard-4]\nmax_boot_disk_size: 50GB\nexpose: [0]\n",
		"max: 1\nmachine_types: [e2-standard-4]\nmax_boot_disk_size: 50GB\nexpose: [80, 80]\n",
		"max: 1\nmachine_types: [e2-standard-4]\nmax_boot_disk_size: 50GB\nimages: [x]\n",
	} {
		if _, err := ParseProjectConfig([]byte(bad)); err == nil {
			t.Fatalf("ParseProjectConfig(%q) should fail", bad)
		}
	}
}

func TestAllows(t *testing.T) {
	cfg := testConfig()
	ok := Request{Name: "web", User: "dev", MachineType: "e2-standard-8", BootDiskSize: "100GB", ImageFamily: "ubuntu-2404-lts-amd64", ImageProject: "ubuntu-os-cloud", Expose: []int{443}}
	if err := cfg.Allows(ok, 1); err != nil {
		t.Fatalf("Allows() = %v", err)
	}
	for name, change := range map[string]func(r *Request){
		"too many":     func(r *Request) {},
		"machine type": func(r *Request) { r.MachineType = "n2-standard-64" },
		"disk":         func(r *Request) { r.BootDiskSize = "201GB" },
		"port":         func(r *Request) { r.Expose = []int{22} },
		"name":         func(r *Request) { r.Name = "Web Server" },
		"user":         func(r *Request) { r.User = "root; rm" },
		"image":        func(r *Request) { r.ImageFamily = "../etc" },
	} {
		req := ok
		req.Expose = append([]int(nil), ok.Expose...)
		change(&req)
		existing := 1
		if name == "too many" {
			existing = 2
		}
		if err := cfg.Allows(req, existing); err == nil {
			t.Errorf("Allows(%s) should fail", name)
		}
	}
}

func TestVerifyPinned(t *testing.T) {
	homeDir := t.TempDir()
	if _, err := VerifyPinned(homeDir, "abc", testConfig()); err == nil || !strings.Contains(err.Error(), "not pinned") {
		t.Fatalf("VerifyPinned() without pin = %v", err)
	}
	if err := pin.Save(homeDir, "abc", PinKind, testConfig()); err != nil {
		t.Fatal(err)
	}
	reordered := testConfig()
	reordered.MachineTypes = []string{"n2-highmem-4", "e2-standard-8"}
	if _, err := VerifyPinned(homeDir, "abc", reordered); err != nil {
		t.Fatalf("VerifyPinned(reordered) = %v", err)
	}
	widened := testConfig()
	widened.Max = 10
	if _, err := VerifyPinned(homeDir, "abc", widened); err == nil || !strings.Contains(err.Error(), "differs") {
		t.Fatalf("VerifyPinned(widened) = %v", err)
	}
}

func TestRelayCreatesWithinTheAgreement(t *testing.T) {
	cloud := &fakeCloud{}
	gateway, events := newTestGateway(t, cloud)

	status, payload := doJSON(t, "POST", gateway.URL+"/create", map[string]any{"name": "web", "user": "dev", "machine_type": "e2-standard-8", "expose": []int{80, 443}})
	if status != 200 {
		t.Fatalf("create status = %d, payload = %v", status, payload)
	}
	spec := cloud.specs[0]
	if spec.Parent != testParent || spec.ProjectHash != "abc123" || spec.User != "dev" || spec.BootDiskSize != "50GB" || spec.ImageFamily != "ubuntu-2404-lts-amd64" || spec.ImageProject != "ubuntu-os-cloud" {
		t.Fatalf("spec = %+v, want the defaults filled in", spec)
	}
	publicKey, _, _, _, err := ssh.ParseAuthorizedKey([]byte(spec.PublicKey))
	if err != nil {
		t.Fatalf("public key %q: %v", spec.PublicKey, err)
	}
	signer, err := ssh.ParsePrivateKey([]byte(payload["private_key"].(string)))
	if err != nil {
		t.Fatalf("private key: %v", err)
	}
	if !bytes.Equal(signer.PublicKey().Marshal(), publicKey.Marshal()) {
		t.Fatal("the private key handed to the VM does not match the public key put on the child")
	}
	child := payload["child"].(map[string]any)
	if child["Instance"] != "isotty-abc123-web" || child["ExternalIP"] != "203.0.113.9" || payload["user"] != "dev" {
		t.Fatalf("payload = %v", payload)
	}
	if len(*events) != 1 || (*events)[0] != (Event{Operation: "create", Name: "web", Instance: "isotty-abc123-web", Shape: "e2-standard-8 50GB ubuntu-os-cloud/ubuntu-2404-lts-amd64", Expose: "80, 443", ExternalIP: "203.0.113.9", Result: "RUNNING"}) {
		t.Fatalf("events = %+v", *events)
	}

	status, _ = doJSON(t, "POST", gateway.URL+"/create", map[string]any{"name": "web", "user": "dev", "machine_type": "e2-standard-8"})
	if status != 403 {
		t.Fatalf("duplicate name status = %d", status)
	}
	status, _ = doJSON(t, "POST", gateway.URL+"/create", map[string]any{"name": "big", "user": "dev", "machine_type": "n2-highmem-4", "boot_disk_size": "200GB", "image_family": "debian-12", "image_project": "debian-cloud"})
	if status != 200 || cloud.specs[1].ImageFamily != "debian-12" {
		t.Fatalf("second child status = %d, spec = %+v", status, cloud.specs[1])
	}
	status, payload = doJSON(t, "POST", gateway.URL+"/create", map[string]any{"name": "third", "user": "dev", "machine_type": "e2-standard-8"})
	if status != 403 || !strings.Contains(payload["error"].(string), "may have 2 child VM(s)") {
		t.Fatalf("third child status = %d, payload = %v", status, payload)
	}
	if len(cloud.specs) != 2 {
		t.Fatalf("the cloud was asked for %d children, want 2", len(cloud.specs))
	}

	status, payload = doJSON(t, "GET", gateway.URL+"/list", nil)
	if status != 200 || len(payload["children"].([]any)) != 2 {
		t.Fatalf("list = %d %v", status, payload)
	}
	status, payload = doJSON(t, "POST", gateway.URL+"/delete", map[string]any{"name": "web"})
	if status != 200 || payload["instance"] != "isotty-abc123-web" || strings.Join(cloud.deleted, ",") != "isotty-abc123-web" {
		t.Fatalf("delete = %d %v, deleted = %v", status, payload, cloud.deleted)
	}
	status, _ = doJSON(t, "POST", gateway.URL+"/delete", map[string]any{"name": "web"})
	if status != 404 {
		t.Fatalf("delete again status = %d", status)
	}
	last := (*events)[len(*events)-1]
	if last.Operation != "delete" || last.Result != "no such child VM" {
		t.Fatalf("last event = %+v", last)
	}
}

func TestRelayRefusesOutsideTheAgreement(t *testing.T) {
	cloud := &fakeCloud{}
	gateway, events := newTestGateway(t, cloud)
	for _, body := range []map[string]any{
		{"name": "web", "user": "dev", "machine_type": "n2-standard-64"},
		{"name": "web", "user": "dev", "machine_type": "e2-standard-8", "expose": []int{22}},
		{"name": "web", "user": "dev", "machine_type": "e2-standard-8", "boot_disk_size": "500GB"},
	} {
		status, payload := doJSON(t, "POST", gateway.URL+"/create", body)
		if status != 403 {
			t.Fatalf("create %v: status = %d, payload = %v", body, status, payload)
		}
	}
	if len(cloud.specs) != 0 {
		t.Fatalf("the cloud was asked for %v; refused requests must not reach it", cloud.specs)
	}
	for _, event := range *events {
		if event.Result != "refused" || event.Instance != "" {
			t.Fatalf("event = %+v", event)
		}
	}
}

func TestRelayReportsACreateFailure(t *testing.T) {
	cloud := &fakeCloud{fail: errors.New("gcloud: quota exceeded")}
	gateway, events := newTestGateway(t, cloud)
	status, payload := doJSON(t, "POST", gateway.URL+"/create", map[string]any{"name": "web", "user": "dev", "machine_type": "e2-standard-8"})
	if status != 502 || !strings.Contains(payload["error"].(string), "quota exceeded") {
		t.Fatalf("status = %d, payload = %v", status, payload)
	}
	if (*events)[0].Result != "gcloud: quota exceeded" || (*events)[0].Instance != "isotty-abc123-web" {
		t.Fatalf("event = %+v", (*events)[0])
	}
}

func TestSetupCommand(t *testing.T) {
	cmd := SetupCommand([]string{"claude", "codex"})
	for _, want := range []string{"/usr/local/bin/isotty-child", "00-isotty.conf", ".claude/skills/isotty-child", ".codex/skills/isotty-child"} {
		if !strings.Contains(cmd, want) {
			t.Errorf("SetupCommand() lacks %q", want)
		}
	}
	if strings.Contains(render(clientScript), "__ISOTTY_GATEWAY_PORT__") {
		t.Error("the client script keeps the port placeholder")
	}
}

func TestClientScript(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 is not installed")
	}
	cloud := &fakeCloud{}
	gateway, _ := newTestGateway(t, cloud)
	home := t.TempDir()
	script := filepath.Join(home, "isotty-child")
	if err := os.WriteFile(script, []byte(strings.ReplaceAll(clientScript, "http://127.0.0.1:__ISOTTY_GATEWAY_PORT__/child", gateway.URL)), 0o755); err != nil {
		t.Fatal(err)
	}
	run := func(args ...string) (string, int) {
		cmd := exec.Command("python3", append([]string{script}, args...)...)
		cmd.Env = append(os.Environ(), "HOME="+home)
		out, err := cmd.CombinedOutput()
		code := 0
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			code = exitErr.ExitCode()
		} else if err != nil {
			t.Fatal(err)
		}
		return string(out), code
	}
	if out, code := run("status"); code != 0 || !strings.Contains(out, "up to 2 child VM(s) of type e2-standard-8, n2-highmem-4") {
		t.Fatalf("status = %q, %d", out, code)
	}
	if out, code := run("create", "web", "--machine-type", "e2-standard-8", "--expose", "80", "--expose", "443"); code != 0 || !strings.Contains(out, "web\tRUNNING\te2-standard-8\t10.128.0.9\t203.0.113.9\t80,443") {
		t.Fatalf("create = %q, %d", out, code)
	}
	key := filepath.Join(home, ".ssh", "isotty-child", "web")
	info, err := os.Stat(key)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("key file: %v, mode %v", err, info.Mode())
	}
	if _, err := ssh.ParsePrivateKey(mustRead(t, key)); err != nil {
		t.Fatalf("the key file is not a private key: %v", err)
	}
	if out, code := run("create", "web2", "--machine-type", "e2-standard-8", "--expose", "22"); code != 1 || !strings.Contains(out, "port 22 is not one this project may expose") {
		t.Fatalf("create outside = %q, %d", out, code)
	}
	if out, code := run("list"); code != 0 || !strings.Contains(out, "web\tRUNNING") {
		t.Fatalf("list = %q, %d", out, code)
	}
	if out, code := run("delete", "web"); code != 0 || !strings.Contains(out, "deleted isotty-abc123-web") {
		t.Fatalf("delete = %q, %d", out, code)
	}
	if _, err := os.Stat(key); !os.IsNotExist(err) {
		t.Fatal("the key must go with the child")
	}
}

func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}
