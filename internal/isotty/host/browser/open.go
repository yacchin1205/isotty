// Package browser lets the VM show a URL in the human's browser on the
// host: the `isotty-open` command in the VM, which is also its BROWSER,
// and the gateway service that runs the host's opener.
package browser

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/yazawa/isotty/internal/isotty/host/gateway"
)

const openTimeout = 5 * time.Second

// contentSecurityPolicy is sent with every served file. sandbox gives the
// document an opaque origin, which makes its own page's images foreign to
// a Cross-Origin-Resource-Policy, so the policy itself names what the
// page may load: only the kinds that cannot run, and only from here.
const contentSecurityPolicy = "sandbox; default-src 'none'; img-src 'self'; style-src 'self' 'unsafe-inline'; font-src 'self'; media-src 'self'"

// Files shows files of the VM's workspace to the human: the workspace is
// synced to a project directory on the host, and a path under it is served
// from that copy on a loopback port only the host reaches, so the human's
// browser renders the file and nothing hands it to another application.
// It is nil when the attach has no project on the host (`attach --target`).
type Files struct {
	// Workspace is the workspace path in the VM.
	Workspace string
	// Project is the project directory on the host.
	Project string
	// Flush waits for the sync to carry the VM's writes to the host.
	Flush func() error
	// Ignored says whether the sync keeps a project-relative path on the
	// host; such a file is not on the VM's side and is not shown. Nil
	// ignores nothing.
	Ignored func(rel string) bool

	listener net.Listener
	token    string
}

// Serve starts the loopback file server; Close stops it with the attach.
func (f *Files) Serve() error {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return fmt.Errorf("listen for the file server: %w", err)
	}
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		listener.Close()
		return err
	}
	f.listener, f.token = listener, hex.EncodeToString(raw)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /"+f.token+"/{path...}", f.serve)
	go func() { _ = http.Serve(listener, mux) }()
	return nil
}

func (f *Files) Close() error {
	if f == nil || f.listener == nil {
		return nil
	}
	return f.listener.Close()
}

// url is where the browser finds the file at rel, a slash path under the
// project. The token keeps the URL unguessable by pages in that browser.
func (f *Files) url(rel string) string {
	var escaped []string
	for _, segment := range strings.Split(rel, "/") {
		escaped = append(escaped, url.PathEscape(segment))
	}
	return fmt.Sprintf("http://%s/%s/%s", f.listener.Addr(), f.token, strings.Join(escaped, "/"))
}

// serve answers the browser. The file is located again, so what is served
// is under the project at that moment; it is served from the descriptor
// that was checked to be a regular file, so nothing is resolved after the
// check. The response headers keep a VM-made page inert: no script runs,
// the document has no origin to read the server with, it loads nothing
// but the images, styles, fonts and media this server holds, so the page
// reaches no other site, and nothing is sniffed into HTML. The Host check
// keeps a page that rebinds a name to the loopback from reaching it.
func (f *Files) serve(w http.ResponseWriter, r *http.Request) {
	if r.Host != f.listener.Addr().String() {
		http.NotFound(w, r)
		return
	}
	hostPath, rel, err := f.locate(r.PathValue("path"))
	if err != nil || f.ignored(rel) {
		http.NotFound(w, r)
		return
	}
	file, err := os.Open(hostPath)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Security-Policy", contentSecurityPolicy)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if isMarkdown(rel) {
		renderMarkdown(w, path.Base(rel), file)
		return
	}
	http.ServeContent(w, r, path.Base(rel), info.ModTime(), file)
}

func (f *Files) ignored(rel string) bool {
	return f.Ignored != nil && f.Ignored(rel)
}

// own reports whether a URL points at this file server, which the VM may
// not open by url: only a path, which is checked, leads there. The port
// alone decides, since browsers spell the loopback in more ways than are
// worth listing and the port is a random one of this attach.
func (f *Files) own(raw string) bool {
	if f == nil || f.listener == nil {
		return false
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return false
	}
	_, port, _ := net.SplitHostPort(f.listener.Addr().String())
	return parsed.Port() == port
}

// resolve turns a path in the VM into the file on the host, after the sync
// has delivered it, and answers with the URL to open and the file's real
// path as the VM would name it, for the record. The path must be under the
// workspace as the VM names it, and the file under the project as the host
// finds it: a symlink that leads out of the project is refused, since the
// VM may have made it, and a file the sync keeps on the host is not there
// for the VM. Errors are phrased for the VM and name no host path.
func (f *Files) resolve(vmPath string) (string, string, int, error) {
	if f == nil {
		return "", "", http.StatusForbidden, errors.New("this attach has no project directory on the host, so files are not opened; give the path to the human instead")
	}
	cleaned := path.Clean(vmPath)
	if !path.IsAbs(cleaned) || (cleaned != f.Workspace && !strings.HasPrefix(cleaned, f.Workspace+"/")) {
		return "", "", http.StatusForbidden, fmt.Errorf("only files under %s are opened on the host; give the path to the human instead", f.Workspace)
	}
	if err := f.Flush(); err != nil {
		return "", "", http.StatusBadGateway, fmt.Errorf("waiting for the sync to carry %s to the host failed: %w", vmPath, err)
	}
	notOnHost := fmt.Errorf("%s is not on the host; the sync keeps secrets, keys and the like out of it", vmPath)
	if f.ignored(strings.TrimPrefix(cleaned, f.Workspace+"/")) {
		return "", "", http.StatusNotFound, notOnHost
	}
	hostPath, rel, err := f.locate(strings.TrimPrefix(cleaned, f.Workspace))
	if err != nil {
		if os.IsNotExist(err) {
			return "", "", http.StatusNotFound, notOnHost
		}
		if errors.Is(err, errOutside) {
			return "", "", http.StatusForbidden, fmt.Errorf("%s leads outside the project on the host and is not opened", vmPath)
		}
		return "", "", http.StatusBadGateway, fmt.Errorf("%s could not be resolved on the host", vmPath)
	}
	if f.ignored(rel) {
		return "", "", http.StatusNotFound, notOnHost
	}
	info, err := os.Stat(hostPath)
	if err != nil || !info.Mode().IsRegular() {
		return "", "", http.StatusForbidden, fmt.Errorf("%s is not a file; only files are opened", vmPath)
	}
	return f.url(rel), f.Workspace + "/" + rel, 0, nil
}

var errOutside = errors.New("outside the project")

// locate resolves rel, a slash path under the project, to the real file on
// the host and its real path relative to the real project directory.
func (f *Files) locate(rel string) (string, string, error) {
	project, err := filepath.EvalSymlinks(f.Project)
	if err != nil {
		return "", "", err
	}
	hostPath, err := filepath.EvalSymlinks(filepath.Join(project, filepath.FromSlash(rel)))
	if err != nil {
		return "", "", err
	}
	if hostPath != project && !strings.HasPrefix(hostPath, project+string(filepath.Separator)) {
		return "", "", errOutside
	}
	realRel, err := filepath.Rel(project, hostPath)
	if err != nil {
		return "", "", err
	}
	return hostPath, filepath.ToSlash(realRel), nil
}

// Handler answers POST /open with a url or a path. Any http or https URL
// is opened: the tab appears in front of the human, who is the one to
// judge it, and the open is recorded. Other schemes are refused because the
// host's opener would hand them to other applications. A path under the
// VM's workspace is opened, once the sync has carried it over, as the URL
// where files serves the host's copy, so it too lands in the browser. A
// request is answered as soon as the opener has been launched; opened runs
// after the response with what was shown, the URL or the file's real path
// as the VM names it, so bookkeeping never delays or cancels the launch.
func Handler(open func(context.Context, string) error, opened func(target string), files *Files) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /open", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			URL  string `json:"url"`
			Path string `json:"path"`
		}
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&req); err != nil {
			gateway.WriteError(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
			return
		}
		target, shown := req.URL, req.URL
		if req.Path != "" {
			if req.URL != "" {
				gateway.WriteError(w, http.StatusBadRequest, "give a url or a path, not both")
				return
			}
			var status int
			var err error
			target, shown, status, err = files.resolve(req.Path)
			if err != nil {
				gateway.WriteError(w, status, err.Error())
				return
			}
		} else if !isWebURL(req.URL) {
			gateway.WriteError(w, http.StatusForbidden, "only http and https URLs are opened on the host; give the URL to the human instead")
			return
		} else if files.own(req.URL) {
			gateway.WriteError(w, http.StatusForbidden, "that is the attach's own file server; open a file by its path instead")
			return
		}
		// The launch is deliberately independent of the request context: a
		// client that disconnects must not cancel a browser that is opening.
		if err := open(context.Background(), target); err != nil {
			gateway.WriteError(w, http.StatusBadGateway, "opening the browser on the host failed: "+err.Error())
			return
		}
		w.WriteHeader(http.StatusNoContent)
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
		opened(shown)
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
