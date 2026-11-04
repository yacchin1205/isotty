package browser

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func postOpen(t *testing.T, server *httptest.Server, body string) (int, string) {
	t.Helper()
	resp, err := http.Post(server.URL+"/open", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	text, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode, string(text)
}

func TestHandlerOpensWebURLsOnly(t *testing.T) {
	var opened, recorded []string
	server := httptest.NewServer(Handler(
		func(_ context.Context, url string) error { opened = append(opened, url); return nil },
		func(url string) { recorded = append(recorded, url) },
		nil,
	))
	defer server.Close()

	allowed := []string{
		"http://127.0.0.1:61002/qr/abc",
		"https://script.google.com/macros/s/x/exec?formId=1",
		"HTTPS://example.com/",
	}
	for _, url := range allowed {
		if status, text := postOpen(t, server, `{"url":"`+url+`"}`); status != http.StatusNoContent {
			t.Fatalf("%s: status = %d, body = %s", url, status, text)
		}
	}
	for _, body := range []string{
		`{"url":"file:///etc/passwd"}`,
		`{"url":"javascript:alert(1)"}`,
		`{"url":"vscode://file/etc/passwd"}`,
		`{"url":"mailto:someone@example.com"}`,
		`{"url":"example.com/no-scheme"}`,
		`{"url":""}`,
	} {
		status, text := postOpen(t, server, body)
		if status != http.StatusForbidden || !strings.Contains(text, "only http and https") {
			t.Fatalf("%s: status = %d, body = %s", body, status, text)
		}
	}
	if status, _ := postOpen(t, server, `{"url":"https://example.com/","extra":1}`); status != http.StatusBadRequest {
		t.Fatalf("unknown field status = %d", status)
	}
	if len(opened) != len(allowed) || len(recorded) != len(allowed) || opened[1] != allowed[1] {
		t.Fatalf("opened = %v, recorded = %v", opened, recorded)
	}
}

func TestHandlerReportsBrowserFailure(t *testing.T) {
	recorded := false
	server := httptest.NewServer(Handler(
		func(context.Context, string) error { return errors.New("no display") },
		func(string) { recorded = true },
		nil,
	))
	defer server.Close()
	status, text := postOpen(t, server, `{"url":"https://example.com/"}`)
	if status != http.StatusBadGateway || !strings.Contains(text, "no display") {
		t.Fatalf("status = %d, body = %s", status, text)
	}
	if recorded {
		t.Fatal("a failed launch must not be recorded as opened")
	}
}

func TestHandlerAnswersBeforeBookkeeping(t *testing.T) {
	bookkeeping := make(chan struct{})
	server := httptest.NewServer(Handler(
		func(context.Context, string) error { return nil },
		func(string) { <-bookkeeping },
		nil,
	))
	defer server.Close()

	done := make(chan int, 1)
	go func() {
		status, _ := postOpen(t, server, `{"url":"https://example.com/"}`)
		done <- status
	}()
	select {
	case status := <-done:
		if status != http.StatusNoContent {
			t.Fatalf("status = %d", status)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("response waited for the bookkeeping to finish")
	}
	close(bookkeeping)
}

func newFiles(t *testing.T, project string, flush func() error) *Files {
	t.Helper()
	files := &Files{Workspace: "/workspace", Project: project, Flush: flush, Ignored: func(rel string) bool {
		return rel == ".env" || strings.HasPrefix(rel, ".ssh/")
	}}
	if err := files.Serve(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { files.Close() })
	return files
}

func get(t *testing.T, rawURL string, host string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, rawURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	if host != "" {
		req.Host = host
	}
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	return resp
}

func TestHandlerServesWorkspaceFilesToTheHostBrowser(t *testing.T) {
	project := t.TempDir()
	for name, content := range map[string]string{
		"figs/plot.png":   "png",
		"site/index.html": "<script>fetch('/')</script>",
		"run.command":     "#!/bin/sh\nrm -rf ~\n",
		".env":            "SECRET=1",
		".ssh/id_ed25519": "key",
	} {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(project, name)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(project, name), []byte(content), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink("/etc", filepath.Join(project, "escape")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("figs/plot.png", filepath.Join(project, "latest.png")); err != nil {
		t.Fatal(err)
	}
	realProject, err := filepath.EvalSymlinks(project)
	if err != nil {
		t.Fatal(err)
	}
	var opened []string
	// The record is written after the response, so the test waits for it.
	recorded := make(chan string, 16)
	flushes := 0
	files := newFiles(t, project, func() error { flushes++; return nil })
	server := httptest.NewServer(Handler(
		func(_ context.Context, url string) error { opened = append(opened, url); return nil },
		func(target string) { recorded <- target },
		files,
	))
	defer server.Close()

	// The browser gets an http URL on the host's own loopback port, never
	// a file: URL, so a VM-made file is rendered and nothing executes it.
	// The record names the real file as the VM would, without the URL.
	for _, vmPath := range []string{"/workspace/figs/plot.png", "/workspace/latest.png", "/workspace/run.command", "/workspace/site/index.html"} {
		if status, text := postOpen(t, server, `{"path":"`+vmPath+`"}`); status != http.StatusNoContent {
			t.Fatalf("%s: status = %d, body = %s", vmPath, status, text)
		}
	}
	prefix := "http://" + files.listener.Addr().String() + "/" + files.token + "/"
	_, port, _ := net.SplitHostPort(files.listener.Addr().String())
	wantOpened := []string{prefix + "figs/plot.png", prefix + "figs/plot.png", prefix + "run.command", prefix + "site/index.html"}
	if strings.Join(opened, " ") != strings.Join(wantOpened, " ") || flushes != 4 {
		t.Fatalf("opened = %v\nwant %v after %d flushes", opened, wantOpened, flushes)
	}
	for _, want := range []string{"/workspace/figs/plot.png", "/workspace/figs/plot.png", "/workspace/run.command", "/workspace/site/index.html"} {
		select {
		case got := <-recorded:
			if got != want {
				t.Fatalf("recorded %q, want %q", got, want)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("no record for %s", want)
		}
	}

	// Served as what it is, inert, and only to the host's own address.
	resp := get(t, opened[0], "")
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK || string(body) != "png" || !strings.HasPrefix(resp.Header.Get("Content-Type"), "image/png") {
		t.Fatalf("served %d %q %s", resp.StatusCode, body, resp.Header.Get("Content-Type"))
	}
	policy := resp.Header.Get("Content-Security-Policy")
	for _, directive := range []string{"sandbox", "default-src 'none'", "img-src 'self'"} {
		if !strings.Contains(policy, directive) {
			t.Fatalf("Content-Security-Policy = %q, want %s", policy, directive)
		}
	}
	if got := resp.Header.Get("X-Content-Type-Options"); got != "nosniff" {
		t.Fatalf("X-Content-Type-Options = %q", got)
	}
	// A sandboxed page has an opaque origin, so a resource policy would
	// count the page's own images as foreign and keep them from it.
	if _, set := resp.Header["Cross-Origin-Resource-Policy"]; set {
		t.Fatal("Cross-Origin-Resource-Policy would keep a sandboxed page from its own images")
	}
	if resp := get(t, opened[3], ""); resp.StatusCode != http.StatusOK {
		t.Fatalf("index.html: status = %d, want it served, not redirected", resp.StatusCode)
	}
	if resp := get(t, opened[0], "evil.example:1"); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("a foreign Host header must be refused: %d", resp.StatusCode)
	}

	for body, want := range map[string]int{
		`{"path":"/workspace/figs/missing.png"}`:                           http.StatusNotFound,
		`{"path":"/workspace/.env"}`:                                       http.StatusNotFound,
		`{"path":"/workspace/.ssh/id_ed25519"}`:                            http.StatusNotFound,
		`{"path":"/workspace/../etc/passwd"}`:                              http.StatusForbidden,
		`{"path":"/etc/passwd"}`:                                           http.StatusForbidden,
		`{"path":"figs/plot.png"}`:                                         http.StatusForbidden,
		`{"path":"/workspace/escape/passwd"}`:                              http.StatusForbidden,
		`{"path":"/workspace/figs"}`:                                       http.StatusForbidden,
		`{"path":"/workspace/figs/plot.png","url":"https://example.com/"}`: http.StatusBadRequest,
		`{"url":"` + prefix + `.env"}`:                                     http.StatusForbidden,
		`{"url":"http://localhost:` + port + `/x"}`:                        http.StatusForbidden,
		`{"url":"http://127.1:` + port + `/x"}`:                            http.StatusForbidden,
		`{"url":"http://example.com:` + port + `/x"}`:                      http.StatusForbidden,
	} {
		status, text := postOpen(t, server, body)
		if status != want {
			t.Fatalf("%s: status = %d, want %d; body = %s", body, status, want, text)
		}
		if strings.Contains(text, project) || strings.Contains(text, realProject) {
			t.Fatalf("%s: the error names a host path: %s", body, text)
		}
	}
	if len(opened) != 4 {
		t.Fatalf("refused requests must not open anything: %v", opened)
	}

	// The file server itself serves nothing but regular files under the
	// project that the sync shares, and nothing without the token.
	for _, rel := range []string{"..%2F..%2Fetc%2Fpasswd", "escape/passwd", "figs", "missing.png", ".env", ".ssh/id_ed25519"} {
		if resp := get(t, prefix+rel, ""); resp.StatusCode != http.StatusNotFound {
			t.Fatalf("GET %s: status = %d", rel, resp.StatusCode)
		}
	}
	if resp := get(t, "http://"+files.listener.Addr().String()+"/figs/plot.png", ""); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("a URL without the token must serve nothing: %d", resp.StatusCode)
	}
}

func TestHandlerRefusesFilesWithoutAProject(t *testing.T) {
	server := httptest.NewServer(Handler(
		func(context.Context, string) error { t.Fatal("nothing should open"); return nil },
		func(string) {},
		nil,
	))
	defer server.Close()
	if status, text := postOpen(t, server, `{"path":"/workspace/plot.png"}`); status != http.StatusForbidden || !strings.Contains(text, "no project directory on the host") {
		t.Fatalf("status = %d, body = %s", status, text)
	}
}

func TestHandlerReportsASyncFailure(t *testing.T) {
	files := newFiles(t, t.TempDir(), func() error { return errors.New("session ended") })
	server := httptest.NewServer(Handler(
		func(context.Context, string) error { t.Fatal("nothing should open"); return nil },
		func(string) {},
		files,
	))
	defer server.Close()
	if status, text := postOpen(t, server, `{"path":"/workspace/plot.png"}`); status != http.StatusBadGateway || !strings.Contains(text, "session ended") {
		t.Fatalf("status = %d, body = %s", status, text)
	}
}
