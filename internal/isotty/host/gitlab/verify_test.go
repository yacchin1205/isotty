package gitlab

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/yazawa/isotty/internal/isotty/host/forge"
)

func TestVerify(t *testing.T) {
	var token string
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token = r.Header.Get("PRIVATE-TOKEN")
		switch r.URL.EscapedPath() {
		case "/api/v4/projects/me%2Fmine":
			w.Write([]byte(`{"path_with_namespace":"Me/Mine","permissions":{"project_access":{"access_level":40},"group_access":null}}`))
		case "/api/v4/projects/group%2Fsub%2Ftheirs":
			w.Write([]byte(`{"path_with_namespace":"group/sub/theirs","permissions":{"project_access":null,"group_access":{"access_level":20}}}`))
		case "/api/v4/projects/group%2Fdev":
			w.Write([]byte(`{"path_with_namespace":"group/dev","permissions":{"project_access":{"access_level":30},"group_access":null}}`))
		default:
			w.WriteHeader(http.StatusNotFound)
			w.Write([]byte(`{"message":"404 Project Not Found"}`))
		}
	}))
	defer api.Close()
	host := mustHost(t, api.URL)
	cfg := HostConfig{Instances: []Instance{{URL: api.URL, Token: "glpat-x"}}}

	if name, err := Verify(cfg, host+"/me/mine", forge.AccessWrite); err != nil || name != host+"/Me/Mine" {
		t.Fatalf("Verify(write) = %q, %v; want GitLab's spelling", name, err)
	}
	if token != "glpat-x" {
		t.Fatalf("PRIVATE-TOKEN = %q", token)
	}
	if name, err := Verify(cfg, host+"/group/sub/theirs", forge.AccessComment); err != nil || name != host+"/group/sub/theirs" {
		t.Fatalf("Verify(comment) = %q, %v", name, err)
	}
	if _, err := Verify(cfg, host+"/group/sub/theirs", forge.AccessWrite); err == nil || !strings.Contains(err.Error(), "no Developer role") {
		t.Fatalf("Verify(write as Reporter) error = %v", err)
	}
	if name, err := Verify(cfg, host+"/me/mine", forge.AccessAdmin); err != nil || name != host+"/Me/Mine" {
		t.Fatalf("Verify(admin as Maintainer) = %q, %v", name, err)
	}
	if _, err := Verify(cfg, host+"/group/sub/theirs", forge.AccessAdmin); err == nil || !strings.Contains(err.Error(), "no Developer role") {
		t.Fatalf("Verify(admin as Reporter) error = %v", err)
	}
	if _, err := Verify(cfg, host+"/group/dev", forge.AccessAdmin); err == nil || !strings.Contains(err.Error(), "no Maintainer role") {
		t.Fatalf("Verify(admin as Developer) error = %v", err)
	}
	if _, err := Verify(cfg, host+"/nobody/missing", forge.AccessRead); err == nil || !strings.Contains(err.Error(), "not visible to the token") {
		t.Fatalf("Verify(missing) error = %v", err)
	}
	if _, err := Verify(cfg, "other.example.org/g/p", forge.AccessRead); err == nil || !strings.Contains(err.Error(), "no instance for other.example.org") {
		t.Fatalf("Verify(unknown host) error = %v", err)
	}
}

func mustHost(t *testing.T, raw string) string {
	t.Helper()
	parsed, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return parsed.Hostname()
}

func TestWhoAmIAndHostConfigPut(t *testing.T) {
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v4/user" || r.Header.Get("PRIVATE-TOKEN") != "glpat-x" {
			w.WriteHeader(http.StatusUnauthorized)
			w.Write([]byte(`{"message":"401 Unauthorized"}`))
			return
		}
		w.Write([]byte(`{"username":"yazawa"}`))
	}))
	defer api.Close()
	if name, err := WhoAmI(Instance{URL: api.URL + "/", Token: "glpat-x"}); err != nil || name != "yazawa" {
		t.Fatalf("WhoAmI() = %q, %v", name, err)
	}
	if _, err := WhoAmI(Instance{URL: api.URL, Token: "bad"}); err == nil || !strings.Contains(err.Error(), "answered 401") {
		t.Fatalf("WhoAmI(bad) error = %v", err)
	}

	home := t.TempDir()
	cfg, err := LoadOrEmptyHostConfig(home, "abc")
	if err != nil || len(cfg.Instances) != 0 {
		t.Fatalf("LoadOrEmptyHostConfig() = %#v, %v", cfg, err)
	}
	for _, instance := range []Instance{{URL: "https://a.example.org", Token: "1"}, {URL: "https://b.example.org", Token: "2"}, {URL: "https://A.example.org/", Token: "3"}} {
		if err := cfg.Put(instance); err != nil {
			t.Fatal(err)
		}
	}
	if err := cfg.Put(Instance{URL: "not a url"}); err == nil {
		t.Fatal("Put(not a url) should fail")
	}
	if err := SaveHostConfig(cfg); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadHostConfig(home, "abc")
	if err != nil || len(loaded.Instances) != 2 {
		t.Fatalf("LoadHostConfig() = %#v, %v", loaded, err)
	}
	if got, _ := loaded.Find("a.example.org"); got.Token != "3" {
		t.Fatalf("Put() did not replace: %#v", got)
	}
	if info, _ := os.Stat(HostConfigPath(home, "abc")); info.Mode().Perm() != 0o600 {
		t.Fatalf("host config mode = %o", info.Mode().Perm())
	}
}
