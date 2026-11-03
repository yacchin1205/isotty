package dns

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

	"github.com/yazawa/isotty/internal/isotty/host/pin"
)

func testConfig() ProjectConfig {
	return ProjectConfig{Provider: ProviderCloudDNS, Zones: []Zone{{
		Project: "example-project", ManagedZone: "test-zone",
		Names: []string{"lab.example.org"},
	}}}
}

// fakeCloudDNS holds one zone's record sets and the changes it was asked for.
type fakeCloudDNS struct {
	server  *httptest.Server
	rrsets  []map[string]any
	changes []map[string]any
	auths   []string
	refuse  bool
}

func newFakeCloudDNS(t *testing.T) *fakeCloudDNS {
	t.Helper()
	f := &fakeCloudDNS{rrsets: []map[string]any{
		{"name": "other.example.org.", "type": "A", "ttl": 300, "rrdatas": []string{"192.0.2.9"}},
		{"name": "lab.example.org.", "type": "A", "ttl": 300, "rrdatas": []string{"192.0.2.1"}},
		{"name": "*.lab.example.org.", "type": "A", "ttl": 300, "rrdatas": []string{"192.0.2.1"}},
	}}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /dns/v1/projects/example-project/managedZones", func(w http.ResponseWriter, r *http.Request) {
		f.auths = append(f.auths, r.Header.Get("Authorization"))
		json.NewEncoder(w).Encode(map[string]any{"managedZones": []map[string]any{
			{"name": "test-zone", "dnsName": "example.org.", "visibility": "public"},
			{"name": "lab-zone", "dnsName": "lab.example.org.", "visibility": "public"},
			{"name": "lab-private", "dnsName": "lab.example.org.", "visibility": "private"},
			{"name": "other-zone", "dnsName": "example.net.", "visibility": "public"},
		}})
	})
	mux.HandleFunc("GET /dns/v1/projects/example-project/managedZones/test-zone/rrsets", func(w http.ResponseWriter, r *http.Request) {
		f.auths = append(f.auths, r.Header.Get("Authorization"))
		// Two pages, to exercise the paging.
		if r.URL.Query().Get("pageToken") == "" {
			json.NewEncoder(w).Encode(map[string]any{"rrsets": f.rrsets[:1], "nextPageToken": "p2"})
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"rrsets": f.rrsets[1:]})
	})
	mux.HandleFunc("POST /dns/v1/projects/example-project/managedZones/test-zone/changes", func(w http.ResponseWriter, r *http.Request) {
		f.auths = append(f.auths, r.Header.Get("Authorization"))
		if f.refuse {
			w.WriteHeader(http.StatusForbidden)
			json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{"code": 403, "message": "Forbidden"}})
			return
		}
		var change map[string]any
		if err := json.NewDecoder(r.Body).Decode(&change); err != nil {
			t.Fatal(err)
		}
		f.changes = append(f.changes, change)
		for _, deletion := range change["deletions"].([]any) {
			d := deletion.(map[string]any)
			f.rrsets = deleteRrset(f.rrsets, d["name"].(string), d["type"].(string))
		}
		for _, addition := range change["additions"].([]any) {
			f.rrsets = append(f.rrsets, addition.(map[string]any))
		}
		json.NewEncoder(w).Encode(map[string]any{"status": "pending"})
	})
	f.server = httptest.NewServer(mux)
	t.Cleanup(f.server.Close)
	return f
}

func deleteRrset(rrsets []map[string]any, name, rtype string) []map[string]any {
	var kept []map[string]any
	for _, rrset := range rrsets {
		if rrset["name"] != name || rrset["type"] != rtype {
			kept = append(kept, rrset)
		}
	}
	return kept
}

func (f *fakeCloudDNS) client() *Client {
	return newClient(f.server.URL, func() (string, error) { return "ya29.host", nil })
}

func newTestGateway(t *testing.T, f *fakeCloudDNS) (*httptest.Server, string, *[]ChangeEvent) {
	t.Helper()
	homeDir := t.TempDir()
	var events []ChangeEvent
	relay := NewRelay(testConfig(), f.client(), homeDir, "abc")
	gateway := httptest.NewServer(Handler(relay, func(event ChangeEvent) error {
		events = append(events, event)
		return nil
	}))
	t.Cleanup(gateway.Close)
	return gateway, homeDir, &events
}

func doJSON(t *testing.T, method, url string, body any) (int, map[string]any) {
	t.Helper()
	var reader *bytes.Reader
	if body != nil {
		data, _ := json.Marshal(body)
		reader = bytes.NewReader(data)
	} else {
		reader = bytes.NewReader(nil)
	}
	req, _ := http.NewRequest(method, url, reader)
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
	cfg, err := ParseProjectConfig([]byte("provider: clouddns\nzones:\n  - project: example-project\n    managed_zone: test-zone\n    names: [lab.example.org]\n"))
	if err != nil {
		t.Fatalf("ParseProjectConfig() error = %v", err)
	}
	if !cfg.equal(testConfig()) {
		t.Fatalf("cfg = %+v", cfg)
	}
	zone := func(body string) string {
		return "provider: clouddns\nzones:\n  - project: example-project\n    managed_zone: test-zone\n" + body
	}
	for _, bad := range []string{
		"provider: clouddns\nzones: []\n",
		"zones:\n  - project: example-project\n    managed_zone: test-zone\n    names: [a.example.org]\n",
		"provider: route53\nzones:\n  - project: example-project\n    managed_zone: test-zone\n    names: [a.example.org]\n",
		zone("    names: ['*.example.org']\n"),
		zone("    names: [Example.org]\n"),
		zone("    names: [example.org.]\n"),
		zone("    names: []\n"),
		zone("    names: [example.org]\n    types: [A]\n"),
		zone("    names: [a.example.org, a.example.org]\n"),
	} {
		if _, err := ParseProjectConfig([]byte(bad)); err == nil {
			t.Fatalf("ParseProjectConfig(%q) should fail", bad)
		}
	}
}

func TestAllows(t *testing.T) {
	cfg := testConfig()
	for _, name := range []string{"lab.example.org", "app.lab.example.org", "*.lab.example.org", "App.LAB.example.org.", "_acme-challenge.x.lab.example.org"} {
		if _, ok := cfg.Allows(name); !ok {
			t.Errorf("Allows(%q) = false", name)
		}
	}
	for _, name := range []string{"example.org", "lab2.example.org", "xlab.example.org", "lab.example.org.evil.example"} {
		if _, ok := cfg.Allows(name); ok {
			t.Errorf("Allows(%q) = true", name)
		}
	}
}

func TestPutAndDelete(t *testing.T) {
	cfg := testConfig()
	cfg.Put(Zone{Project: "example-project", ManagedZone: "test-zone", Names: []string{"other-lab.example.org", "lab.example.org"}})
	if len(cfg.Zones) != 1 || strings.Join(cfg.Zones[0].Names, ",") != "lab.example.org,other-lab.example.org" {
		t.Fatalf("Put() = %+v", cfg.Zones)
	}
	cfg.Put(Zone{Project: "other", ManagedZone: "z", Names: []string{"a.example.org"}})
	if len(cfg.Zones) != 2 {
		t.Fatalf("Put(other zone) = %+v", cfg.Zones)
	}
	if !cfg.Delete("lab.example.org") || cfg.Delete("lab.example.org") {
		t.Fatal("Delete() should remove the name once")
	}
	if !cfg.Delete("a.example.org") || len(cfg.Zones) != 1 || len(cfg.Zones[0].Names) != 1 {
		t.Fatalf("Delete(last name of a zone) = %+v", cfg.Zones)
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
	if _, err := VerifyPinned(homeDir, "abc", testConfig()); err != nil {
		t.Fatalf("VerifyPinned(same) = %v", err)
	}
	widened := testConfig()
	widened.Zones[0].Names = append(widened.Zones[0].Names, "example.org")
	if _, err := VerifyPinned(homeDir, "abc", widened); err == nil || !strings.Contains(err.Error(), "differs") {
		t.Fatalf("VerifyPinned(widened) = %v", err)
	}
}

func TestRelaySetsAndDeletesWithinThePin(t *testing.T) {
	f := newFakeCloudDNS(t)
	gateway, homeDir, events := newTestGateway(t, f)

	status, payload := doJSON(t, "POST", gateway.URL+"/set", map[string]any{"name": "app.lab.example.org", "type": "a", "rrdatas": []string{"192.0.2.5"}})
	if status != 200 || payload["status"] != "pending" {
		t.Fatalf("set status = %d, payload = %v", status, payload)
	}
	change := f.changes[0]
	addition := change["additions"].([]any)[0].(map[string]any)
	if len(change["deletions"].([]any)) != 0 || addition["name"] != "app.lab.example.org." || addition["type"] != "A" || addition["ttl"].(float64) != 300 {
		t.Fatalf("change = %v", change)
	}
	if f.auths[0] != "Bearer ya29.host" {
		t.Fatalf("auth = %q", f.auths[0])
	}
	tracked, err := LoadTracked(homeDir, "abc")
	if err != nil || len(tracked) != 1 || tracked[0] != (Tracked{Provider: ProviderCloudDNS, Project: "example-project", ManagedZone: "test-zone", Name: "app.lab.example.org", Type: "A"}) {
		t.Fatalf("tracked = %v, %v", tracked, err)
	}

	// Replacing keeps one change: the old record out, the new one in.
	status, _ = doJSON(t, "POST", gateway.URL+"/set", map[string]any{"name": "lab.example.org", "type": "A", "ttl": 60, "rrdatas": []string{"192.0.2.2", "192.0.2.3"}})
	if status != 200 {
		t.Fatalf("replace status = %d", status)
	}
	change = f.changes[1]
	deletion := change["deletions"].([]any)[0].(map[string]any)
	if deletion["name"] != "lab.example.org." || deletion["rrdatas"].([]any)[0] != "192.0.2.1" {
		t.Fatalf("replace change = %v", change)
	}
	if tracked, _ := LoadTracked(homeDir, "abc"); len(tracked) != 1 {
		t.Fatalf("a replaced record is not tracked: %v", tracked)
	}

	status, payload = doJSON(t, "POST", gateway.URL+"/delete", map[string]any{"name": "app.lab.example.org", "type": "A"})
	if status != 200 || payload["record"].(map[string]any)["rrdatas"].([]any)[0] != "192.0.2.5" {
		t.Fatalf("delete status = %d, payload = %v", status, payload)
	}
	if tracked, _ := LoadTracked(homeDir, "abc"); len(tracked) != 0 {
		t.Fatalf("tracked after delete = %v", tracked)
	}
	status, _ = doJSON(t, "POST", gateway.URL+"/delete", map[string]any{"name": "app.lab.example.org", "type": "A"})
	if status != 404 {
		t.Fatalf("delete again status = %d", status)
	}

	want := []ChangeEvent{
		{Operation: "set", Zone: "example-project/test-zone", Name: "app.lab.example.org", Type: "A", After: "300 192.0.2.5", Result: "pending"},
		{Operation: "set", Zone: "example-project/test-zone", Name: "lab.example.org", Type: "A", Before: "300 192.0.2.1", After: "60 192.0.2.2 192.0.2.3", Result: "pending"},
		{Operation: "delete", Zone: "example-project/test-zone", Name: "app.lab.example.org", Type: "A", Before: "300 192.0.2.5", Result: "pending"},
		{Operation: "delete", Zone: "example-project/test-zone", Name: "app.lab.example.org", Type: "A", Result: "no such record"},
	}
	if len(*events) != len(want) {
		t.Fatalf("events = %+v", *events)
	}
	for i, event := range want {
		if (*events)[i] != event {
			t.Errorf("event %d = %+v, want %+v", i, (*events)[i], event)
		}
	}
}

func TestRelayRefusesOutsideThePin(t *testing.T) {
	f := newFakeCloudDNS(t)
	gateway, _, events := newTestGateway(t, f)
	for _, body := range []map[string]any{
		{"name": "other.example.org", "type": "A", "rrdatas": []string{"192.0.2.5"}},
		{"name": "example.org", "type": "TXT", "rrdatas": []string{`"v=spf1 -all"`}},
	} {
		status, payload := doJSON(t, "POST", gateway.URL+"/set", body)
		if status != 403 || !strings.Contains(payload["error"].(string), "outside what this project may change") {
			t.Fatalf("set %v: status = %d, payload = %v", body, status, payload)
		}
	}
	// Any type under a pinned name is the VM's to ask for.
	if status, _ := doJSON(t, "POST", gateway.URL+"/set", map[string]any{"name": "lab.example.org", "type": "CNAME", "rrdatas": []string{"x.example."}}); status != 200 {
		t.Fatalf("CNAME under the pin: status = %d", status)
	}
	if status, _ := doJSON(t, "POST", gateway.URL+"/delete", map[string]any{"name": "other.example.org", "type": "A"}); status != 403 {
		t.Fatalf("delete outside: status = %d", status)
	}
	if status, _ := doJSON(t, "GET", gateway.URL+"/list?name=other.example.org", nil); status != 403 {
		t.Fatalf("list outside: status = %d", status)
	}
	if len(f.changes) != 1 {
		t.Fatalf("changes = %v, want only the CNAME under the pin", f.changes)
	}
	if len(*events) != 4 || (*events)[0].Result != "refused" || (*events)[1].Result != "refused" || (*events)[2].Type != "CNAME" || (*events)[2].Result != "pending" || (*events)[3].Operation != "delete" || (*events)[3].Result != "refused" {
		t.Fatalf("events = %+v", *events)
	}
}

func TestRelayListsOnlyThePinnedNames(t *testing.T) {
	f := newFakeCloudDNS(t)
	gateway, _, _ := newTestGateway(t, f)
	status, payload := doJSON(t, "GET", gateway.URL+"/list", nil)
	if status != 200 {
		t.Fatalf("list status = %d", status)
	}
	var names []string
	for _, record := range payload["records"].([]any) {
		names = append(names, record.(map[string]any)["name"].(string))
	}
	if strings.Join(names, ",") != "lab.example.org,*.lab.example.org" {
		t.Fatalf("names = %v, want the pinned name and the wildcard under it, not other.example.org", names)
	}
	status, payload = doJSON(t, "GET", gateway.URL+"/list?name=*.lab.example.org", nil)
	if status != 200 || len(payload["records"].([]any)) != 1 {
		t.Fatalf("list under a name: status = %d, payload = %v", status, payload)
	}
}

func TestRelayRelaysTheAPIsRefusal(t *testing.T) {
	f := newFakeCloudDNS(t)
	f.refuse = true
	gateway, _, events := newTestGateway(t, f)
	status, payload := doJSON(t, "POST", gateway.URL+"/set", map[string]any{"name": "app.lab.example.org", "type": "A", "rrdatas": []string{"192.0.2.5"}})
	if status != 502 || payload["error"].(map[string]any)["status"].(float64) != 403 {
		t.Fatalf("status = %d, payload = %v", status, payload)
	}
	if (*events)[0].Result != "Cloud DNS answered 403: Forbidden" {
		t.Fatalf("event = %+v", (*events)[0])
	}
}

func TestChooseZone(t *testing.T) {
	f := newFakeCloudDNS(t)
	zones, err := f.client().ManagedZones("example-project")
	if err != nil || len(zones) != 4 {
		t.Fatalf("ManagedZones() = %v, %v", zones, err)
	}
	if zone, err := ChooseZone(zones, "app.example.org", ""); err != nil || zone.Name != "test-zone" {
		t.Fatalf("ChooseZone(app.example.org) = %v, %v", zone, err)
	}
	if zone, err := ChooseZone(zones, "example.org", ""); err != nil || zone.Name != "test-zone" {
		t.Fatalf("ChooseZone(example.org) = %v, %v", zone, err)
	}
	if _, err := ChooseZone(zones, "app.lab.example.org", ""); err == nil || !strings.Contains(err.Error(), "lab-zone (public) and lab-private (private)") {
		t.Fatalf("ChooseZone(two zones) = %v", err)
	}
	if zone, err := ChooseZone(zones, "app.lab.example.org", "lab-private"); err != nil || zone.Name != "lab-private" {
		t.Fatalf("ChooseZone(--managed-zone) = %v, %v", zone, err)
	}
	if _, err := ChooseZone(zones, "app.example.org", "lab-zone"); err == nil || !strings.Contains(err.Error(), "is not in managed zone lab-zone") {
		t.Fatalf("ChooseZone(wrong --managed-zone) = %v", err)
	}
	if _, err := ChooseZone(zones, "app.example.com", ""); err == nil || !strings.Contains(err.Error(), "no managed zone") {
		t.Fatalf("ChooseZone(unserved) = %v", err)
	}
	if _, err := ChooseZone(zones, "app.example.org", "nope"); err == nil || !strings.Contains(err.Error(), "does not exist") {
		t.Fatalf("ChooseZone(unknown zone) = %v", err)
	}
}

func TestRemaining(t *testing.T) {
	f := newFakeCloudDNS(t)
	remaining, err := Remaining(f.client(), []Tracked{
		{Provider: ProviderCloudDNS, Project: "example-project", ManagedZone: "test-zone", Name: "lab.example.org", Type: "A"},
		{Provider: ProviderCloudDNS, Project: "example-project", ManagedZone: "test-zone", Name: "gone.lab.example.org", Type: "A"},
	})
	if err != nil || len(remaining) != 1 || remaining[0].Name != "lab.example.org" {
		t.Fatalf("Remaining() = %v, %v", remaining, err)
	}
}

func TestClientRenewsTheTokenAndReportsGcloudFailures(t *testing.T) {
	calls := 0
	client := newClient("http://127.0.0.1:1", func() (string, error) {
		calls++
		if calls == 1 {
			return "", errors.New("gcloud: not logged in")
		}
		return "tok", nil
	})
	if _, err := client.bearer(false); err == nil || !strings.Contains(err.Error(), "not logged in") {
		t.Fatalf("bearer() = %v", err)
	}
	for range 2 {
		if token, err := client.bearer(false); err != nil || token != "tok" {
			t.Fatalf("bearer() = %q, %v", token, err)
		}
	}
	if calls != 2 {
		t.Fatalf("token calls = %d, want the failure and one success reused", calls)
	}
}

func TestClientRetriesOnceWithAFreshTokenAfter401(t *testing.T) {
	var auths []string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auths = append(auths, r.Header.Get("Authorization"))
		if r.Header.Get("Authorization") != "Bearer fresh" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"name": "test-zone", "dnsName": "example.org."})
	}))
	t.Cleanup(upstream.Close)
	issued := 0
	client := newClient(upstream.URL, func() (string, error) {
		issued++
		if issued == 1 {
			return "stale", nil
		}
		return "fresh", nil
	})
	zones, err := client.ManagedZones("example-project")
	if err != nil {
		t.Fatalf("ManagedZones() = %v", err)
	}
	if len(zones) != 0 || strings.Join(auths, ",") != "Bearer stale,Bearer fresh" || issued != 2 {
		t.Fatalf("zones = %v, auths = %v, issued = %d", zones, auths, issued)
	}
}

func TestSetupCommand(t *testing.T) {
	cmd := SetupCommand([]string{"claude"})
	for _, want := range []string{"/usr/local/bin/isotty-dns", "00-isotty.conf", ".claude/skills/isotty-dns"} {
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
	f := newFakeCloudDNS(t)
	gateway, _, _ := newTestGateway(t, f)
	script := filepath.Join(t.TempDir(), "isotty-dns")
	if err := os.WriteFile(script, []byte(strings.ReplaceAll(clientScript, "http://127.0.0.1:__ISOTTY_GATEWAY_PORT__/dns", gateway.URL)), 0o755); err != nil {
		t.Fatal(err)
	}
	run := func(args ...string) (string, int) {
		cmd := exec.Command("python3", append([]string{script}, args...)...)
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
	if out, code := run("status"); code != 0 || !strings.Contains(out, "(clouddns)") || !strings.Contains(out, "example-project/test-zone: lab.example.org") {
		t.Fatalf("status = %q, %d", out, code)
	}
	if out, code := run("set", "A", "app.lab.example.org", "192.0.2.5", "192.0.2.6", "--ttl", "60"); code != 0 || !strings.Contains(out, "app.lab.example.org\tA\t60\t192.0.2.5 192.0.2.6") {
		t.Fatalf("set = %q, %d", out, code)
	}
	if out, code := run("list", "app.lab.example.org"); code != 0 || !strings.Contains(out, "192.0.2.5 192.0.2.6") {
		t.Fatalf("list = %q, %d", out, code)
	}
	if out, code := run("set", "A", "other.example.org", "192.0.2.5"); code != 1 || !strings.Contains(out, "outside what this project may change") {
		t.Fatalf("set outside = %q, %d", out, code)
	}
	if out, code := run("delete", "A", "app.lab.example.org"); code != 0 || !strings.Contains(out, "was:") {
		t.Fatalf("delete = %q, %d", out, code)
	}
}
