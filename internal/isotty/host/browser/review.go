package browser

import (
	"bufio"
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/yazawa/isotty/internal/isotty/host/gateway"
)

// reviewsDir is where a project keeps the human's comments on its files,
// relative to the project: under .isotty, which the sync carries into the
// VM, so the agent reads them as files and no new channel is needed.
const reviewsDir = ".isotty/reviews"

// Comment is one remark the human made on a file in the viewer. The lines
// or the spot it refers to are those of the file as it was then: the hash
// says whether the file has changed since, and the quoted line lets the
// agent find the place again when it has.
type Comment struct {
	ID         string    `json:"id"`
	At         time.Time `json:"at"`
	Anchor     Anchor    `json:"anchor"`
	Body       string    `json:"body"`
	FileSHA256 string    `json:"file_sha256"`
	Resolved   bool      `json:"resolved"`
}

// Anchor is where in the file a comment sits: lines of a text or of a
// Markdown page's source, with the text at the first of them quoted; a
// point or a rectangle on an image, in fractions of its size; or nothing,
// for the file as a whole.
type Anchor struct {
	Lines []int     `json:"lines,omitempty"`
	Quote string    `json:"quote,omitempty"`
	Point []float64 `json:"point,omitempty"`
	Rect  []float64 `json:"rect,omitempty"`
}

func (a Anchor) valid() error {
	switch {
	case len(a.Lines) == 2 && a.Lines[0] >= 1 && a.Lines[1] >= a.Lines[0] && a.Point == nil && a.Rect == nil:
	case len(a.Point) == 2 && a.Lines == nil && a.Quote == "" && a.Rect == nil && unit(a.Point):
	case len(a.Rect) == 4 && a.Lines == nil && a.Quote == "" && a.Point == nil && unit(a.Rect):
	case a.Lines == nil && a.Quote == "" && a.Point == nil && a.Rect == nil:
	default:
		return errors.New("an anchor is two lines with the first quoted, a point or a rectangle in fractions of the image, or nothing")
	}
	return nil
}

func unit(values []float64) bool {
	for _, v := range values {
		if v < 0 || v > 1 {
			return false
		}
	}
	return true
}

// reviewsPath is the file holding the comments on rel, a real path under
// the real project directory. The host alone makes what is on the way to
// it, so a symlink there is the VM's, planted to have the host write
// somewhere else; the path is refused rather than resolved, since there is
// nowhere else the comments belong.
func reviewsPath(project, rel string) (string, error) {
	at := project
	steps := strings.Split(path.Join(reviewsDir, rel)+".jsonl", "/")
	for i, step := range steps {
		at = filepath.Join(at, step)
		info, err := os.Lstat(at)
		if errors.Is(err, fs.ErrNotExist) {
			// What is not there yet, writeComments makes, as the host's own.
			return filepath.Join(project, filepath.FromSlash(reviewsDir), filepath.FromSlash(rel)+".jsonl"), nil
		}
		if err != nil {
			return "", err
		}
		if last := i == len(steps)-1; last && !info.Mode().IsRegular() || !last && !info.Mode().IsDir() {
			return "", fmt.Errorf("%s was not made by the host; the comments are kept only in what it made", at)
		}
	}
	return at, nil
}

// readComments returns the comments in the file, none when there is no file.
func readComments(path string) ([]Comment, error) {
	file, err := os.Open(path)
	if errors.Is(err, fs.ErrNotExist) {
		return []Comment{}, nil
	}
	if err != nil {
		return nil, err
	}
	defer file.Close()
	comments := []Comment{}
	scanner := bufio.NewScanner(file)
	scanner.Buffer(nil, 1<<20)
	for scanner.Scan() {
		if len(scanner.Bytes()) == 0 {
			continue
		}
		var comment Comment
		if err := json.Unmarshal(scanner.Bytes(), &comment); err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		comments = append(comments, comment)
	}
	return comments, scanner.Err()
}

// writeComments replaces the file with the comments, one per line, in one
// step; an empty list removes the file.
func writeComments(path string, comments []Comment) error {
	if len(comments) == 0 {
		err := os.Remove(path)
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	// The file is for the agent to read, so a "<" in a quote stays a "<".
	var out bytes.Buffer
	encoder := json.NewEncoder(&out)
	encoder.SetEscapeHTML(false)
	for _, comment := range comments {
		if err := encoder.Encode(comment); err != nil {
			return err
		}
	}
	incoming, err := os.CreateTemp(filepath.Dir(path), ".incoming-")
	if err != nil {
		return err
	}
	_, err = incoming.Write(out.Bytes())
	if closeErr := incoming.Close(); err == nil {
		err = closeErr
	}
	if err == nil {
		err = os.Rename(incoming.Name(), path)
	}
	if err != nil {
		return errors.Join(err, os.Remove(incoming.Name()))
	}
	return nil
}

func fileSHA256(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}
	return "sha256:" + hex.EncodeToString(hash.Sum(nil)), nil
}

func newID() (string, error) {
	raw := make([]byte, 8)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return hex.EncodeToString(raw), nil
}

// reviews answers the viewer about the comments on one file: GET lists
// them, POST adds one, PATCH changes one's text or resolved mark, DELETE
// removes one. The file must be one the server would show, located again
// here, and the comments live beside the project under reviewsDir.
func (f *Files) reviews(w http.ResponseWriter, r *http.Request) {
	if r.Host != f.listener.Addr().String() {
		http.NotFound(w, r)
		return
	}
	hostPath, rel, err := f.locate(r.PathValue("path"))
	if err != nil || f.ignored(rel) {
		http.NotFound(w, r)
		return
	}
	if info, err := os.Stat(hostPath); err != nil || !info.Mode().IsRegular() {
		http.NotFound(w, r)
		return
	}
	project, err := f.realProject()
	if err != nil {
		gateway.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	path, err := reviewsPath(project, rel)
	if err != nil {
		gateway.WriteError(w, http.StatusForbidden, err.Error())
		return
	}
	comments, err := readComments(path)
	if err != nil {
		gateway.WriteError(w, http.StatusInternalServerError, "reading the comments failed: "+err.Error())
		return
	}
	if r.Method == http.MethodGet {
		// The file's hash of now, for the viewer to tell which comments
		// were made on an earlier version.
		sum, err := fileSHA256(hostPath)
		if err != nil {
			gateway.WriteError(w, http.StatusInternalServerError, "reading the file failed: "+err.Error())
			return
		}
		gateway.WriteJSON(w, http.StatusOK, map[string]any{"file_sha256": sum, "comments": comments})
		return
	}

	var change struct {
		ID       string  `json:"id"`
		Anchor   *Anchor `json:"anchor"`
		Body     *string `json:"body"`
		Resolved *bool   `json:"resolved"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&change); err != nil {
		gateway.WriteError(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return
	}
	if change.Body != nil && *change.Body == "" {
		gateway.WriteError(w, http.StatusBadRequest, "a comment has a body")
		return
	}
	var answer Comment
	switch r.Method {
	case http.MethodPost:
		if change.Anchor == nil || change.Body == nil {
			gateway.WriteError(w, http.StatusBadRequest, "a new comment has an anchor and a body")
			return
		}
		if err := change.Anchor.valid(); err != nil {
			gateway.WriteError(w, http.StatusBadRequest, err.Error())
			return
		}
		sum, err := fileSHA256(hostPath)
		if err != nil {
			gateway.WriteError(w, http.StatusInternalServerError, "reading the file failed: "+err.Error())
			return
		}
		id, err := newID()
		if err != nil {
			gateway.WriteError(w, http.StatusInternalServerError, err.Error())
			return
		}
		answer = Comment{ID: id, At: time.Now().UTC().Truncate(time.Second), Anchor: *change.Anchor, Body: *change.Body, FileSHA256: sum}
		comments = append(comments, answer)
	case http.MethodPatch, http.MethodDelete:
		at := -1
		for i, comment := range comments {
			if comment.ID == change.ID {
				at = i
			}
		}
		if at < 0 {
			gateway.WriteError(w, http.StatusNotFound, "no such comment")
			return
		}
		if r.Method == http.MethodDelete {
			answer = comments[at]
			comments = append(comments[:at], comments[at+1:]...)
			break
		}
		if change.Body != nil {
			comments[at].Body = *change.Body
		}
		if change.Resolved != nil {
			comments[at].Resolved = *change.Resolved
		}
		answer = comments[at]
	}
	if err := writeComments(path, comments); err != nil {
		gateway.WriteError(w, http.StatusInternalServerError, "writing the comments failed: "+err.Error())
		return
	}
	gateway.WriteJSON(w, http.StatusOK, answer)
}
