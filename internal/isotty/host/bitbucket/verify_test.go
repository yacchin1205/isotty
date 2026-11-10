package bitbucket

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestVerify(t *testing.T) {
	var auth, query string
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/repositories/my-team/app":
			w.Write([]byte(`{"full_name":"my-team/app","is_private":true}`))
		case "/repositories/other-team/lib":
			w.Write([]byte(`{"full_name":"Other-Team/lib","is_private":false}`))
		case "/repositories/other-team/tool":
			w.Write([]byte(`{"full_name":"other-team/tool","is_private":true}`))
		case "/user/workspaces/my-team/permissions/repositories":
			query = r.URL.Query().Get("q")
			w.Write([]byte(`{"values":[{"permission":"admin","repository":{"full_name":"my-team/app"}}]}`))
		case "/user/workspaces/Other-Team/permissions/repositories":
			query = r.URL.Query().Get("q")
			w.Write([]byte(`{"values":[]}`))
		case "/user/workspaces/other-team/permissions/repositories":
			query = r.URL.Query().Get("q")
			w.Write([]byte(`{"values":[{"permission":"write","repository":{"full_name":"other-team/tool"}}]}`))
		default:
			w.WriteHeader(http.StatusNotFound)
			w.Write([]byte(`{"type":"error","error":{"message":"Repository not found"}}`))
		}
	}))
	defer api.Close()

	if name, err := verify("tok", "my-team/app", AccessWrite, api.URL); err != nil || name != "my-team/app" {
		t.Fatalf("verify(write) = %q, %v", name, err)
	}
	if auth != "Bearer tok" {
		t.Fatalf("Authorization = %q", auth)
	}
	if query != `repository.full_name="my-team/app"` {
		t.Fatalf("permission query = %q", query)
	}
	if name, err := verify("tok", "other-team/lib", AccessRead, api.URL); err != nil || name != "Other-Team/lib" {
		t.Fatalf("verify(read) = %q, %v; want Bitbucket's spelling", name, err)
	}
	if name, err := verify("tok", "other-team/lib", AccessComment, api.URL); err != nil || name != "Other-Team/lib" {
		t.Fatalf("verify(comment) = %q, %v", name, err)
	}
	if _, err := verify("tok", "other-team/lib", AccessWrite, api.URL); err == nil || !strings.Contains(err.Error(), "has none permission") || !strings.Contains(err.Error(), "cannot push") {
		t.Fatalf("verify(write without permission) error = %v", err)
	}
	if name, err := verify("tok", "my-team/app", AccessAdmin, api.URL); err != nil || name != "my-team/app" {
		t.Fatalf("verify(admin) = %q, %v", name, err)
	}
	if name, err := verify("tok", "other-team/tool", AccessWrite, api.URL); err != nil || name != "other-team/tool" {
		t.Fatalf("verify(write with write) = %q, %v", name, err)
	}
	if _, err := verify("tok", "other-team/tool", AccessAdmin, api.URL); err == nil || !strings.Contains(err.Error(), "has write permission") || !strings.Contains(err.Error(), "cannot change its settings") {
		t.Fatalf("verify(admin with write) error = %v", err)
	}
	if _, err := verify("tok", "nobody/missing", AccessRead, api.URL); err == nil || !strings.Contains(err.Error(), "not visible to the token") {
		t.Fatalf("verify(missing) error = %v", err)
	}
}

func TestVerifyReportsARejectedToken(t *testing.T) {
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"type":"error","error":{"message":"Token is invalid or not supported for this endpoint."}}`))
	}))
	defer api.Close()
	if _, err := verify("tok", "my-team/app", AccessRead, api.URL); err == nil || !strings.Contains(err.Error(), "rejected the token") || !strings.Contains(err.Error(), "Token is invalid") {
		t.Fatalf("error = %v", err)
	}
}

func TestWhoAmI(t *testing.T) {
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/user" || r.Header.Get("Authorization") != "Bearer ATATT3x" {
			w.WriteHeader(http.StatusUnauthorized)
			w.Write([]byte(`{"type":"error","error":{"message":"Access token expired."}}`))
			return
		}
		w.Write([]byte(`{"display_name":"Some One","nickname":"someone","uuid":"{u}"}`))
	}))
	defer api.Close()
	if name, err := whoAmI("ATATT3x", api.URL); err != nil || name != "someone" {
		t.Fatalf("whoAmI() = %q, %v", name, err)
	}
	if _, err := whoAmI("old", api.URL); err == nil || !strings.Contains(err.Error(), "answered 401") || !strings.Contains(err.Error(), "expired") {
		t.Fatalf("whoAmI(old) error = %v", err)
	}
}
