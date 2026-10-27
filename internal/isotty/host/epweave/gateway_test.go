package epweave

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeEtherpad implements just enough of the Etherpad API and ep_weave's
// search endpoint for the Notes operations: pads keyed by id, titles taken
// from the first <h1> like ep_weave does.
type fakeEtherpad struct {
	mu   sync.Mutex
	pads map[string]*fakePad
	// issued counts the tokens handed out; only the latest is accepted, so
	// raising it stands for the token's hour running out.
	issued int
}

func (f *fakeEtherpad) expireToken() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.issued++
}

type fakePad struct {
	html      string
	revisions int
	// history[rev] is the html after that revision, so getHTML with rev works.
	history []string
}

// plainText mimics getText: block ends become line breaks, tags disappear.
func plainText(html string) string {
	return strings.Join(htmlLines(html), "\n") + "\n"
}

func (p *fakePad) set(html string) {
	p.html = html
	p.revisions++
	for len(p.history) <= p.revisions {
		p.history = append(p.history, "")
	}
	p.history[p.revisions] = html
}

// title mimics ep_weave: the first line of the pad, whether a heading or plain text.
func (f *fakeEtherpad) title(html string) string {
	body := html[strings.Index(html, "<body>")+len("<body>"):]
	if strings.HasPrefix(body, "<h1>") {
		return body[4:strings.Index(body, "</h1>")]
	}
	return body[:strings.Index(body, "<br>")]
}

// matchesTitleQuery handles the two query shapes the client sends:
// an escaped exact title and a "title:foo*" prefix search.
func matchesTitleQuery(query, title string) bool {
	if !strings.HasPrefix(query, "title:") {
		return true
	}
	wanted := strings.ReplaceAll(strings.TrimPrefix(query, "title:"), `\`, "")
	if strings.HasSuffix(wanted, "*") {
		return strings.HasPrefix(title, strings.TrimSuffix(wanted, "*"))
	}
	return title == wanted
}

func (f *fakeEtherpad) handler(t *testing.T) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /oidc/token", func(w http.ResponseWriter, r *http.Request) {
		if r.FormValue("resource") == "" {
			t.Error("token request without resource")
		}
		f.mu.Lock()
		f.issued++
		token := fmt.Sprintf("tok%d", f.issued)
		f.mu.Unlock()
		fmt.Fprintf(w, `{"access_token":%q}`, token)
	})
	mux.HandleFunc("GET /search/", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		query := r.URL.Query().Get("query")
		var docs []map[string]string
		for id, pad := range f.pads {
			title := f.title(pad.html)
			if !matchesTitleQuery(query, title) {
				continue
			}
			docs = append(docs, map[string]string{"id": id, "title": title})
		}
		if docs == nil {
			docs = []map[string]string{}
		}
		json.NewEncoder(w).Encode(map[string]any{"docs": docs})
	})
	mux.HandleFunc("POST /api/1.3.1/{method}", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		if r.Header.Get("Authorization") != fmt.Sprintf("Bearer tok%d", f.issued) {
			http.Error(w, `{"code":4,"message":"no or wrong OAuth token","data":null}`, http.StatusUnauthorized)
			return
		}
		ok := func(data any) {
			json.NewEncoder(w).Encode(map[string]any{"code": 0, "message": "ok", "data": data})
		}
		fail := func(message string) {
			json.NewEncoder(w).Encode(map[string]any{"code": 1, "message": message, "data": nil})
		}
		padID := r.FormValue("padID")
		pad, exists := f.pads[padID]
		switch r.PathValue("method") {
		case "createAuthorIfNotExistsFor":
			ok(map[string]string{"authorID": "a." + r.FormValue("authorMapper")})
		case "createPad":
			if exists {
				fail("padID does already exist")
				return
			}
			f.pads[padID] = &fakePad{html: "<!DOCTYPE HTML><html><body><br></body></html>", history: []string{"<html><body><br></body></html>"}}
			ok(nil)
		case "setHTML":
			if !exists {
				fail("padID does not exist")
				return
			}
			pad.set(r.FormValue("html"))
			ok(nil)
		case "getHTML":
			if !exists {
				fail("padID does not exist")
				return
			}
			if raw := r.FormValue("rev"); raw != "" {
				rev, err := strconv.Atoi(raw)
				if err != nil || rev < 0 || rev >= len(pad.history) {
					fail("rev is higher than the head revision of the pad")
					return
				}
				ok(map[string]string{"html": pad.history[rev]})
				return
			}
			ok(map[string]string{"html": pad.html})
		case "getText":
			if !exists {
				fail("padID does not exist")
				return
			}
			ok(map[string]string{"text": plainText(pad.html)})
		case "getRevisionsCount":
			if !exists {
				fail("padID does not exist")
				return
			}
			ok(map[string]int{"revisions": pad.revisions})
		case "createDiffHTML":
			ok(map[string]any{"html": "<span>diff</span>", "authors": []string{"a.human"}})
		case "getAuthorName":
			ok("Human")
		default:
			t.Errorf("unexpected API method %s", r.PathValue("method"))
		}
	})
	return mux
}

func newTestGateway(t *testing.T) (*httptest.Server, *fakeEtherpad, []WriteEvent, *[]WriteEvent) {
	t.Helper()
	lookupRetryDelay = time.Millisecond
	etherpad := &fakeEtherpad{pads: map[string]*fakePad{}}
	upstream := httptest.NewServer(etherpad.handler(t))
	t.Cleanup(upstream.Close)

	notes, err := Connect(
		HostConfig{URL: upstream.URL, ClientID: "id", ClientSecret: "secret"},
		ProjectConfig{Prefix: "isotty"},
		"isotty-abc", "isotty:isotty",
	)
	if err != nil {
		t.Fatalf("Connect() error = %v", err)
	}
	var events []WriteEvent
	gateway := httptest.NewServer(Handler(notes, func(event WriteEvent) error {
		events = append(events, event)
		return nil
	}))
	t.Cleanup(gateway.Close)
	return gateway, etherpad, events, &events
}

func doJSON(t *testing.T, method, url string, body any) (int, map[string]any) {
	t.Helper()
	var reader *strings.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		reader = strings.NewReader(string(data))
	} else {
		reader = strings.NewReader("")
	}
	req, err := http.NewRequest(method, url, reader)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var payload map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("decode %s %s: %v", method, url, err)
	}
	return resp.StatusCode, payload
}

func TestGatewayStatus(t *testing.T) {
	gateway, _, _, _ := newTestGateway(t)
	status, payload := doJSON(t, http.MethodGet, gateway.URL+"/status", nil)
	if status != http.StatusOK || payload["prefix"] != "isotty/" {
		t.Fatalf("status = %d, payload = %v", status, payload)
	}
}

func TestGatewayCreateAppendRead(t *testing.T) {
	gateway, etherpad, _, events := newTestGateway(t)

	status, payload := doJSON(t, http.MethodPost, gateway.URL+"/create", map[string]string{
		"title": "isotty/design", "markdown": "# Why\n- one\n- two\n\nBody <b>\n", "tags": "#isotty #決定",
	})
	if status != http.StatusOK {
		t.Fatalf("create status = %d, payload = %v", status, payload)
	}
	padID := payload["pad_id"].(string)
	if !strings.HasSuffix(payload["url"].(string), "/p/"+padID) || payload["revisions"].(float64) != 1 {
		t.Fatalf("payload = %v", payload)
	}
	want := "<!DOCTYPE html><html><body><h1>isotty/design</h1><h1>Why</h1><ul><li>one</li><li>two</li></ul><p>Body &lt;b&gt;</p><p>#isotty #決定</p></body></html>"
	if got := etherpad.pads[padID].html; got != want {
		t.Fatalf("pad html = %s, want %s", got, want)
	}

	status, payload = doJSON(t, http.MethodPost, gateway.URL+"/create", map[string]string{"title": "isotty/design"})
	if status != http.StatusConflict {
		t.Fatalf("duplicate create status = %d, payload = %v", status, payload)
	}

	// Simulate ep_weave rendering the hashtag line the way getHTML returns it.
	etherpad.pads[padID].history[1] = "<!DOCTYPE HTML><html><body>isotty/design<br>Body<br>#isotty #決定<br></body></html>"
	etherpad.pads[padID].html = etherpad.pads[padID].history[1]
	status, payload = doJSON(t, http.MethodPost, gateway.URL+"/append", map[string]string{"title": "isotty/design", "markdown": "## Later\nmore"})
	if status != http.StatusOK {
		t.Fatalf("append status = %d, payload = %v", status, payload)
	}
	want = "<!DOCTYPE HTML><html><body>isotty/design<br>Body<br><h2>Later</h2><p>more</p>#isotty #決定<br></body></html>"
	if got := etherpad.pads[padID].html; got != want {
		t.Fatalf("appended html = %s, want %s", got, want)
	}

	status, payload = doJSON(t, http.MethodGet, gateway.URL+"/read?title=isotty%2Fdesign&format=html", nil)
	if status != http.StatusOK || payload["pad_id"] != padID || payload["revisions"].(float64) != 2 {
		t.Fatalf("read status = %d, payload = %v", status, payload)
	}

	status, payload = doJSON(t, http.MethodGet, gateway.URL+"/changes?title=isotty%2Fdesign&since=1", nil)
	if status != http.StatusOK {
		t.Fatalf("changes status = %d, payload = %v", status, payload)
	}
	if _, hasHTML := payload["html"]; hasHTML {
		t.Fatalf("text changes should omit html: %v", payload)
	}
	added := payload["added_lines"].([]any)
	if len(added) != 2 || added[0] != "Later" || added[1] != "more" {
		t.Fatalf("added_lines = %v", added)
	}
	status, payload = doJSON(t, http.MethodGet, gateway.URL+"/changes?title=isotty%2Fdesign&since=1&format=html", nil)
	if status != http.StatusOK || payload["html"] != "<span>diff</span>" {
		t.Fatalf("html changes status = %d, payload = %v", status, payload)
	}

	if len(*events) != 2 || (*events)[0].Operation != "create" || (*events)[1].Operation != "append" || (*events)[1].PadID != padID {
		t.Fatalf("write events = %#v", *events)
	}
}

func TestGatewayRenewsExpiredToken(t *testing.T) {
	gateway, etherpad, _, events := newTestGateway(t)
	if status, body := doJSON(t, "POST", gateway.URL+"/create", map[string]string{"title": "isotty/a", "markdown": "x"}); status != 200 {
		t.Fatalf("create = %d %v", status, body)
	}
	etherpad.expireToken()
	status, body := doJSON(t, "POST", gateway.URL+"/append", map[string]string{"title": "isotty/a", "markdown": "y"})
	if status != 200 {
		t.Fatalf("append after expiry = %d %v", status, body)
	}
	if len(*events) != 2 {
		t.Fatalf("events = %+v", *events)
	}
	etherpad.mu.Lock()
	issued := etherpad.issued
	etherpad.mu.Unlock()
	if issued != 3 {
		t.Fatalf("tokens issued = %d, want the first, the expiry and the renewal", issued)
	}
}

func TestGatewayRejectsWritesOutsidePrefix(t *testing.T) {
	gateway, etherpad, _, events := newTestGateway(t)
	for _, title := range []string{"other/design", "isotty", "isotty/", "isottyx/design"} {
		status, payload := doJSON(t, http.MethodPost, gateway.URL+"/create", map[string]string{"title": title})
		if status != http.StatusForbidden {
			t.Fatalf("create %q status = %d, payload = %v", title, status, payload)
		}
		status, payload = doJSON(t, http.MethodPost, gateway.URL+"/append", map[string]string{"title": title})
		if status != http.StatusForbidden {
			t.Fatalf("append %q status = %d, payload = %v", title, status, payload)
		}
	}
	if len(etherpad.pads) != 0 || len(*events) != 0 {
		t.Fatalf("pads = %v, events = %v", etherpad.pads, *events)
	}
}

func TestGatewayReadsAnyPad(t *testing.T) {
	gateway, etherpad, _, _ := newTestGateway(t)
	p1 := "<html><body><h1>other/notes</h1></body></html>"
	etherpad.pads["p1"] = &fakePad{html: p1, revisions: 3, history: []string{"", p1, p1, p1}}

	status, payload := doJSON(t, http.MethodGet, gateway.URL+"/read?title=other%2Fnotes", nil)
	if status != http.StatusOK || payload["content"] != "other/notes\n" {
		t.Fatalf("read status = %d, payload = %v", status, payload)
	}
	status, payload = doJSON(t, http.MethodGet, gateway.URL+"/read?title=missing", nil)
	if status != http.StatusNotFound {
		t.Fatalf("missing read status = %d, payload = %v", status, payload)
	}
	status, payload = doJSON(t, http.MethodGet, gateway.URL+"/changes?title=other%2Fnotes&since=1", nil)
	if status != http.StatusOK || payload["authors"].(map[string]any)["a.human"] != "Human" || len(payload["added_lines"].([]any)) != 0 {
		t.Fatalf("changes status = %d, payload = %v", status, payload)
	}
	status, payload = doJSON(t, http.MethodGet, gateway.URL+"/changes?title=other%2Fnotes&since=3", nil)
	if status != http.StatusOK || payload["to_rev"].(float64) != 3 || len(payload["authors"].(map[string]any)) != 0 {
		t.Fatalf("no-op changes status = %d, payload = %v", status, payload)
	}
	status, payload = doJSON(t, http.MethodGet, gateway.URL+"/search?q=title%3Aother*", nil)
	if status != http.StatusOK || len(payload["docs"].([]any)) != 1 {
		t.Fatalf("search status = %d, payload = %v", status, payload)
	}
}

func TestGatewayReportsAuditFailureAfterWrite(t *testing.T) {
	lookupRetryDelay = time.Millisecond
	etherpad := &fakeEtherpad{pads: map[string]*fakePad{}}
	upstream := httptest.NewServer(etherpad.handler(t))
	defer upstream.Close()
	notes, err := Connect(HostConfig{URL: upstream.URL, ClientID: "id", ClientSecret: "secret"}, ProjectConfig{Prefix: "isotty"}, "m", "n")
	if err != nil {
		t.Fatal(err)
	}
	gateway := httptest.NewServer(Handler(notes, func(WriteEvent) error { return fmt.Errorf("ssh failed") }))
	defer gateway.Close()

	status, payload := doJSON(t, http.MethodPost, gateway.URL+"/create", map[string]string{"title": "isotty/x"})
	if status != http.StatusInternalServerError || !strings.Contains(payload["error"].(string), "was written") || payload["url"] == "" {
		t.Fatalf("status = %d, payload = %v", status, payload)
	}
	if len(etherpad.pads) != 1 {
		t.Fatalf("pads = %v", etherpad.pads)
	}
}
