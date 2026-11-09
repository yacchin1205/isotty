package browser

import (
	"bytes"
	"context"
	"encoding/json"
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
	view, raw := prefix+"view/", prefix+"raw/"
	_, port, _ := net.SplitHostPort(files.listener.Addr().String())
	wantOpened := []string{view + "figs/plot.png", view + "figs/plot.png", view + "run.command", view + "site/index.html"}
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

	// What opens is the viewer, the server's own page, with the file in it
	// as an image fetched from the server.
	resp := get(t, opened[0], "")
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), `<img id="image" src="/`+files.token+`/raw/figs/plot.png"`) || !strings.Contains(string(body), `data-kind="image"`) {
		t.Fatalf("the viewer page: %d\n%s", resp.StatusCode, body)
	}
	if policy := resp.Header.Get("Content-Security-Policy"); strings.Contains(policy, "sandbox") || !strings.Contains(policy, "script-src 'self'") || !strings.Contains(policy, "default-src 'none'") {
		t.Fatalf("the viewer's Content-Security-Policy = %q", policy)
	}

	// The file itself is served as what it is, inert, and only to the
	// host's own address.
	resp = get(t, raw+"figs/plot.png", "")
	body, _ = io.ReadAll(resp.Body)
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
	if resp := get(t, raw+"site/index.html", ""); resp.StatusCode != http.StatusOK {
		t.Fatalf("index.html: status = %d, want it served, not redirected", resp.StatusCode)
	}
	for _, u := range []string{opened[0], raw + "figs/plot.png", prefix + "reviews/figs/plot.png", prefix + "viewer.js"} {
		if resp := get(t, u, "evil.example:1"); resp.StatusCode != http.StatusNotFound {
			t.Fatalf("a foreign Host header must be refused: %s: %d", u, resp.StatusCode)
		}
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
		for _, base := range []string{raw, view, prefix + "reviews/"} {
			if resp := get(t, base+rel, ""); resp.StatusCode != http.StatusNotFound {
				t.Fatalf("GET %s%s: status = %d", base, rel, resp.StatusCode)
			}
		}
	}
	for _, u := range []string{"http://" + files.listener.Addr().String() + "/figs/plot.png", "http://" + files.listener.Addr().String() + "/raw/figs/plot.png", prefix + "figs/plot.png"} {
		if resp := get(t, u, ""); resp.StatusCode != http.StatusNotFound {
			t.Fatalf("%s must serve nothing: %d", u, resp.StatusCode)
		}
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

func TestViewerShowsAMarkdownFileAsAPageWithItsLines(t *testing.T) {
	project := t.TempDir()
	if err := os.MkdirAll(filepath.Join(project, "docs"), 0o755); err != nil {
		t.Fatal(err)
	}
	source := "# Report\n\n![plot](../figs/plot.png) and <https://example.com/> and [more](more.md#top) and [root](/README.md)\n\n<script>fetch('/')</script>\n\n- one\n- two\n  more\n\n| a | b |\n|---|---|\n| 1 | 2 |\n"
	if err := os.WriteFile(filepath.Join(project, "docs", "report.md"), []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	files := newFiles(t, project, func() error { return nil })

	// The page is the server's own: the source's HTML is left out, every
	// top-level block and list item says which source lines it came from,
	// an image the file names is fetched from the server as a file and a
	// link leads to the page of its target.
	resp := get(t, files.url("docs/report.md"), "")
	body, _ := io.ReadAll(resp.Body)
	page := string(body)
	if resp.StatusCode != http.StatusOK || !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/html") {
		t.Fatalf("served %d %s", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	token := files.token
	for _, want := range []string{
		"<title>report.md</title>",
		`<div class="block" data-lines="1-1"><h1>Report</h1>`,
		`<div class="block" data-lines="3-3"><p><img src="/` + token + `/raw/figs/plot.png" alt="plot">`,
		`<a href="https://example.com/"`,
		`<a href="/` + token + `/view/docs/more.md#top"`,
		`<a href="/` + token + `/view/README.md"`,
		`<div class="block" data-lines="7-9"><ul>`,
		`<li data-lines="7-7">one</li>`,
		`<li data-lines="8-9">two`,
		`<div class="block" data-lines="11-13"><table>`,
		`data-reviews="/` + token + `/reviews/docs/report.md"`,
	} {
		if !strings.Contains(page, want) {
			t.Fatalf("page lacks %s:\n%s", want, page)
		}
	}
	if strings.Contains(page, "<script>fetch") || strings.Contains(page, "sandbox") || !strings.Contains(page, `<script src="/`+token+`/viewer.js">`) {
		t.Fatalf("the page is not the server's own with the source's script left out:\n%s", page)
	}
	if resp := get(t, files.url("docs/README.MD"), ""); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("a missing file is still missing: %d", resp.StatusCode)
	}
}

// A text is shown line by line, with its lines numbered so that one can be
// pointed at, and escaped so that it stays text; what is not text is not
// put on the page, only linked, for a comment on the whole of it.
func TestViewerShowsTextByTheLineAndTheRestAsAWhole(t *testing.T) {
	project := t.TempDir()
	for name, content := range map[string]string{
		"notes.txt":  "first <b>line</b>\nsecond\n\nfourth\n",
		"fix.patch":  "--- a\n+++ b\n@@ -1 +1 @@\n-x\n+y\n",
		"data.bin":   "PK\x00\x01\x02",
		"report.pdf": "%PDF-1.4\n\x00binary",
		"funny.txt":  "caf\xc3",
		"short.html": "<p>hi</p>",
	} {
		if err := os.WriteFile(filepath.Join(project, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	files := newFiles(t, project, func() error { return nil })
	page := func(rel string) string {
		resp := get(t, files.url(rel), "")
		body, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("%s: %d", rel, resp.StatusCode)
		}
		return string(body)
	}
	notes := page("notes.txt")
	for _, want := range []string{`data-kind="text"`, `<tr id="L1"><td class="n" data-line="1">1</td><td class="c">first &lt;b&gt;line&lt;/b&gt;</td></tr>`, `<tr id="L3"><td class="n" data-line="3">3</td><td class="c"></td></tr>`, `<tr id="L4"`} {
		if !strings.Contains(notes, want) {
			t.Fatalf("notes.txt lacks %s:\n%s", want, notes)
		}
	}
	if strings.Contains(notes, `id="L5"`) {
		t.Fatalf("the final newline became a line:\n%s", notes)
	}
	if patch := page("fix.patch"); !strings.Contains(patch, `data-kind="text"`) || !strings.Contains(patch, `<td class="c">+y</td>`) {
		t.Fatalf("fix.patch:\n%s", patch)
	}
	// HTML the VM wrote is text here, not a document.
	if html := page("short.html"); !strings.Contains(html, `data-kind="text"`) || !strings.Contains(html, "&lt;p&gt;hi&lt;/p&gt;") {
		t.Fatalf("short.html:\n%s", html)
	}
	// Too large for a page, whatever its name says.
	for _, name := range []string{"huge.txt", "huge.md"} {
		if err := os.WriteFile(filepath.Join(project, name), bytes.Repeat([]byte("a\n"), textLimit/2+1), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, rel := range []string{"data.bin", "report.pdf", "funny.txt", "huge.txt", "huge.md"} {
		if other := page(rel); !strings.Contains(other, `data-kind="other"`) || !strings.Contains(other, `href="/`+files.token+`/raw/`+rel+`"`) {
			t.Fatalf("%s:\n%s", rel, other)
		}
	}
}

func review(t *testing.T, files *Files, method, rel, body string) (int, string) {
	t.Helper()
	var payload io.Reader
	if body != "" {
		payload = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, "http://"+files.listener.Addr().String()+"/"+files.token+"/reviews/"+rel, payload)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(data)
}

// A comment the human makes in the viewer is kept beside the project under
// .isotty/reviews, as a line of JSON naming the place and the file's hash
// of the moment, where the sync carries it into the VM. It can be changed,
// resolved and removed, and only on a file the server shows.
func TestReviewsAreKeptBesideTheProjectForTheVM(t *testing.T) {
	project := t.TempDir()
	if err := os.MkdirAll(filepath.Join(project, "docs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project, "docs", "report.md"), []byte("# Report\n\nAll good.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	files := newFiles(t, project, func() error { return nil })

	status, answer := review(t, files, http.MethodPost, "docs/report.md", `{"anchor":{"lines":[3,3],"quote":"All good."},"body":"Is it, though?"}`)
	if status != http.StatusOK {
		t.Fatalf("post = %d %s", status, answer)
	}
	var made Comment
	if err := json.Unmarshal([]byte(answer), &made); err != nil {
		t.Fatal(err)
	}
	kept, err := os.ReadFile(filepath.Join(project, ".isotty", "reviews", "docs", "report.md.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"id":"` + made.ID + `"`, `"lines":[3,3]`, `"quote":"All good."`, `"body":"Is it, though?"`, `"file_sha256":"sha256:`, `"resolved":false`} {
		if !strings.Contains(string(kept), want) {
			t.Fatalf("the kept comment lacks %s:\n%s", want, kept)
		}
	}
	if strings.Count(string(kept), "\n") != 1 || !strings.HasSuffix(string(kept), "\n") {
		t.Fatalf("one comment is one line:\n%q", kept)
	}

	status, answer = review(t, files, http.MethodGet, "docs/report.md", "")
	if status != http.StatusOK || !strings.Contains(answer, `"file_sha256":"`+made.FileSHA256+`"`) || !strings.Contains(answer, made.ID) {
		t.Fatalf("get = %d %s", status, answer)
	}
	if status, answer := review(t, files, http.MethodPatch, "docs/report.md", `{"id":"`+made.ID+`","resolved":true,"body":"Fine."}`); status != http.StatusOK || !strings.Contains(answer, `"resolved":true`) || !strings.Contains(answer, `"body":"Fine."`) {
		t.Fatalf("patch = %d %s", status, answer)
	}
	if status, answer := review(t, files, http.MethodPost, "docs/report.md", `{"anchor":{},"body":"Overall fine."}`); status != http.StatusOK {
		t.Fatalf("post on the whole = %d %s", status, answer)
	}
	if status, answer := review(t, files, http.MethodDelete, "docs/report.md", `{"id":"`+made.ID+`"}`); status != http.StatusOK {
		t.Fatalf("delete = %d %s", status, answer)
	}
	status, answer = review(t, files, http.MethodGet, "docs/report.md", "")
	if status != http.StatusOK || strings.Contains(answer, made.ID) || !strings.Contains(answer, "Overall fine.") {
		t.Fatalf("after delete = %d %s", status, answer)
	}

	for body, want := range map[string]int{
		`{"anchor":{"lines":[0,3]},"body":"x"}`:                   http.StatusBadRequest,
		`{"anchor":{"lines":[3,2]},"body":"x"}`:                   http.StatusBadRequest,
		`{"anchor":{"point":[1.5,0]},"body":"x"}`:                 http.StatusBadRequest,
		`{"anchor":{"point":[0.5,0.5],"lines":[1,1]},"body":"x"}`: http.StatusBadRequest,
		`{"anchor":{"spot":1},"body":"x"}`:                        http.StatusBadRequest,
		`{"anchor":{},"body":""}`:                                 http.StatusBadRequest,
		`{"body":"x"}`:                                            http.StatusBadRequest,
		`{"id":"nope","resolved":true}`:                           http.StatusBadRequest,
	} {
		if status, answer := review(t, files, http.MethodPost, "docs/report.md", body); status != want {
			t.Fatalf("post %s = %d %s, want %d", body, status, answer, want)
		}
	}
	if status, _ := review(t, files, http.MethodPatch, "docs/report.md", `{"id":"nope","resolved":true}`); status != http.StatusNotFound {
		t.Fatalf("patch of an unknown comment = %d", status)
	}
	for _, rel := range []string{"docs/missing.md", "docs", "../etc/passwd"} {
		if status, _ := review(t, files, http.MethodPost, rel, `{"anchor":{},"body":"x"}`); status != http.StatusNotFound {
			t.Fatalf("post on %s = %d", rel, status)
		}
	}
	if entries, _ := os.ReadDir(filepath.Join(project, ".isotty", "reviews")); len(entries) != 1 {
		t.Fatalf("reviews dir holds %d entries, want only docs/", len(entries))
	}
}

func TestReviewsAreNotWrittenThroughLinksTheVMPlanted(t *testing.T) {
	project := t.TempDir()
	elsewhere := t.TempDir()
	for _, dir := range []string{"notes", "docs", filepath.Join(".isotty", "reviews")} {
		if err := os.MkdirAll(filepath.Join(project, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"notes/a.txt", "docs/b.txt"} {
		if err := os.WriteFile(filepath.Join(project, name), []byte("text\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// A linked directory on the way, and a linked file at the end; both as
	// the sync would deliver them, relative and inside the project.
	if err := os.Symlink(elsewhere, filepath.Join(project, "data")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join("..", "..", "data"), filepath.Join(project, ".isotty", "reviews", "notes")); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(project, ".isotty", "reviews", "docs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join("..", "..", "..", "docs", "b.txt"), filepath.Join(project, ".isotty", "reviews", "docs", "b.txt.jsonl")); err != nil {
		t.Fatal(err)
	}
	files := newFiles(t, project, func() error { return nil })

	for _, rel := range []string{"notes/a.txt", "docs/b.txt"} {
		for _, method := range []string{http.MethodGet, http.MethodPost} {
			if status, answer := review(t, files, method, rel, `{"anchor":{},"body":"x"}`); status != http.StatusForbidden {
				t.Fatalf("%s on %s = %d %s", method, rel, status, answer)
			}
		}
	}
	if entries, _ := os.ReadDir(elsewhere); len(entries) != 0 {
		t.Fatalf("the host wrote through the link: %v", entries)
	}
	if got, _ := os.ReadFile(filepath.Join(project, "docs", "b.txt")); string(got) != "text\n" {
		t.Fatalf("the linked file changed: %q", got)
	}
}
