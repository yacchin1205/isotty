package browser

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
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
