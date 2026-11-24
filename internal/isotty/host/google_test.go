package host

import (
	"bytes"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/yazawa/isotty/internal/isotty/host/google"
)

const testServiceAccount = "isotty@my-project.iam.gserviceaccount.com"

func stubGoogleCheck(t *testing.T, err error) *[]google.ProjectConfig {
	t.Helper()
	var checked []google.ProjectConfig
	saved := checkGoogle
	checkGoogle = func(_ string, cfg google.ProjectConfig) error {
		checked = append(checked, cfg)
		return err
	}
	t.Cleanup(func() { checkGoogle = saved })
	return &checked
}

func TestGoogleEnableImpersonatesAfterTheCheck(t *testing.T) {
	project, home := t.TempDir(), t.TempDir()
	checked := stubGoogleCheck(t, nil)
	var stdout, stderr bytes.Buffer
	if err := runGoogle(project, "abc", home, []string{"enable", "--impersonate", testServiceAccount, "--scope", "cloud-platform"}, &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadGoogleProjectConfig(project, "abc", home)
	if err != nil || cfg == nil || cfg.Credential != google.CredentialImpersonation || cfg.ServiceAccount != testServiceAccount {
		t.Fatal(cfg, err)
	}
	if len(*checked) != 1 || (*checked)[0].Credential != google.CredentialImpersonation {
		t.Fatalf("checked %+v", *checked)
	}
	// --scope alone keeps the pinned service account and its credential.
	if err := runGoogle(project, "abc", home, []string{"enable", "--scope", "cloud-platform.read-only"}, &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	if cfg, _ := LoadGoogleProjectConfig(project, "abc", home); cfg.Credential != google.CredentialImpersonation || cfg.Scopes[0] != "cloud-platform.read-only" {
		t.Fatalf("after --scope: %+v", cfg)
	}
	if err := runGoogle(project, "abc", home, []string{"show"}, &stdout, &stderr); err != nil || !strings.Contains(stdout.String(), "credential: impersonation") || strings.Contains(stdout.String(), "key:") {
		t.Fatalf("show: %v\n%s", err, stdout.String())
	}
	if err := runGoogle(project, "abc", home, []string{"enable", "--key", "k.json", "--impersonate", testServiceAccount, "--scope", "x"}, &stdout, &stderr); err == nil || !strings.Contains(err.Error(), "not both") {
		t.Fatalf("both credentials: %v", err)
	}
}

func TestGoogleEnablePinsNothingWhenNoTokenIsIssued(t *testing.T) {
	project, home := t.TempDir(), t.TempDir()
	stubGoogleCheck(t, errors.New("PERMISSION_DENIED"))
	var stdout, stderr bytes.Buffer
	err := runGoogle(project, "abc", home, []string{"enable", "--impersonate", testServiceAccount, "--scope", "cloud-platform"}, &stdout, &stderr)
	if err == nil || !strings.Contains(err.Error(), "nothing was pinned") {
		t.Fatalf("enable: %v", err)
	}
	if !strings.Contains(stderr.String(), "roles/iam.serviceAccountTokenCreator") {
		t.Fatalf("the guide was not shown:\n%s", stderr.String())
	}
	if _, err := os.Stat(google.ProjectConfigPath(project)); !os.IsNotExist(err) {
		t.Fatalf("google.yaml was written: %v", err)
	}
	if cfg, err := LoadGoogleProjectConfig(project, "abc", home); err != nil || cfg != nil {
		t.Fatal(cfg, err)
	}
}
