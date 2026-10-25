package gateway

import (
	"fmt"
	"net/http"
	"testing"
)

func TestServeAndClose(t *testing.T) {
	gateway, err := Serve(http.NotFoundHandler())
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/", gateway.Port()))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	if err := gateway.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
}
