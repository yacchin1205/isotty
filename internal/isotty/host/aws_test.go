package host

import (
	"bytes"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/yazawa/isotty/internal/isotty/host/aws"
)

func stubAWSCheck(t *testing.T, err error) {
	t.Helper()
	saved := checkAWS
	checkAWS = func(aws.ProjectConfig, aws.HostConfig, string) error { return err }
	t.Cleanup(func() { checkAWS = saved })
}

func TestAWSEnablePinsTheRoleAfterTheCheck(t *testing.T) {
	project, home := t.TempDir(), t.TempDir()
	stubAWSCheck(t, nil)
	var stdout, stderr bytes.Buffer
	if err := runAWS(project, "abc", home, []string{"enable", "--role-arn", "arn:aws:iam::123456789012:role/isotty-agent", "--profile", "admin"}, &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadAWSProjectConfig(project, "abc", home)
	if err != nil || cfg == nil || cfg.RoleARN != "arn:aws:iam::123456789012:role/isotty-agent" {
		t.Fatal(cfg, err)
	}
	stdout.Reset()
	if err := runAWS(project, "abc", home, []string{"show"}, &stdout, &stderr); err != nil || !strings.Contains(stdout.String(), "profile: admin") || !strings.Contains(stdout.String(), "session: isotty-abc") {
		t.Fatalf("show: %v\n%s", err, stdout.String())
	}
	if err := runAWS(project, "abc", home, []string{"disable"}, &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	if cfg, err := LoadAWSProjectConfig(project, "abc", home); err != nil || cfg != nil {
		t.Fatal(cfg, err)
	}
}

func TestAWSEnablePinsNothingWhenTheRoleCannotBeAssumed(t *testing.T) {
	project, home := t.TempDir(), t.TempDir()
	stubAWSCheck(t, errors.New("AccessDenied"))
	var stdout, stderr bytes.Buffer
	err := runAWS(project, "abc", home, []string{"enable", "--role-arn", "arn:aws:iam::123456789012:role/isotty-agent"}, &stdout, &stderr)
	if err == nil || !strings.Contains(err.Error(), "nothing was pinned") {
		t.Fatalf("enable: %v", err)
	}
	if !strings.Contains(stderr.String(), "sts:AssumeRole") {
		t.Fatalf("the guide was not shown:\n%s", stderr.String())
	}
	if _, err := os.Stat(aws.ProjectConfigPath(project)); !os.IsNotExist(err) {
		t.Fatalf("aws.yaml was written: %v", err)
	}
	if err := runAWS(project, "abc", home, []string{"enable", "--role-arn", "arn:aws:iam::123456789012:user/me"}, &stdout, &stderr); err == nil || !strings.Contains(err.Error(), "invalid role_arn") {
		t.Fatalf("a user ARN: %v", err)
	}
}
