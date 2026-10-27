package github

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestVerify(t *testing.T) {
	var auth string
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")
		switch r.URL.Path {
		case "/repos/yacchin1205/isotty":
			w.Write([]byte(`{"full_name":"yacchin1205/isotty","permissions":{"push":true,"admin":true}}`))
		case "/repos/some-org/lib":
			w.Write([]byte(`{"full_name":"Some-Org/lib","permissions":{"push":false,"admin":false}}`))
		case "/repos/some-org/app":
			w.Write([]byte(`{"full_name":"some-org/app","permissions":{"push":true,"admin":false}}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer api.Close()

	if name, err := verify("tok", "yacchin1205/isotty", AccessWrite, api.URL); err != nil || name != "yacchin1205/isotty" {
		t.Fatalf("verify(write) = %q, %v", name, err)
	}
	if auth != "Bearer tok" {
		t.Fatalf("Authorization = %q", auth)
	}
	if name, err := verify("tok", "some-org/lib", AccessRead, api.URL); err != nil || name != "Some-Org/lib" {
		t.Fatalf("verify(read) = %q, %v; want GitHub's spelling", name, err)
	}
	if _, err := verify("tok", "some-org/lib", AccessWrite, api.URL); err == nil || !strings.Contains(err.Error(), "cannot push") {
		t.Fatalf("verify(write without push) error = %v", err)
	}
	if name, err := verify("tok", "yacchin1205/isotty", AccessAdmin, api.URL); err != nil || name != "yacchin1205/isotty" {
		t.Fatalf("verify(admin) = %q, %v", name, err)
	}
	if _, err := verify("tok", "some-org/app", AccessAdmin, api.URL); err == nil || !strings.Contains(err.Error(), "does not administer") {
		t.Fatalf("verify(admin without admin) error = %v", err)
	}
	if _, err := verify("tok", "some-org/lib", AccessAdmin, api.URL); err == nil || !strings.Contains(err.Error(), "cannot push") {
		t.Fatalf("verify(admin without push) error = %v", err)
	}
	if _, err := verify("tok", "nobody/missing", AccessRead, api.URL); err == nil || !strings.Contains(err.Error(), "not visible to the token") {
		t.Fatalf("verify(missing) error = %v", err)
	}
}
