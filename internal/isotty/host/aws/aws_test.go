package aws

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yazawa/isotty/internal/isotty/host/pin"
)

const testRole = "arn:aws:iam::123456789012:role/isotty-agent"

func TestProjectConfig(t *testing.T) {
	cfg, err := ParseProjectConfig([]byte("role_arn: " + testRole + "\n"))
	if err != nil || cfg.Account() != "123456789012" {
		t.Fatal(cfg, err)
	}
	if cfg, err := ParseProjectConfig([]byte("role_arn: arn:aws-cn:iam::123456789012:role/path/to/agent\n")); err != nil || cfg.Account() != "123456789012" {
		t.Fatal(cfg, err)
	}
	for _, bad := range []string{
		"role_arn: arn:aws:iam::123456789012:user/someone\n",
		"role_arn: arn:aws:iam::12345:role/agent\n",
		"role_arn: " + testRole + "\nprofile: admin\n",
		"{}\n",
	} {
		if _, err := ParseProjectConfig([]byte(bad)); err == nil {
			t.Fatalf("ParseProjectConfig(%q) should fail", bad)
		}
	}
	if err := (HostConfig{Profile: "a b"}).Validate(); err == nil {
		t.Fatal("a profile with a space was accepted")
	}
}

// The profile is the host's matter: it is kept beside the pin, not in the
// synced file, and only the role is compared with the pin.
func TestSaveKeepsTheProfileOffTheSyncedFile(t *testing.T) {
	project, home := t.TempDir(), t.TempDir()
	if err := Save(project, "abc", home, ProjectConfig{RoleARN: testRole}, HostConfig{Profile: "admin"}); err != nil {
		t.Fatal(err)
	}
	synced, _ := os.ReadFile(ProjectConfigPath(project))
	if strings.Contains(string(synced), "admin") {
		t.Fatalf("the profile is in the synced file:\n%s", synced)
	}
	if host, err := LoadHostConfig(home, "abc"); err != nil || host.Profile != "admin" {
		t.Fatal(host, err)
	}
	cfg, _, _ := LoadProjectConfig(project)
	if _, err := VerifyPinned(home, "abc", cfg); err != nil {
		t.Fatal(err)
	}
	if _, err := VerifyPinned(home, "abc", ProjectConfig{RoleARN: "arn:aws:iam::123456789012:role/admin"}); err == nil || !strings.Contains(err.Error(), "differs from what is pinned") {
		t.Fatalf("a swapped role: %v", err)
	}
	if err := Remove(project, "abc", home); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{ProjectConfigPath(project), HostConfigPath(home, "abc"), pin.Path(home, "abc", PinKind)} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("%s was kept: %v", path, err)
		}
	}
}

// fakeAWS stands in for the host's aws CLI and records what it was asked.
type fakeAWS struct {
	calls    [][]string
	envs     [][]string
	account  string
	expires  time.Time
	failWith error
}

func (f *fakeAWS) run(env []string, args ...string) ([]byte, error) {
	f.calls = append(f.calls, args)
	f.envs = append(f.envs, env)
	if f.failWith != nil {
		return nil, f.failWith
	}
	switch strings.Join(args[:2], " ") {
	case "sts assume-role":
		return json.Marshal(map[string]any{"Credentials": map[string]any{
			"AccessKeyId": "ASIA" + string(rune('A'+len(f.calls))), "SecretAccessKey": "secret", "SessionToken": "token",
			"Expiration": f.expires.Format("2006-01-02T15:04:05+00:00"),
		}})
	case "sts get-caller-identity":
		return json.Marshal(map[string]string{"Account": f.account, "Arn": "arn:aws:sts::" + f.account + ":assumed-role/isotty-agent/isotty-abc"})
	}
	return nil, errors.New("unexpected aws " + strings.Join(args, " "))
}

func newTestIssuer(profile string) (*Issuer, *fakeAWS, *time.Time) {
	now := time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)
	fake := &fakeAWS{account: "123456789012", expires: now.Add(time.Hour)}
	issuer := NewIssuer(ProjectConfig{RoleARN: testRole}, HostConfig{Profile: profile}, "abc")
	issuer.run = fake.run
	issuer.now = func() time.Time { return now }
	return issuer, fake, &now
}

func TestIssuerAssumesTheRoleAndReuses(t *testing.T) {
	issuer, fake, now := newTestIssuer("admin")
	creds, err := issuer.Issue(noRecord)
	if err != nil || creds.Token != "token" || creds.RoleArn != testRole || !creds.Expiration.Equal(now.Add(time.Hour)) {
		t.Fatalf("Issue() = %+v %v", creds, err)
	}
	args := strings.Join(fake.calls[0], " ")
	for _, want := range []string{"--role-arn " + testRole, "--role-session-name isotty-abc", "--duration-seconds 3600", "--profile admin"} {
		if !strings.Contains(args, want) {
			t.Fatalf("assume-role args lack %q: %s", want, args)
		}
	}
	*now = now.Add(35 * time.Minute)
	if _, err := issuer.Issue(noRecord); err != nil || len(fake.calls) != 1 {
		t.Fatal("fresh credentials were not reused")
	}
	// Inside the SDKs' refresh window, new credentials.
	*now = now.Add(6 * time.Minute)
	if _, err := issuer.Issue(noRecord); err != nil || len(fake.calls) != 2 {
		t.Fatal("credentials near expiry were reused")
	}
	fake.failWith = errors.New("aws sts assume-role: exit status 254 AccessDenied")
	*now = now.Add(2 * time.Hour)
	if _, err := issuer.Issue(noRecord); err == nil || !strings.Contains(err.Error(), "AccessDenied") {
		t.Fatalf("a refused role: %v", err)
	}
}

func TestIssuerWithoutAProfileUsesTheCLIDefault(t *testing.T) {
	issuer, fake, _ := newTestIssuer("")
	if _, err := issuer.Issue(noRecord); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.Join(fake.calls[0], " "), "--profile") {
		t.Fatalf("args: %v", fake.calls[0])
	}
}

func TestCheckAsksWhoseCredentialsTheyAre(t *testing.T) {
	t.Setenv("AWS_PROFILE", "admin")
	t.Setenv("AWS_ACCESS_KEY_ID", "AKIAHUMAN")
	t.Setenv("AWS_CA_BUNDLE", "/etc/corp-ca.pem")
	issuer, fake, _ := newTestIssuer("")
	if err := issuer.Check(); err != nil {
		t.Fatal(err)
	}
	env := strings.Join(fake.envs[1], "\n")
	if !strings.Contains(env, "AWS_SESSION_TOKEN=token") || strings.Contains(env, "AKIAHUMAN") || strings.Contains(env, "AWS_PROFILE") || !strings.Contains(env, "AWS_CA_BUNDLE=/etc/corp-ca.pem") {
		t.Fatalf("get-caller-identity ran with:\n%s", env)
	}
	issuer, fake, _ = newTestIssuer("")
	fake.account = "999999999999"
	if err := issuer.Check(); err == nil || !strings.Contains(err.Error(), "999999999999") {
		t.Fatalf("another account: %v", err)
	}
}

func TestHandlerAnswersAsAContainerCredentialsEndpoint(t *testing.T) {
	issuer, _, _ := newTestIssuer("")
	var events []IssueEvent
	failRecord := false
	server := httptest.NewServer(Handler(issuer, func(e IssueEvent) error {
		if failRecord {
			return errors.New("disk full")
		}
		events = append(events, e)
		return nil
	}))
	defer server.Close()

	get := func(path string) (int, map[string]any) {
		resp, err := http.Get(server.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var body map[string]any
		data, _ := io.ReadAll(resp.Body)
		json.Unmarshal(data, &body)
		return resp.StatusCode, body
	}
	failRecord = true
	if code, body := get("/credentials"); code != 500 || body["AccessKeyId"] != nil {
		t.Fatalf("unrecorded credentials were handed out: %d %v", code, body)
	}
	// The retry assumes and records again rather than getting them unrecorded.
	failRecord = false
	code, body := get("/credentials")
	if code != 200 || body["Token"] != "token" || body["AccessKeyId"] == "" || body["Expiration"] != "2026-11-24T13:00:00Z" {
		t.Fatalf("credentials: %d %v", code, body)
	}
	if len(events) != 1 || events[0].Session != "isotty-abc" || events[0].RoleARN != testRole {
		t.Fatalf("events %+v", events)
	}
	if _, status := get("/status"); status["account"] != "123456789012" || status["role_arn"] != testRole {
		t.Fatalf("status %v", status)
	}
}

// The client points the command at the gateway and removes what would
// answer as another identity before it.
func TestClientExec(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 is not available")
	}
	issuer, _, _ := newTestIssuer("")
	issuer.now = time.Now
	issuer.creds = Credentials{AccessKeyID: "ASIAX", SecretAccessKey: "s", Token: "t", Expiration: time.Now().Add(time.Hour), RoleArn: testRole}
	server := httptest.NewServer(http.StripPrefix("/aws", Handler(issuer, func(IssueEvent) error { return nil })))
	defer server.Close()
	script := strings.ReplaceAll(clientScript, "http://127.0.0.1:__ISOTTY_GATEWAY_PORT__/aws", server.URL+"/aws")
	path := filepath.Join(t.TempDir(), "isotty-aws")
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("python3", path, "exec", "--", "env")
	cmd.Env = append(os.Environ(), "AWS_PROFILE=mine", "AWS_ACCESS_KEY_ID=AKIAMINE", "AWS_REGION=ap-northeast-1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	env := string(out)
	if !strings.Contains(env, "AWS_CONTAINER_CREDENTIALS_FULL_URI="+server.URL+"/aws/credentials") || strings.Contains(env, "AWS_PROFILE") || strings.Contains(env, "AKIAMINE") || !strings.Contains(env, "AWS_REGION=ap-northeast-1") {
		t.Fatalf("exec env:\n%s", env)
	}
}

func noRecord(Credentials) error { return nil }
