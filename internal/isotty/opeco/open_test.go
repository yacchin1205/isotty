package opeco

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

func TestOpenHandlerOpensOnlyAllowedURLs(t *testing.T) {
	var opened, recorded []string
	handler := OpenHandler(61002,
		func(_ context.Context, url string) error { opened = append(opened, url); return nil },
		func(url string) { recorded = append(recorded, url) },
	)
	server := httptest.NewServer(handler)
	defer server.Close()

	status, _ := postOpen(t, server, `{"url":"http://127.0.0.1:61002/qr/abc"}`)
	if status != http.StatusNoContent {
		t.Fatalf("allowed status = %d", status)
	}
	for _, body := range []string{
		`{"url":"http://127.0.0.1:61003/qr/abc"}`,
		`{"url":"http://127.0.0.1:610020/"}`,
		`{"url":"https://example.com/"}`,
		`{"url":"file:///etc/passwd"}`,
	} {
		status, text := postOpen(t, server, body)
		if status != http.StatusForbidden || !strings.Contains(text, "only URLs under") {
			t.Fatalf("%s: status = %d, body = %s", body, status, text)
		}
	}
	if status, _ := postOpen(t, server, `{"url":"http://127.0.0.1:61002/","extra":1}`); status != http.StatusBadRequest {
		t.Fatalf("unknown field status = %d", status)
	}
	if len(opened) != 1 || len(recorded) != 1 || opened[0] != "http://127.0.0.1:61002/qr/abc" {
		t.Fatalf("opened = %v, recorded = %v", opened, recorded)
	}
}

func TestOpenHandlerReportsBrowserFailure(t *testing.T) {
	recorded := false
	handler := OpenHandler(61002,
		func(context.Context, string) error { return errors.New("no display") },
		func(string) { recorded = true },
	)
	server := httptest.NewServer(handler)
	defer server.Close()
	status, text := postOpen(t, server, `{"url":"http://127.0.0.1:61002/qr/abc"}`)
	if status != http.StatusBadGateway || !strings.Contains(text, "no display") {
		t.Fatalf("status = %d, body = %s", status, text)
	}
	if recorded {
		t.Fatal("a failed launch must not be recorded as opened")
	}
}

func TestOpenHandlerAnswersBeforeBookkeeping(t *testing.T) {
	bookkeeping := make(chan struct{})
	handler := OpenHandler(61002,
		func(context.Context, string) error { return nil },
		func(string) { <-bookkeeping },
	)
	server := httptest.NewServer(handler)
	defer server.Close()

	done := make(chan int, 1)
	go func() {
		status, _ := postOpen(t, server, `{"url":"http://127.0.0.1:61002/qr/abc"}`)
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
