package aws

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"slices"
	"strings"
	"sync"
	"time"
)

// Credentials are the role's short-lived credentials, in the shape the AWS
// CLI and SDKs read from a container credentials endpoint.
type Credentials struct {
	AccessKeyID     string    `json:"AccessKeyId"`
	SecretAccessKey string    `json:"SecretAccessKey"`
	Token           string    `json:"Token"`
	Expiration      time.Time `json:"Expiration"`
	RoleArn         string    `json:"RoleArn"`
}

// sessionSeconds is how long a role session lasts. An hour is what AWS
// allows a role assumed from another role's session, as an SSO login is.
const sessionSeconds = 3600

// reuseMargin keeps the gateway from handing out credentials inside the
// window in which the SDKs refresh them, fifteen minutes before expiry for
// botocore, so a refresh gets new ones.
const reuseMargin = 20 * time.Minute

// runAWS runs the host's aws CLI and returns its standard output; a failure
// carries what the CLI said.
type runAWS func(env []string, args ...string) ([]byte, error)

func hostAWS(env []string, args ...string) ([]byte, error) {
	cmd := exec.Command("aws", args...)
	cmd.Env = env
	out, err := cmd.Output()
	if err != nil {
		var stderr string
		if exitErr, ok := err.(*exec.ExitError); ok {
			stderr = strings.TrimSpace(string(exitErr.Stderr))
		}
		return nil, fmt.Errorf("aws %s: %w %s", strings.Join(args[:2], " "), err, stderr)
	}
	return out, nil
}

// Issuer assumes the role with the host's aws CLI and reuses the
// credentials until they are about to expire.
type Issuer struct {
	cfg     ProjectConfig
	host    HostConfig
	session string
	run     runAWS
	now     func() time.Time

	mu    sync.Mutex
	creds Credentials
}

func NewIssuer(cfg ProjectConfig, host HostConfig, projectHash string) *Issuer {
	return &Issuer{cfg: cfg, host: host, session: SessionName(projectHash), run: hostAWS, now: time.Now}
}

// RecordError is credentials that were obtained but could not be recorded;
// they are neither kept nor handed out.
type RecordError struct{ Err error }

func (e RecordError) Error() string {
	return "the role was assumed but recording the audit event failed: " + e.Err.Error()
}

// Issue returns credentials, assuming the role when none are fresh. New
// credentials are recorded first, under the lock, and kept for reuse only
// once recorded, so no request, a retry or a concurrent one, gets
// credentials the log lacks.
func (i *Issuer) Issue(record func(Credentials) error) (Credentials, error) {
	i.mu.Lock()
	defer i.mu.Unlock()
	if i.creds.AccessKeyID != "" && i.now().Add(reuseMargin).Before(i.creds.Expiration) {
		return i.creds, nil
	}
	creds, err := i.assume()
	if err != nil {
		return Credentials{}, err
	}
	if err := record(creds); err != nil {
		return Credentials{}, RecordError{err}
	}
	i.creds = creds
	return creds, nil
}

func (i *Issuer) assume() (Credentials, error) {
	args := []string{"sts", "assume-role", "--role-arn", i.cfg.RoleARN, "--role-session-name", i.session, "--duration-seconds", fmt.Sprint(sessionSeconds), "--output", "json"}
	if i.host.Profile != "" {
		args = append(args, "--profile", i.host.Profile)
	}
	out, err := i.run(os.Environ(), args...)
	if err != nil {
		return Credentials{}, err
	}
	var answer struct {
		Credentials struct {
			AccessKeyID     string    `json:"AccessKeyId"`
			SecretAccessKey string    `json:"SecretAccessKey"`
			SessionToken    string    `json:"SessionToken"`
			Expiration      time.Time `json:"Expiration"`
		} `json:"Credentials"`
	}
	if err := json.Unmarshal(out, &answer); err != nil {
		return Credentials{}, fmt.Errorf("parse aws sts assume-role output: %w", err)
	}
	c := answer.Credentials
	if c.AccessKeyID == "" || c.SecretAccessKey == "" || c.SessionToken == "" || !c.Expiration.After(i.now()) {
		return Credentials{}, fmt.Errorf("aws sts assume-role returned no usable credentials for %s", i.cfg.RoleARN)
	}
	return Credentials{AccessKeyID: c.AccessKeyID, SecretAccessKey: c.SecretAccessKey, Token: c.SessionToken, Expiration: c.Expiration, RoleArn: i.cfg.RoleARN}, nil
}

// hostIdentities are the variables through which an identity other than
// the assumed credentials would answer.
var hostIdentities = []string{
	"AWS_ACCESS_KEY_ID", "AWS_SECRET_ACCESS_KEY", "AWS_SESSION_TOKEN", "AWS_SECURITY_TOKEN",
	"AWS_PROFILE", "AWS_DEFAULT_PROFILE",
	"AWS_CONTAINER_CREDENTIALS_FULL_URI", "AWS_CONTAINER_CREDENTIALS_RELATIVE_URI",
	"AWS_CONTAINER_AUTHORIZATION_TOKEN", "AWS_CONTAINER_AUTHORIZATION_TOKEN_FILE",
	"AWS_WEB_IDENTITY_TOKEN_FILE", "AWS_ROLE_ARN", "AWS_ROLE_SESSION_NAME",
}

// Check assumes the role and asks AWS whose credentials they are, so that a
// role the host cannot assume, or one in another account than its ARN says,
// is found at enable and not at attach.
func (i *Issuer) Check() error {
	// The credentials stay on the host, so there is nothing to record.
	creds, err := i.Issue(func(Credentials) error { return nil })
	if err != nil {
		return err
	}
	env := []string{"AWS_ACCESS_KEY_ID=" + creds.AccessKeyID, "AWS_SECRET_ACCESS_KEY=" + creds.SecretAccessKey, "AWS_SESSION_TOKEN=" + creds.Token}
	for _, kv := range os.Environ() {
		// Only the assumed credentials may answer; the region, the CA
		// bundle and the like stay as the human set them.
		if name, _, _ := strings.Cut(kv, "="); !slices.Contains(hostIdentities, name) {
			env = append(env, kv)
		}
	}
	out, err := i.run(env, "sts", "get-caller-identity", "--output", "json")
	if err != nil {
		return err
	}
	var identity struct {
		Account string `json:"Account"`
		Arn     string `json:"Arn"`
	}
	if err := json.Unmarshal(out, &identity); err != nil {
		return fmt.Errorf("parse aws sts get-caller-identity output: %w", err)
	}
	if identity.Account != i.cfg.Account() {
		return fmt.Errorf("the role's credentials belong to account %s, not %s", identity.Account, i.cfg.Account())
	}
	return nil
}
