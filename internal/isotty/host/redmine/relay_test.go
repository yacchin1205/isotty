package redmine

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

const token = "ghp_1234567890abcdefghijABCDEFGHIJ123456"

// fakeRedmine answers like a Redmine with one pinned project, "infra"
// (id 7), one other project (id 9), issues, a wiki page and attachments.
// Every call is recorded.
type fakeRedmine struct {
	server *httptest.Server
	calls  []string
	keys   []string
}

func newFakeRedmine(t *testing.T) *fakeRedmine {
	f := &fakeRedmine{}
	f.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.calls = append(f.calls, r.URL.Path+"?"+r.URL.RawQuery)
		f.keys = append(f.keys, r.Header.Get("X-Redmine-API-Key"))
		base := f.server.URL
		answer := func(body string) {
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(body))
		}
		switch r.URL.Path {
		case "/users/current.json":
			answer(`{"user":{"id":1,"login":"someone","firstname":"Some","lastname":"One"}}`)
		case "/projects/infra.json":
			answer(`{"project":{"id":7,"identifier":"infra","name":"Infrastructure","description":"山田さんが運用している"}}`)
		case "/projects/secret.json":
			w.WriteHeader(http.StatusForbidden)
		case "/issues.json":
			answer(`{"issues":[{"id":101,"project":{"id":7,"name":"Infrastructure"},"subject":"山田さんがログインできない","author":{"id":12,"name":"Taro Yamada"},"assigned_to":{"id":15,"name":"Hanako Mori"},"status":{"id":1,"name":"New"}}],"total_count":1,"offset":0,"limit":25}`)
		case "/issues/101.json":
			answer(`{"issue":{"id":101,"project":{"id":7,"name":"Infrastructure"},"subject":"山田さんがログインできない","description":"taro@example.org に連絡するか 03-1234-5678 に電話。森さんが対応する。トークンは ` + token + `","author":{"id":12,"name":"Taro Yamada"},"assigned_to":{"id":15,"name":"Hanako Mori"},"journals":[{"id":9,"user":{"id":20,"name":"Jiro Suzuki"},"notes":"山田さんの件は確認済み。","created_on":"2026-11-10T00:00:00Z"}],"watchers":[{"id":15,"name":"Hanako Mori"}],"attachments":[{"id":5,"filename":"log.txt","content_type":"text/plain","content_url":"` + base + `/attachments/download/5/log.txt"},{"id":6,"filename":"spec.pdf","content_type":"application/pdf","content_url":"` + base + `/attachments/download/6/spec.pdf"},{"id":8,"filename":"photo.png","content_type":"image/png","content_url":"` + base + `/attachments/download/8/photo.png"},{"id":10,"filename":"山田さんのバックアップ.zip","content_type":"application/zip","content_url":"` + base + `/attachments/download/10/backup.zip"}]}}`)
		case "/issues/202.json":
			answer(`{"issue":{"id":202,"project":{"id":9,"name":"Other"},"subject":"Elsewhere","author":{"id":12,"name":"Taro Yamada"}}}`)
		case "/issues/303.json":
			answer(`{"issue":{"id":303,"project":{"id":7,"name":"Infrastructure"},"subject":"No id","author":{"name":"Taro Yamada"}}}`)
		case "/projects/infra/wiki/index.json":
			answer(`{"wiki_pages":[{"title":"Setup","version":3,"updated_on":"2026-11-01T00:00:00Z"}]}`)
		case "/projects/infra/wiki/Setup.json":
			answer(`{"wiki_page":{"title":"Setup","text":"山田さん（taro@example.org）に聞く。","version":3,"author":{"id":12,"name":"Taro Yamada"},"attachments":[{"id":5,"filename":"log.txt","content_type":"text/plain","content_url":"` + base + `/attachments/download/5/log.txt"}]}}`)
		case "/attachments/download/5/log.txt":
			w.Header().Set("Content-Type", "text/plain")
			w.Write([]byte("山田さん <taro@example.org> が再起動した\n"))
		case "/attachments/download/6/spec.pdf":
			w.Header().Set("Content-Type", "application/pdf")
			w.Write([]byte("%PDF-fake"))
		case "/attachments/download/8/photo.png":
			w.Header().Set("Content-Type", "image/png")
			w.Write([]byte("PNG-fake"))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(f.server.Close)
	return f
}

// fakeTools puts poppler and tesseract stand-ins first on PATH. pdftotext
// answers a PDF of two pages, the second without a text layer; pdftoppm
// renders the page it is asked for; tesseract reads the rendered file
// back, so the output shows which page was read and how.
func fakeTools(t *testing.T) {
	dir := t.TempDir()
	scripts := map[string]string{
		"pdftotext": `[ "$1" = -layout ] && [ "$3" = - ] || exit 2
printf '山田さんの資料\f\f'`,
		"pdftoppm": `[ "$1" = -f ] && [ "$8" = -singlefile ] || exit 2
printf 'page %s' "$2" > "${10}.png"`,
		"tesseract": `[ "$2" = stdout ] && [ "$4" = jpn+eng ] || exit 2
printf '%s: 森さん 090-1234-5678\n' "$(cat "$1")"`,
	}
	for name, script := range scripts {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\n"+script+"\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

var testSalt = []byte("0123456789abcdef0123456789abcdef")

// person is the pseudonym the relay gives the user id under testSalt.
func person(id int) string {
	return (&Filter{salt: testSalt}).pseudonym(id)
}

type testRelay struct {
	server  *httptest.Server
	redmine *fakeRedmine
	mu      sync.Mutex
	records []Delivery
}

func newTestRelay(t *testing.T, pins ProjectConfig) *testRelay {
	tr := &testRelay{redmine: newFakeRedmine(t)}
	record := func(d Delivery) error {
		tr.mu.Lock()
		defer tr.mu.Unlock()
		tr.records = append(tr.records, d)
		return nil
	}
	handler, err := Handler(HostConfig{URL: tr.redmine.server.URL, APIKey: "key"}, pins, testSalt, record)
	if err != nil {
		t.Fatal(err)
	}
	tr.server = httptest.NewServer(http.StripPrefix("/redmine", handler))
	t.Cleanup(tr.server.Close)
	return tr
}

func (tr *testRelay) last() Delivery {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	return tr.records[len(tr.records)-1]
}

func get(t *testing.T, url string) (int, string, string) {
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode, string(body), resp.Header.Get("Content-Type")
}

var infraPins = ProjectConfig{Projects: []string{"infra"}}

func TestHandlerReadsThePinnedProjectsFirst(t *testing.T) {
	redmine := newFakeRedmine(t)
	cfg := HostConfig{URL: redmine.server.URL, APIKey: "key"}
	none := func(Delivery) error { return nil }
	if _, err := Handler(cfg, ProjectConfig{Projects: []string{"secret"}}, testSalt, none); err == nil || !strings.Contains(err.Error(), "secret does not exist or is not visible") {
		t.Fatalf("handler with an unreadable project: %v", err)
	}
	if _, err := Handler(cfg, infraPins, testSalt, none); err != nil {
		t.Fatal(err)
	}
	for _, key := range redmine.keys {
		if key != "key" {
			t.Fatalf("a call carried key %q", key)
		}
	}
}

func TestRelayStatusAndProject(t *testing.T) {
	tr := newTestRelay(t, infraPins)
	status, body, _ := get(t, tr.server.URL+"/redmine/status")
	if status != 200 || !strings.Contains(body, `"identifier":"infra"`) || !strings.Contains(body, tr.redmine.server.URL) {
		t.Fatalf("status = %d %s", status, body)
	}
	if record := tr.last(); record.Target != "/status" || record.Content != body {
		t.Fatalf("status record = %+v", record)
	}
	status, body, _ = get(t, tr.server.URL+"/redmine/projects/infra")
	if status != 200 || !strings.Contains(body, `"description":"[name]さんが運用している"`) {
		t.Fatalf("project = %d %s", status, body)
	}
	status, body, _ = get(t, tr.server.URL+"/redmine/projects/other")
	if status != 403 || !strings.Contains(body, "not among the projects pinned") {
		t.Fatalf("unpinned project = %d %s", status, body)
	}
}

func TestRelayListsIssuesOfTheProjectOnly(t *testing.T) {
	tr := newTestRelay(t, infraPins)
	status, body, _ := get(t, tr.server.URL+"/redmine/projects/infra/issues?status_id=open&limit=5")
	if status != 200 {
		t.Fatalf("issues = %d %s", status, body)
	}
	last := tr.redmine.calls[len(tr.redmine.calls)-1]
	for _, want := range []string{"/issues.json?", "project_id=infra", "subproject_id=%21%2A", "status_id=open", "limit=5"} {
		if !strings.Contains(last, want) {
			t.Fatalf("upstream call %q lacks %q", last, want)
		}
	}
	if !strings.Contains(body, `"subject":"[name]さんがログインできない"`) || !strings.Contains(body, `"assigned_to":{"id":15,"name":"`+person(15)+`"}`) {
		t.Fatalf("issues not filtered: %s", body)
	}
	status, body, _ = get(t, tr.server.URL+"/redmine/projects/infra/issues?project_id=secret")
	if status != 400 || !strings.Contains(body, "project_id is not accepted") {
		t.Fatalf("foreign parameter = %d %s", status, body)
	}
}

func TestRelayFiltersAnIssue(t *testing.T) {
	tr := newTestRelay(t, infraPins)
	status, body, _ := get(t, tr.server.URL+"/redmine/issues/101")
	if status != 200 {
		t.Fatalf("issue = %d %s", status, body)
	}
	for _, want := range []string{
		`"author":{"id":12,"name":"` + person(12) + `"}`,
		`"user":{"id":20,"name":"` + person(20) + `"}`,
		`"watchers":[{"id":15,"name":"` + person(15) + `"}]`,
		`"subject":"[name]さんがログインできない"`,
		`"description":"[email] に連絡するか [phone] に電話。[name]さんが対応する。トークンは [secret]"`,
		`"notes":"[name]さんの件は確認済み。"`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("issue lacks %s:\n%s", want, body)
		}
	}
	for _, leaked := range []string{"Yamada", "Mori", "Suzuki", "山田", "森", "taro@", "03-1234", token} {
		if strings.Contains(body, leaked) {
			t.Fatalf("issue leaks %s:\n%s", leaked, body)
		}
	}
	if person(12) == person(15) || !strings.HasPrefix(person(12), "Person-") || len(person(12)) != len("Person-")+6 {
		t.Fatalf("pseudonyms %s %s", person(12), person(15))
	}
	// The record carries what the VM got and where it was changed.
	record := tr.last()
	if record.Target != "/issues/101" || record.Content != body {
		t.Fatalf("record = %+v", record)
	}
	kinds := map[string]bool{}
	for _, m := range record.Marks {
		kinds[m.Field+" "+m.Kind] = true
	}
	for _, want := range []string{"issue.author.name person", "issue.journals[0].user.name person", "issue.watchers[0].name person", "issue.description email", "issue.description phone", "issue.description name", "issue.description secret", "issue.subject name"} {
		if !kinds[want] {
			t.Fatalf("record marks lack %q: %+v", want, record.Marks)
		}
	}
	status, body, _ = get(t, tr.server.URL+"/redmine/issues/202")
	if status != 403 || !strings.Contains(body, "issue #202 belongs to a project that is not pinned") || strings.Contains(body, "Yamada") {
		t.Fatalf("issue of another project = %d %s", status, body)
	}
	status, body, _ = get(t, tr.server.URL+"/redmine/issues/303")
	if status != 502 || !strings.Contains(body, "issue.author names a person without a user id") || strings.Contains(body, "Yamada") {
		t.Fatalf("person without an id = %d %s", status, body)
	}
}

func TestMarksPointAtTheReplacements(t *testing.T) {
	f, err := NewFilter(testSalt)
	if err != nil {
		t.Fatal(err)
	}
	text, marks := f.Text("森さん、taro@example.org と 090-1234-5678、" + token + " を確認")
	if text != "[name]さん、[email] と [phone]、[secret] を確認" {
		t.Fatalf("text = %q", text)
	}
	runes := []rune(text)
	var got []string
	for _, m := range marks {
		got = append(got, m.Kind+"="+string(runes[m.Start:m.End]))
	}
	if strings.Join(got, " ") != "name=[name] email=[email] phone=[phone] secret=[secret]" {
		t.Fatalf("marks = %v", got)
	}
}

func TestRelayWithholdsWhatItCannotRecord(t *testing.T) {
	redmine := newFakeRedmine(t)
	handler, err := Handler(HostConfig{URL: redmine.server.URL, APIKey: "key"}, infraPins, testSalt, func(Delivery) error { return errors.New("the VM did not answer") })
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.StripPrefix("/redmine", handler))
	defer server.Close()
	status, body, _ := get(t, server.URL+"/redmine/issues/101")
	if status != 502 || !strings.Contains(body, "audit record could not be written: the VM did not answer") || strings.Contains(body, "ログイン") {
		t.Fatalf("unrecorded answer = %d %s", status, body)
	}
}

func TestRelayWiki(t *testing.T) {
	tr := newTestRelay(t, infraPins)
	status, body, _ := get(t, tr.server.URL+"/redmine/projects/infra/wiki")
	if status != 200 || !strings.Contains(body, `"title":"Setup"`) {
		t.Fatalf("wiki index = %d %s", status, body)
	}
	status, body, _ = get(t, tr.server.URL+"/redmine/projects/infra/wiki/Setup")
	if status != 200 || !strings.Contains(body, `"text":"[name]さん（[email]）に聞く。"`) || !strings.Contains(body, `"author":{"id":12,"name":"`+person(12)+`"}`) {
		t.Fatalf("wiki page = %d %s", status, body)
	}
	status, _, _ = get(t, tr.server.URL+"/redmine/projects/other/wiki/Setup")
	if status != 403 {
		t.Fatalf("wiki of an unpinned project = %d", status)
	}
}

func TestRelayAttachments(t *testing.T) {
	fakeTools(t)
	tr := newTestRelay(t, infraPins)
	status, body, contentType := get(t, tr.server.URL+"/redmine/issues/101/attachments/5/download")
	if status != 200 || body != "[name]さん <[email]> が再起動した\n" || contentType != "text/plain; charset=utf-8" {
		t.Fatalf("text attachment = %d %q %s", status, body, contentType)
	}
	if record := tr.last(); record.Target != "/issues/101/attachments/5/download" || record.Content != body || len(record.Marks) != 2 {
		t.Fatalf("attachment record = %+v", record)
	}
	// The text layer of page one, OCR for page two, both filtered.
	status, body, _ = get(t, tr.server.URL+"/redmine/issues/101/attachments/6/download")
	if status != 200 || body != "[name]さんの資料\fpage 2: [name]さん [phone]\n" {
		t.Fatalf("pdf attachment = %d %q", status, body)
	}
	status, body, _ = get(t, tr.server.URL+"/redmine/issues/101/attachments/8/download")
	if status != 200 || body != "PNG-fake: [name]さん [phone]\n" {
		t.Fatalf("image attachment = %d %q", status, body)
	}
	status, body, _ = get(t, tr.server.URL+"/redmine/issues/101/attachments/10/download")
	if status != 415 || !strings.Contains(body, "attachment 10 is application/zip") || strings.Contains(body, "山田") {
		t.Fatalf("zip attachment = %d %s", status, body)
	}
	status, body, _ = get(t, tr.server.URL+"/redmine/issues/101/attachments/99/download")
	if status != 404 || !strings.Contains(body, "not listed there") {
		t.Fatalf("unlisted attachment = %d %s", status, body)
	}
	status, _, _ = get(t, tr.server.URL+"/redmine/issues/202/attachments/5/download")
	if status != 403 {
		t.Fatalf("attachment through another project's issue = %d", status)
	}
	status, body, _ = get(t, tr.server.URL+"/redmine/projects/infra/wiki/Setup/attachments/5/download")
	if status != 200 || !strings.HasPrefix(body, "[name]さん <[email]>") {
		t.Fatalf("wiki attachment = %d %q", status, body)
	}
}

func TestRelayNamesTheMissingTool(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	tr := newTestRelay(t, infraPins)
	status, body, _ := get(t, tr.server.URL+"/redmine/issues/101/attachments/6/download")
	if status != 502 || !strings.Contains(body, "pdftotext is not installed on the host") {
		t.Fatalf("pdf without poppler = %d %s", status, body)
	}
}

func TestPhonePattern(t *testing.T) {
	for _, phone := range []string{"03-1234-5678", "0120-123-456", "090 1234 5678", "090(1234)5678", "(03)1234-5678", "09012345678", "0312345678", "+81-90-1234-5678", "+819012345678", "+81 (0)3 1234 5678"} {
		if found := phonePattern.FindString("tel " + phone + " ok"); found != phone {
			t.Errorf("%q found as %q", phone, found)
		}
	}
	if found := phonePattern.FindString("Suzuki (090-9876-5432) confirmed"); found != "090-9876-5432" {
		t.Errorf("a number in parentheses found as %q", found)
	}
	for _, other := range []string{"2026-11-17", "v0.12.3", "0157-3310", "1234-5678"} {
		if found := phonePattern.FindString("x " + other + " y"); found != "" {
			t.Errorf("%q matched %q", other, found)
		}
	}
}
