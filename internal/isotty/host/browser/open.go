// Package browser lets the VM show a URL in the human's browser on the
// host: the `isotty-open` command in the VM, which is also its BROWSER,
// and the gateway service that runs the host's opener.
package browser

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/yazawa/isotty/internal/isotty/host/gateway"
)

const openTimeout = 5 * time.Second

// Handler answers POST /open. Any http or https URL is opened: the tab
// appears in front of the human, who is the one to judge it, and the open
// is recorded. Other schemes are refused because the host's opener would
// hand them to other applications. A request is answered as soon as the
// browser has been launched; opened runs after the response, so
// bookkeeping never delays or cancels the launch.
func Handler(open func(context.Context, string) error, opened func(url string)) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /open", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			URL string `json:"url"`
		}
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&req); err != nil {
			gateway.WriteError(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
			return
		}
		if !isWebURL(req.URL) {
			gateway.WriteError(w, http.StatusForbidden, "only http and https URLs are opened on the host; give the URL to the human instead")
			return
		}
		// The launch is deliberately independent of the request context: a
		// client that disconnects must not cancel a browser that is opening.
		if err := open(context.Background(), req.URL); err != nil {
			gateway.WriteError(w, http.StatusBadGateway, "opening the browser on the host failed: "+err.Error())
			return
		}
		w.WriteHeader(http.StatusNoContent)
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
		opened(req.URL)
	})
	return mux
}

func isWebURL(raw string) bool {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" {
		return false
	}
	scheme := strings.ToLower(parsed.Scheme)
	return scheme == "http" || scheme == "https"
}

// OpenInBrowser shows url with the host desktop's default opener.
func OpenInBrowser(ctx context.Context, url string) error {
	ctx, cancel := context.WithTimeout(ctx, openTimeout)
	defer cancel()
	var command *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		command = exec.CommandContext(ctx, "open", url)
	case "windows":
		command = exec.CommandContext(ctx, "rundll32", "url.dll,FileProtocolHandler", url)
	default:
		command = exec.CommandContext(ctx, "xdg-open", url)
	}
	output, err := command.CombinedOutput()
	if err != nil {
		if detail := strings.TrimSpace(string(output)); detail != "" {
			return fmt.Errorf("%s: %w: %s", command.Args[0], err, detail)
		}
		return fmt.Errorf("%s: %w", command.Args[0], err)
	}
	return nil
}
