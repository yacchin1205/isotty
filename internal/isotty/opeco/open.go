package opeco

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/yazawa/isotty/internal/isotty/host/gateway"
)

const openTimeout = 5 * time.Second

// OpenHandler is the gateway service that lets the VM ask the host to show
// opeco's QR page in the host's browser. Only URLs under the project's QR
// port are accepted: the VM must not be able to point the person's browser
// anywhere else. A request is answered as soon as the browser has been
// launched; opened runs after the response, so bookkeeping never delays or
// cancels the launch.
func OpenHandler(qrPort int, open func(context.Context, string) error, opened func(url string)) http.Handler {
	allowedPrefix := fmt.Sprintf("http://127.0.0.1:%d/", qrPort)
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
		if !strings.HasPrefix(req.URL, allowedPrefix) {
			gateway.WriteError(w, http.StatusForbidden, fmt.Sprintf("only URLs under %s can be opened on the host", allowedPrefix))
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
