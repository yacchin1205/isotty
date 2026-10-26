package google

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
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

const testAccount = "docs-reader@my-project.iam.gserviceaccount.com"

// fakeKey writes a service account key file whose private key the test
// generated, so signatures can be checked against the matching public key.
func fakeKey(t *testing.T, dir, tokenURI string) (string, *rsa.PrivateKey) {
	t.Helper()
	private, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(private)
	if err != nil {
		t.Fatal(err)
	}
	key := map[string]string{
		"type":         "service_account",
		"client_email": testAccount,
		"private_key":  string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})),
		"token_uri":    tokenURI,
		"project_id":   "my-project",
	}
	data, _ := json.Marshal(key)
	path := filepath.Join(dir, "key.json")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return path, private
}

func TestParseProjectConfig(t *testing.T) {
	cfg, err := ParseProjectConfig([]byte("service_account: " + testAccount + "\nscopes:\n  - spreadsheets.readonly\n  - documents.readonly\n"))
	if err != nil {
		t.Fatalf("ParseProjectConfig() error = %v", err)
	}
	if got := strings.Join(cfg.ScopeURLs(), " "); got != "https://www.googleapis.com/auth/documents.readonly https://www.googleapis.com/auth/spreadsheets.readonly" {
		t.Fatalf("ScopeURLs() = %q", got)
	}
	for _, bad := range []string{
		"service_account: " + testAccount + "\n",
		"service_account: not-an-email\nscopes: [documents]\n",
		"service_account: " + testAccount + "\nscopes: [https://www.googleapis.com/auth/documents]\n",
		"service_account: " + testAccount + "\nscopes: ['a b']\n",
		"service_account: " + testAccount + "\nscopes: [documents]\nextra: 1\n",
	} {
		if _, err := ParseProjectConfig([]byte(bad)); err == nil {
			t.Fatalf("ParseProjectConfig(%q) should fail", bad)
		}
	}
}

func TestImportKey(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	path, _ := fakeKey(t, t.TempDir(), "https://oauth2.googleapis.com/token")

	account, err := ImportKey(home, project, path)
	if err != nil || account != testAccount {
		t.Fatalf("ImportKey() = %q, %v", account, err)
	}
	info, err := os.Stat(KeyPath(home, testAccount))
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("imported key stat = %v, %v", info, err)
	}
	if key, err := LoadKey(home, testAccount); err != nil || key.ClientEmail != testAccount {
		t.Fatalf("LoadKey() = %#v, %v", key, err)
	}
	if _, err := LoadKey(home, "other@my-project.iam.gserviceaccount.com"); err == nil || !strings.Contains(err.Error(), "does not exist") {
		t.Fatalf("LoadKey(other) error = %v", err)
	}

	inside := filepath.Join(project, "secrets", "key.json")
	if err := os.MkdirAll(filepath.Dir(inside), 0o755); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	if err := os.WriteFile(inside, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ImportKey(home, project, inside); err == nil || !strings.Contains(err.Error(), "inside the project") {
		t.Fatalf("ImportKey(inside project) error = %v", err)
	}

	notSA := filepath.Join(t.TempDir(), "user.json")
	if err := os.WriteFile(notSA, []byte(`{"type":"authorized_user","client_id":"x"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ImportKey(home, project, notSA); err == nil || !strings.Contains(err.Error(), "not a service account key") {
		t.Fatalf("ImportKey(authorized_user) error = %v", err)
	}
}

func TestVerifyPinned(t *testing.T) {
	home := t.TempDir()
	synced := ProjectConfig{ServiceAccount: testAccount, Scopes: []string{"documents.readonly", "spreadsheets.readonly"}}
	if _, err := VerifyPinned(home, "abc", synced); err == nil || !strings.Contains(err.Error(), "not pinned") {
		t.Fatalf("unpinned error = %v", err)
	}
	if err := pin.Save(home, "abc", PinKind, synced); err != nil {
		t.Fatal(err)
	}
	reordered := ProjectConfig{ServiceAccount: testAccount, Scopes: []string{"spreadsheets.readonly", "documents.readonly"}}
	if _, err := VerifyPinned(home, "abc", reordered); err != nil {
		t.Fatalf("VerifyPinned(reordered) error = %v", err)
	}
	widened := ProjectConfig{ServiceAccount: testAccount, Scopes: []string{"documents", "spreadsheets.readonly"}}
	if _, err := VerifyPinned(home, "abc", widened); err == nil || !strings.Contains(err.Error(), "differs from what is pinned") {
		t.Fatalf("widened error = %v", err)
	}
	swapped := ProjectConfig{ServiceAccount: "other@my-project.iam.gserviceaccount.com", Scopes: synced.Scopes}
	if _, err := VerifyPinned(home, "abc", swapped); err == nil {
		t.Fatal("swapped service account must be refused")
	}
}

// tokenEndpoint stands in for oauth2.googleapis.com/token: it verifies the
// JWT assertion against the public key and records what it was asked for.
type tokenEndpoint struct {
	public *rsa.PublicKey
	claims []map[string]any
	fail   bool
}

func (e *tokenEndpoint) handler(t *testing.T) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Fatal(err)
		}
		if r.Form.Get("grant_type") != "urn:ietf:params:oauth:grant-type:jwt-bearer" {
			t.Errorf("grant_type = %q", r.Form.Get("grant_type"))
		}
		parts := strings.Split(r.Form.Get("assertion"), ".")
		if len(parts) != 3 {
			t.Fatalf("assertion has %d parts", len(parts))
		}
		digest := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
		signature, err := base64.RawURLEncoding.DecodeString(parts[2])
		if err != nil {
			t.Fatal(err)
		}
		if err := rsa.VerifyPKCS1v15(e.public, crypto.SHA256, digest[:], signature); err != nil {
			t.Errorf("assertion signature: %v", err)
		}
		payload, err := base64.RawURLEncoding.DecodeString(parts[1])
		if err != nil {
			t.Fatal(err)
		}
		var claims map[string]any
		if err := json.Unmarshal(payload, &claims); err != nil {
			t.Fatal(err)
		}
		e.claims = append(e.claims, claims)
		if e.fail {
			w.WriteHeader(http.StatusBadRequest)
			w.Write([]byte(`{"error":"invalid_grant","error_description":"Invalid JWT Signature."}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"access_token":"ya29.test","expires_in":3600,"token_type":"Bearer"}`))
	})
}

func newTestIssuer(t *testing.T) (*Issuer, *tokenEndpoint, ProjectConfig) {
	endpoint := &tokenEndpoint{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { endpoint.handler(t).ServeHTTP(w, r) }))
	t.Cleanup(server.Close)
	path, private := fakeKey(t, t.TempDir(), server.URL+"/token")
	endpoint.public = &private.PublicKey
	data, _ := os.ReadFile(path)
	key, err := ParseKey(data)
	if err != nil {
		t.Fatal(err)
	}
	cfg := ProjectConfig{ServiceAccount: testAccount, Scopes: []string{"spreadsheets.readonly", "documents.readonly"}}
	issuer, err := NewIssuer(key, cfg)
	if err != nil {
		t.Fatal(err)
	}
	return issuer, endpoint, cfg
}

func TestIssuerMintsAndReuses(t *testing.T) {
	issuer, endpoint, _ := newTestIssuer(t)
	now := time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC)
	issuer.now = func() time.Time { return now }

	token, minted, err := issuer.Issue()
	if err != nil || !minted || token.AccessToken != "ya29.test" || token.ServiceAccount != testAccount {
		t.Fatalf("Issue() = %#v, %v, %v", token, minted, err)
	}
	if !token.ExpiresAt.Equal(now.Add(time.Hour)) {
		t.Fatalf("ExpiresAt = %v", token.ExpiresAt)
	}
	claims := endpoint.claims[0]
	if claims["iss"] != testAccount || claims["aud"] != issuer.key.TokenURI {
		t.Fatalf("claims = %v", claims)
	}
	if claims["scope"] != "https://www.googleapis.com/auth/documents.readonly https://www.googleapis.com/auth/spreadsheets.readonly" {
		t.Fatalf("scope claim = %v", claims["scope"])
	}
	if exp, iat := claims["exp"].(float64), claims["iat"].(float64); exp-iat != 3600 {
		t.Fatalf("exp-iat = %v", exp-iat)
	}

	now = now.Add(50 * time.Minute)
	if _, minted, err := issuer.Issue(); err != nil || minted {
		t.Fatalf("second Issue() minted = %v, err %v; want reuse", minted, err)
	}
	now = now.Add(6 * time.Minute)
	if _, minted, err := issuer.Issue(); err != nil || !minted {
		t.Fatalf("Issue() near expiry minted = %v, err %v; want a new token", minted, err)
	}
	if len(endpoint.claims) != 2 {
		t.Fatalf("token endpoint called %d times", len(endpoint.claims))
	}

	endpoint.fail = true
	now = now.Add(2 * time.Hour)
	if _, _, err := issuer.Issue(); err == nil || !strings.Contains(err.Error(), "invalid_grant") {
		t.Fatalf("failed grant error = %v", err)
	}
}

func TestHandler(t *testing.T) {
	issuer, _, _ := newTestIssuer(t)
	var mints []MintEvent
	server := httptest.NewServer(Handler(issuer, func(e MintEvent) error { mints = append(mints, e); return nil }))
	defer server.Close()

	resp, err := http.Get(server.URL + "/status")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || !strings.Contains(string(body), testAccount) || !strings.Contains(string(body), "spreadsheets.readonly") {
		t.Fatalf("status = %d %s", resp.StatusCode, body)
	}
	for i := 0; i < 2; i++ {
		resp, err := http.Get(server.URL + "/token")
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != 200 || !strings.Contains(string(body), `"access_token":"ya29.test"`) {
			t.Fatalf("token = %d %s", resp.StatusCode, body)
		}
	}
	if len(mints) != 1 || mints[0].ServiceAccount != testAccount || len(mints[0].Scopes) != 2 {
		t.Fatalf("mints = %+v; want one, the reuse unrecorded", mints)
	}
}

func TestHandlerWithholdsTokenWhenAuditFails(t *testing.T) {
	issuer, _, _ := newTestIssuer(t)
	server := httptest.NewServer(Handler(issuer, func(MintEvent) error { return errors.New("ssh is gone") }))
	defer server.Close()
	resp, err := http.Get(server.URL + "/token")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 500 || strings.Contains(string(body), "ya29") || !strings.Contains(string(body), "recording the audit event failed") {
		t.Fatalf("token with failed audit = %d %s", resp.StatusCode, body)
	}
}

func TestSetupCommand(t *testing.T) {
	command := SetupCommand(true)
	for _, want := range []string{"set -euo pipefail", "sudo tee " + ClientPath, "sudo chmod 755 " + ClientPath, "systemctl reload ssh", ClaudeSkillDir + "/SKILL.md"} {
		if !strings.Contains(command, want) {
			t.Fatalf("SetupCommand() missing %q:\n%s", want, command)
		}
	}
	if strings.Contains(SetupCommand(false), "SKILL.md") {
		t.Fatal("SetupCommand() should not install the skill without Claude")
	}
	if rendered := render(clientScript); !strings.Contains(rendered, "http://127.0.0.1:61001/google") || strings.Contains(rendered, "__ISOTTY_") {
		t.Fatalf("rendered client script not substituted:\n%s", rendered)
	}
}

// TestClientExec runs the VM client against the handler: `exec` must hand
// the token to the child in the environment and nothing else.
func TestClientExec(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not available")
	}
	issuer, _, _ := newTestIssuer(t)
	server := httptest.NewServer(http.StripPrefix("/google", Handler(issuer, func(MintEvent) error { return nil })))
	defer server.Close()
	port := strings.TrimPrefix(server.URL, "http://127.0.0.1:")
	script := filepath.Join(t.TempDir(), "isotty-google")
	if err := os.WriteFile(script, []byte(strings.ReplaceAll(clientScript, "__ISOTTY_GATEWAY_PORT__", port)), 0o755); err != nil {
		t.Fatal(err)
	}
	run := func(args ...string) (string, int) {
		cmd := exec.Command("python3", append([]string{"-I", script}, args...)...)
		cmd.Env = append(os.Environ(), "GOOGLE_APPLICATION_CREDENTIALS=/should/not/survive")
		out, err := cmd.CombinedOutput()
		var exitErr *exec.ExitError
		if err != nil && !errors.As(err, &exitErr) {
			t.Fatalf("%v: %v", args, err)
		}
		return string(out), cmd.ProcessState.ExitCode()
	}
	if out, code := run("status"); code != 0 || !strings.Contains(out, testAccount) {
		t.Fatalf("status: %d\n%s", code, out)
	}
	if out, code := run("token"); code != 0 || strings.TrimSpace(out) != "ya29.test" {
		t.Fatalf("token: %d\n%s", code, out)
	}
	out, code := run("exec", "--", "sh", "-c", "echo token=$GOOGLE_OAUTH_ACCESS_TOKEN adc=${GOOGLE_APPLICATION_CREDENTIALS:-unset}")
	if code != 0 || strings.TrimSpace(out) != "token=ya29.test adc=unset" {
		t.Fatalf("exec: %d\n%s", code, out)
	}
	if out, code := run("exec", "--", "sh", "-c", "exit 7"); code != 7 {
		t.Fatalf("exec exit code = %d\n%s", code, out)
	}
	if out, code := run("exec"); code == 0 || !strings.Contains(out, "needs a command") {
		t.Fatalf("exec without command: %d\n%s", code, out)
	}
}
