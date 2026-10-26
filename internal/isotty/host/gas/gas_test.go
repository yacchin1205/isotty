package gas

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yazawa/isotty/internal/isotty/host/pin"
)

const (
	testClientID = "123-abc.apps.googleusercontent.com"
	testAccount  = "human@example.com"
	testScript   = "1AbCdEf_GhIjKlMnOpQrStUvWxYz"
	otherScript  = "1OtherScript"
)

var testScopes = []string{"https://www.googleapis.com/auth/spreadsheets", "https://www.googleapis.com/auth/script.send_mail"}

func testConfig() ProjectConfig {
	return ProjectConfig{Scripts: []Script{
		{ID: testScript, Client: testClientID, Account: testAccount, Scopes: testScopes},
		{ID: otherScript, Client: testClientID, Account: testAccount, Scopes: testScopes[:1]},
	}}
}

// fakeGoogle stands in for the token endpoint and the Apps Script API.
type fakeGoogle struct {
	server    *httptest.Server
	forms     []url.Values
	runs      []map[string]any
	auths     []string
	answer    func(call map[string]any) (int, string)
	failGrant bool
}

func newFakeGoogle(t *testing.T) *fakeGoogle {
	t.Helper()
	g := &fakeGoogle{answer: func(map[string]any) (int, string) {
		return 200, `{"done":true,"response":{"@type":"type.googleapis.com/google.apps.script.v1.ExecutionResponse","result":{"sent":3}}}`
	}}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /token", func(w http.ResponseWriter, req *http.Request) {
		if err := req.ParseForm(); err != nil {
			t.Fatal(err)
		}
		g.forms = append(g.forms, req.PostForm)
		if g.failGrant {
			http.Error(w, `{"error":"invalid_grant"}`, 400)
			return
		}
		claims, _ := json.Marshal(map[string]string{"email": testAccount})
		idToken := "eyJhbGciOiJSUzI1NiJ9." + base64.RawURLEncoding.EncodeToString(claims) + ".sig"
		json.NewEncoder(w).Encode(map[string]any{
			"access_token": "ya29.test", "expires_in": 3600, "refresh_token": "1//refresh", "id_token": idToken,
			"scope": strings.Join(append([]string{"openid", "https://www.googleapis.com/auth/userinfo.email"}, testScopes...), " "),
		})
	})
	mux.HandleFunc("POST /v1/scripts/", func(w http.ResponseWriter, req *http.Request) {
		g.auths = append(g.auths, req.Header.Get("Authorization"))
		var call map[string]any
		if err := json.NewDecoder(req.Body).Decode(&call); err != nil {
			t.Fatal(err)
		}
		call["script"] = strings.TrimSuffix(strings.TrimPrefix(req.URL.Path, "/v1/scripts/"), ":run")
		g.runs = append(g.runs, call)
		status, body := g.answer(call)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		io.WriteString(w, body)
	})
	g.server = httptest.NewServer(mux)
	t.Cleanup(g.server.Close)
	return g
}

func (g *fakeGoogle) client() Client {
	return Client{ClientID: testClientID, ClientSecret: "secret", AuthURI: g.server.URL + "/auth", TokenURI: g.server.URL + "/token"}
}

func (g *fakeGoogle) credential() Credential {
	return Credential{Client: g.client(), Account: testAccount, RefreshToken: "1//refresh", Scopes: append([]string{"email", "openid"}, testScopes...)}
}

func (g *fakeGoogle) credentials() map[string]Credential {
	return map[string]Credential{testClientID: g.credential()}
}

func TestParseProjectConfig(t *testing.T) {
	entry := "  - id: " + testScript + "\n    client: " + testClientID + "\n    account: " + testAccount + "\n    scopes: [https://www.googleapis.com/auth/spreadsheets]\n"
	cfg, err := ParseProjectConfig([]byte("scripts:\n" + entry))
	if err != nil {
		t.Fatalf("ParseProjectConfig() error = %v", err)
	}
	script, ok := cfg.Find(testScript)
	if !ok || script.Client != testClientID {
		t.Fatalf("Find() = %+v, %v", script, ok)
	}
	if _, ok := cfg.Find("other"); ok {
		t.Fatal("Find(other) should fail")
	}
	if got := strings.Join(LoginScopes(cfg.ClientScopes(testClientID)), " "); got != "email https://www.googleapis.com/auth/spreadsheets openid" {
		t.Fatalf("LoginScopes() = %q", got)
	}
	for _, bad := range []string{
		"scripts: []\n",
		"scripts:\n  - id: " + testScript + "\n    client: c\n    account: " + testAccount + "\n",
		"scripts:\n  - id: 'a/b'\n    client: c\n    account: " + testAccount + "\n    scopes: [https://x/y]\n",
		"scripts:\n  - id: " + testScript + "\n    client: c\n    account: nobody\n    scopes: [https://x/y]\n",
		"scripts:\n  - id: " + testScript + "\n    client: c\n    account: " + testAccount + "\n    scopes: [spreadsheets]\n",
		"scripts:\n  - id: " + testScript + "\n    account: " + testAccount + "\n    scopes: [https://x/y]\n",
		"scripts:\n" + entry + "    functions: [f]\n",
		"scripts:\n" + entry + "    dev_mode: true\n",
		"scripts:\n" + entry + entry,
	} {
		if _, err := ParseProjectConfig([]byte(bad)); err == nil {
			t.Fatalf("ParseProjectConfig(%q) should fail", bad)
		}
	}

	cfg = testConfig()
	cfg.Put(Script{ID: testScript, Client: "other-client", Account: testAccount, Scopes: []string{"https://mail.google.com/"}})
	if len(cfg.Scripts) != 2 || cfg.Scripts[0].Client != "other-client" {
		t.Fatalf("Put(existing) = %+v", cfg.Scripts)
	}
	if got := cfg.ClientScopes(testClientID); len(got) != 1 || got[0] != testScopes[0] {
		t.Fatalf("ClientScopes() = %v", got)
	}
	if !cfg.Delete(otherScript) || cfg.Delete(otherScript) || len(cfg.Scripts) != 1 {
		t.Fatalf("Delete() left %+v", cfg.Scripts)
	}
}

func TestReadClient(t *testing.T) {
	project := t.TempDir()
	path := filepath.Join(t.TempDir(), "client_secret.json")
	file := `{"installed":{"client_id":"` + testClientID + `","client_secret":"s","auth_uri":"https://accounts.google.com/o/oauth2/auth","token_uri":"https://oauth2.googleapis.com/token","redirect_uris":["http://localhost"]}}`
	if err := os.WriteFile(path, []byte(file), 0o600); err != nil {
		t.Fatal(err)
	}
	client, err := ReadClient(project, path)
	if err != nil || client.ClientID != testClientID || client.TokenURI != "https://oauth2.googleapis.com/token" {
		t.Fatalf("ReadClient() = %+v, %v", client, err)
	}

	inside := filepath.Join(project, "client_secret.json")
	if err := os.WriteFile(inside, []byte(file), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadClient(project, inside); err == nil || !strings.Contains(err.Error(), "inside the project") {
		t.Fatalf("ReadClient(inside project) error = %v", err)
	}
	if _, err := ParseClient([]byte(`{"web":{"client_id":"x","client_secret":"s","auth_uri":"a","token_uri":"t"}}`)); err == nil || !strings.Contains(err.Error(), "desktop application") {
		t.Fatalf("ParseClient(web) error = %v", err)
	}
	if _, err := ParseClient([]byte(`{"type":"service_account"}`)); err == nil || !strings.Contains(err.Error(), "not an OAuth client file") {
		t.Fatalf("ParseClient(service account) error = %v", err)
	}
}

func TestCredentialFiles(t *testing.T) {
	home := t.TempDir()
	cred := newFakeGoogle(t).credential()
	if err := SaveCredential(home, cred); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(CredentialPath(home, testClientID))
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("credential stat = %v, %v", info, err)
	}
	creds, err := LoadCredentials(home, testConfig())
	if err != nil || len(creds) != 1 || creds[testClientID].RefreshToken != "1//refresh" || creds[testClientID].TokenURI != cred.TokenURI {
		t.Fatalf("LoadCredentials() = %+v, %v", creds, err)
	}

	other := testConfig()
	other.Scripts[1].Account = "someone@example.com"
	if _, err := LoadCredentials(home, other); err == nil || !strings.Contains(err.Error(), "authorized by "+testAccount) {
		t.Fatalf("LoadCredentials(other account) error = %v", err)
	}
	wider := testConfig()
	wider.Scripts[0].Scopes = append(wider.Scripts[0].Scopes, "https://mail.google.com/")
	if _, err := LoadCredentials(home, wider); err == nil || !strings.Contains(err.Error(), "not granted every scope") {
		t.Fatalf("LoadCredentials(wider scopes) error = %v", err)
	}
	unknown := testConfig()
	unknown.Scripts[0].Client = "other.apps.googleusercontent.com"
	if _, err := LoadCredentials(home, unknown); err == nil || !strings.Contains(err.Error(), "does not exist") {
		t.Fatalf("LoadCredentials(unknown client) error = %v", err)
	}
}

func TestVerifyPinned(t *testing.T) {
	home := t.TempDir()
	synced := testConfig()
	if _, err := VerifyPinned(home, "abc", synced); err == nil || !strings.Contains(err.Error(), "not pinned") {
		t.Fatalf("VerifyPinned(no pin) error = %v", err)
	}
	if err := pin.Save(home, "abc", PinKind, synced); err != nil {
		t.Fatal(err)
	}
	reordered := testConfig()
	reordered.Scripts[0], reordered.Scripts[1] = reordered.Scripts[1], reordered.Scripts[0]
	reordered.Scripts[1].Scopes = []string{testScopes[1], testScopes[0]}
	if _, err := VerifyPinned(home, "abc", reordered); err != nil {
		t.Fatalf("VerifyPinned(same set) error = %v", err)
	}
	for name, change := range map[string]func(*ProjectConfig){
		"scope": func(c *ProjectConfig) { c.Scripts[0].Scopes = append(c.Scripts[0].Scopes, "https://mail.google.com/") },
		"script": func(c *ProjectConfig) {
			c.Put(Script{ID: "1Another", Client: testClientID, Account: testAccount, Scopes: testScopes})
		},
		"removed": func(c *ProjectConfig) { c.Delete(otherScript) },
		"client":  func(c *ProjectConfig) { c.Scripts[0].Client = "other" },
		"account": func(c *ProjectConfig) { c.Scripts[0].Account = "someone@example.com" },
	} {
		changed := testConfig()
		change(&changed)
		if _, err := VerifyPinned(home, "abc", changed); err == nil || !strings.Contains(err.Error(), "differs from what is pinned") {
			t.Fatalf("VerifyPinned(changed %s) error = %v", name, err)
		}
	}
}

// TestLogin drives the loopback flow: the "browser" follows the
// authorization URL straight to the redirect with a code, and the code is
// exchanged with the PKCE verifier that matches the challenge.
func TestLogin(t *testing.T) {
	g := newFakeGoogle(t)
	var authURL *url.URL
	browser := func(raw string) error {
		var err error
		authURL, err = url.Parse(raw)
		if err != nil {
			return err
		}
		q := authURL.Query()
		resp, err := http.Get(q.Get("redirect_uri") + "?code=4/code&state=" + q.Get("state"))
		if err != nil {
			return err
		}
		resp.Body.Close()
		if resp.StatusCode != 200 {
			t.Fatalf("redirect answered %d", resp.StatusCode)
		}
		return nil
	}
	cred, err := Login(context.Background(), g.client(), LoginScopes(testScopes), browser)
	if err != nil {
		t.Fatalf("Login() error = %v", err)
	}
	if cred.Account != testAccount || cred.RefreshToken != "1//refresh" || cred.ClientSecret != "secret" {
		t.Fatalf("credential = %+v", cred)
	}
	if !cred.Covers(testScopes) {
		t.Fatalf("granted scopes %v should cover %v", cred.Scopes, testScopes)
	}
	q := authURL.Query()
	if q.Get("access_type") != "offline" || q.Get("prompt") != "consent" || q.Get("code_challenge_method") != "S256" || !strings.Contains(q.Get("scope"), "openid") {
		t.Fatalf("authorization query = %v", q)
	}
	form := g.forms[0]
	sum := sha256.Sum256([]byte(form.Get("code_verifier")))
	if form.Get("grant_type") != "authorization_code" || form.Get("code") != "4/code" || base64.RawURLEncoding.EncodeToString(sum[:]) != q.Get("code_challenge") {
		t.Fatalf("token form = %v", form)
	}

	refused := func(raw string) error {
		q := mustParse(t, raw).Query()
		resp, err := http.Get(q.Get("redirect_uri") + "?error=access_denied&state=" + q.Get("state"))
		if err != nil {
			return err
		}
		resp.Body.Close()
		return nil
	}
	if _, err := Login(context.Background(), g.client(), testScopes, refused); err == nil || !strings.Contains(err.Error(), "access_denied") {
		t.Fatalf("Login(refused) error = %v", err)
	}
	forged := func(raw string) error {
		q := mustParse(t, raw).Query()
		resp, err := http.Get(q.Get("redirect_uri") + "?code=4/code&state=wrong")
		if err != nil {
			return err
		}
		resp.Body.Close()
		return nil
	}
	if _, err := Login(context.Background(), g.client(), testScopes, forged); err == nil || !strings.Contains(err.Error(), "unexpected state") {
		t.Fatalf("Login(forged state) error = %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := Login(ctx, g.client(), testScopes, func(string) error { return nil }); err == nil || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Login(timeout) error = %v", err)
	}
}

func mustParse(t *testing.T, raw string) *url.URL {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return u
}

func TestRefresherReuses(t *testing.T) {
	g := newFakeGoogle(t)
	refresher := NewRefresher(g.credential())
	now := time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC)
	refresher.now = func() time.Time { return now }
	if token, err := refresher.Access(); err != nil || token != "ya29.test" {
		t.Fatalf("Access() = %q, %v", token, err)
	}
	if form := g.forms[0]; form.Get("grant_type") != "refresh_token" || form.Get("refresh_token") != "1//refresh" || form.Get("client_secret") != "secret" {
		t.Fatalf("refresh form = %v", form)
	}
	now = now.Add(50 * time.Minute)
	if _, err := refresher.Access(); err != nil || len(g.forms) != 1 {
		t.Fatalf("Access() at 50 min refreshed (%d calls), err %v; want reuse", len(g.forms), err)
	}
	now = now.Add(4 * time.Minute)
	if _, err := refresher.Access(); err != nil || len(g.forms) != 2 {
		t.Fatalf("Access() with 6 min left refreshed %d times, err %v; want a new token", len(g.forms), err)
	}
	g.failGrant = true
	now = now.Add(2 * time.Hour)
	if _, err := refresher.Access(); err == nil || !strings.Contains(err.Error(), "invalid_grant") {
		t.Fatalf("Access() after revocation error = %v", err)
	}
}

func newTestHandler(t *testing.T, g *fakeGoogle, onRun func(RunEvent) error) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.StripPrefix("/gas", Handler(newRunner(testConfig(), g.credentials(), g.server.URL), onRun)))
	t.Cleanup(server.Close)
	return server
}

func post(t *testing.T, server *httptest.Server, body string) (int, string) {
	t.Helper()
	resp, err := http.Post(server.URL+"/gas/run", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(out)
}

func TestHandler(t *testing.T) {
	g := newFakeGoogle(t)
	var events []RunEvent
	server := newTestHandler(t, g, func(e RunEvent) error { events = append(events, e); return nil })

	resp, err := http.Get(server.URL + "/gas/status")
	if err != nil {
		t.Fatal(err)
	}
	status, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || !strings.Contains(string(status), `"id":"`+testScript+`"`) || !strings.Contains(string(status), `"account":"`+testAccount+`"`) {
		t.Fatalf("status = %d %s", resp.StatusCode, status)
	}

	code, body := post(t, server, `{"script":"`+testScript+`","function":"sendRequests","parameters":[ "sheet-1" , 3 ]}`)
	if code != 200 || !strings.Contains(body, `"result":{"sent":3}`) {
		t.Fatalf("run = %d %s", code, body)
	}
	if g.auths[0] != "Bearer ya29.test" || g.runs[0]["script"] != testScript || g.runs[0]["function"] != "sendRequests" || g.runs[0]["devMode"] != true {
		t.Fatalf("upstream call = %v %v", g.auths, g.runs)
	}
	if len(events) != 1 || events[0] != (RunEvent{Account: testAccount, Script: testScript, Function: "sendRequests", Parameters: `["sheet-1",3]`, Result: "done"}) {
		t.Fatalf("events = %+v", events)
	}

	code, body = post(t, server, `{"script":"`+otherScript+`","function":"collectAnswers"}`)
	if code != 200 || g.runs[1]["script"] != otherScript || g.runs[1]["parameters"] == nil || events[1].Parameters != "[]" {
		t.Fatalf("run of the other script = %d %s, upstream %v, event %+v", code, body, g.runs[1], events[1])
	}

	g.answer = func(map[string]any) (int, string) {
		return 200, `{"done":true,"error":{"code":3,"message":"ScriptError","details":[{"@type":"type.googleapis.com/google.apps.script.v1.ExecutionError","errorMessage":"Sheet not found","errorType":"Error","scriptStackTraceElements":[{"function":"sendRequests","lineNumber":12}]}]}}`
	}
	code, body = post(t, server, `{"script":"`+testScript+`","function":"sendRequests","parameters":["gone"]}`)
	if code != 200 || !strings.Contains(body, "Sheet not found") || events[2].Result != "error: Sheet not found" {
		t.Fatalf("script error = %d %s, event %+v", code, body, events[2])
	}

	g.answer = func(map[string]any) (int, string) {
		return 403, `{"error":{"code":403,"message":"Apps Script API has not been used in project 123 before or it is disabled.","status":"PERMISSION_DENIED"}}`
	}
	code, body = post(t, server, `{"script":"`+testScript+`","function":"sendRequests"}`)
	if code != 403 || !strings.Contains(body, "has not been used") || !strings.HasPrefix(events[3].Result, "http 403: Apps Script API") {
		t.Fatalf("api refusal = %d %s, event %+v", code, body, events[3])
	}

	calls := len(g.runs)
	for _, bad := range []struct{ body, want string }{
		{`{"script":"1Unknown","function":"sendRequests"}`, "not pinned"},
		{`{"script":"` + testScript + `"}`, "names no function"},
		{`{"script":"` + testScript + `","function":"sendRequests","parameters":{"a":1}}`, "must be a JSON array"},
		{`{"script":"` + testScript + `","function":"sendRequests","devMode":true}`, "unknown field"},
		{`not json`, "must be a JSON object"},
	} {
		code, body := post(t, server, bad.body)
		if code/100 != 4 || !strings.Contains(body, bad.want) {
			t.Fatalf("%s = %d %s; want %q", bad.body, code, body, bad.want)
		}
	}
	if len(g.runs) != calls || len(events) != 4 {
		t.Fatalf("refused requests reached upstream (%d calls) or were recorded (%d events)", len(g.runs)-calls, len(events))
	}
}

func TestHandlerRecordsBeforeAnswering(t *testing.T) {
	g := newFakeGoogle(t)
	server := newTestHandler(t, g, func(RunEvent) error { return errors.New("ssh is gone") })
	code, body := post(t, server, `{"script":"`+testScript+`","function":"sendRequests"}`)
	if code != 500 || !strings.Contains(body, "sendRequests was run (done) but recording the audit event failed") || !strings.Contains(body, `"sent":3`) {
		t.Fatalf("run with failed audit = %d %s", code, body)
	}

	var events []RunEvent
	gone := newFakeGoogle(t)
	gone.answer = func(map[string]any) (int, string) {
		time.Sleep(200 * time.Millisecond)
		return 200, "{}"
	}
	runner := newRunner(testConfig(), gone.credentials(), gone.server.URL)
	// Reach the token first so only the run can time out.
	if _, err := runner.tokens[testClientID].Access(); err != nil {
		t.Fatal(err)
	}
	runner.client.Timeout = 50 * time.Millisecond
	server = httptest.NewServer(http.StripPrefix("/gas", Handler(runner, func(e RunEvent) error { events = append(events, e); return nil })))
	defer server.Close()
	code, body = post(t, server, `{"script":"`+testScript+`","function":"sendRequests"}`)
	if code != 502 || !strings.Contains(body, "Apps Script API request failed") {
		t.Fatalf("run without answer = %d %s", code, body)
	}
	if len(events) != 1 || !strings.HasPrefix(events[0].Result, "no response: ") {
		t.Fatalf("events = %+v; want the run recorded as unanswered", events)
	}
}

func TestVerify(t *testing.T) {
	g := newFakeGoogle(t)
	runner := newRunner(testConfig(), g.credentials(), g.server.URL)
	script, _ := testConfig().Find(testScript)

	g.answer = func(call map[string]any) (int, string) {
		if !strings.HasPrefix(call["function"].(string), "isottyProbe") {
			t.Fatalf("probe ran %q", call["function"])
		}
		return 200, `{"done":true,"error":{"code":3,"message":"ScriptError","details":[{"errorMessage":"Script function not found: ` + call["function"].(string) + `","errorType":"FUNCTION_NOT_FOUND"}]}}`
	}
	if err := runner.Verify(script); err != nil {
		t.Fatalf("Verify(reachable) error = %v", err)
	}
	if g.runs[0]["script"] != testScript || g.runs[0]["devMode"] != true {
		t.Fatalf("probe call = %v", g.runs[0])
	}

	g.answer = func(map[string]any) (int, string) {
		return 200, `{"done":true,"error":{"code":3,"message":"ScriptError","details":[{"errorMessage":"We're sorry, a server error occurred while reading from storage. Error code NOT_FOUND.","errorType":"USER_ERROR"}]}}`
	}
	if err := runner.Verify(script); err == nil || !strings.Contains(err.Error(), "API executable") || !strings.Contains(err.Error(), testScript) {
		t.Fatalf("Verify(never deployed) error = %v", err)
	}

	g.answer = func(map[string]any) (int, string) {
		return 403, `{"error":{"code":403,"message":"The caller does not have permission","status":"PERMISSION_DENIED"}}`
	}
	if err := runner.Verify(script); err == nil || !strings.Contains(err.Error(), "403") || !strings.Contains(err.Error(), "Cloud project") {
		t.Fatalf("Verify(403) error = %v", err)
	}

	g.answer = func(map[string]any) (int, string) {
		return 200, `{"done":true,"error":{"code":3,"message":"ScriptError","details":[{"errorMessage":"Authorization is required to perform that action.","errorType":"USER_ERROR"}]}}`
	}
	if err := runner.Verify(script); err == nil || !strings.Contains(err.Error(), "Authorization is required") {
		t.Fatalf("Verify(other script error) error = %v", err)
	}
}

func TestSetupCommand(t *testing.T) {
	command := SetupCommand([]string{"claude", "codex"})
	for _, want := range []string{"set -euo pipefail", "sudo tee " + ClientPath, "sudo chmod 755 " + ClientPath, "systemctl reload ssh", ".claude/skills/" + SkillName + "/SKILL.md", ".codex/skills/" + SkillName + "/SKILL.md"} {
		if !strings.Contains(command, want) {
			t.Fatalf("SetupCommand() missing %q:\n%s", want, command)
		}
	}
	if strings.Contains(SetupCommand(nil), "SKILL.md") {
		t.Fatal("SetupCommand() should not install the skill without an agent")
	}
	if rendered := render(clientScript); !strings.Contains(rendered, "http://127.0.0.1:61001/gas") || strings.Contains(rendered, "__ISOTTY_") {
		t.Fatalf("rendered client script not substituted:\n%s", rendered)
	}
}

// TestClient runs the VM client against the handler.
func TestClient(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not available")
	}
	g := newFakeGoogle(t)
	server := newTestHandler(t, g, func(RunEvent) error { return nil })
	port := strings.TrimPrefix(server.URL, "http://127.0.0.1:")
	script := filepath.Join(t.TempDir(), "isotty-gas")
	if err := os.WriteFile(script, []byte(strings.ReplaceAll(clientScript, "__ISOTTY_GATEWAY_PORT__", port)), 0o755); err != nil {
		t.Fatal(err)
	}
	run := func(args ...string) (string, int) {
		cmd := exec.Command("python3", append([]string{"-I", script}, args...)...)
		out, err := cmd.CombinedOutput()
		var exitErr *exec.ExitError
		if err != nil && !errors.As(err, &exitErr) {
			t.Fatalf("%v: %v", args, err)
		}
		return string(out), cmd.ProcessState.ExitCode()
	}
	if out, code := run("status"); code != 0 || !strings.Contains(out, testScript+" as "+testAccount) || !strings.Contains(out, otherScript+" as "+testAccount) {
		t.Fatalf("status: %d\n%s", code, out)
	}
	if out, code := run("run", testScript, "sendRequests", `["sheet-1", 3]`); code != 0 || strings.TrimSpace(out) != "{\n  \"sent\": 3\n}" {
		t.Fatalf("run: %d\n%s", code, out)
	}
	if params := g.runs[0]["parameters"].([]any); len(params) != 2 || params[0] != "sheet-1" {
		t.Fatalf("upstream parameters = %v", params)
	}
	if out, code := run("run", testScript, "sendRequests", `{"a":1}`); code == 0 || !strings.Contains(out, "JSON array") {
		t.Fatalf("run with object: %d\n%s", code, out)
	}
	if out, code := run("run", "1Unknown", "sendRequests"); code == 0 || !strings.Contains(out, "not pinned") {
		t.Fatalf("run refused: %d\n%s", code, out)
	}

	g.answer = func(map[string]any) (int, string) { return 200, `{"done":true,"response":{}}` }
	if out, code := run("run", testScript, "collectAnswers"); code != 0 || out != "" {
		t.Fatalf("run without result: %d\n%q", code, out)
	}
	g.answer = func(map[string]any) (int, string) {
		return 200, `{"done":true,"error":{"code":3,"message":"ScriptError","details":[{"errorMessage":"Sheet not found","errorType":"Error","scriptStackTraceElements":[{"function":"sendRequests","lineNumber":12}]}]}}`
	}
	if out, code := run("run", testScript, "sendRequests"); code != 1 || !strings.Contains(out, "Sheet not found") || !strings.Contains(out, "at sendRequests (line 12)") {
		t.Fatalf("script error: %d\n%s", code, out)
	}
	g.answer = func(map[string]any) (int, string) {
		return 404, `{"error":{"code":404,"message":"Requested entity was not found.","status":"NOT_FOUND"}}`
	}
	if out, code := run("run", testScript, "sendRequests"); code != 1 || !strings.Contains(out, "answered 404: Requested entity was not found.") {
		t.Fatalf("api error: %d\n%s", code, out)
	}
}
