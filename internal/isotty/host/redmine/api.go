package redmine

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// maxAnswer bounds what is read from Redmine for one JSON answer.
const maxAnswer = 16 << 20

// api is the host's view of the Redmine: every call carries the key.
type api struct {
	url    string
	key    string
	client *http.Client
}

func newAPI(cfg HostConfig) *api {
	return &api{url: cfg.URL, key: cfg.APIKey, client: &http.Client{Timeout: 60 * time.Second}}
}

// get fetches a JSON path such as /projects/infra.json with query params.
func (a *api) get(path string, params url.Values) ([]byte, int, error) {
	target := a.url + path
	if len(params) > 0 {
		target += "?" + params.Encode()
	}
	req, err := http.NewRequest(http.MethodGet, target, nil)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-Redmine-API-Key", a.key)
	resp, err := a.client.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxAnswer))
	if err != nil {
		return nil, 0, fmt.Errorf("read response: %w", err)
	}
	return body, resp.StatusCode, nil
}

// Project is what the relay keeps of a pinned project.
type Project struct {
	ID         int    `json:"id"`
	Identifier string `json:"identifier"`
	Name       string `json:"name"`
}

// Verify asks the Redmine for the project with the host key and returns
// it as Redmine spells it. Redmine answers 403 for a project the key may
// not see and 404 for one that does not exist.
func Verify(cfg HostConfig, identifier string) (Project, error) {
	return newAPI(cfg).project(identifier)
}

func (a *api) project(identifier string) (Project, error) {
	body, status, err := a.get("/projects/"+url.PathEscape(identifier)+".json", nil)
	if err != nil {
		return Project{}, fmt.Errorf("project %s: %w", identifier, err)
	}
	switch status {
	case http.StatusOK:
	case http.StatusForbidden, http.StatusNotFound:
		return Project{}, fmt.Errorf("project %s does not exist or is not visible to the key in ~/.isotty/redmine.yaml (%d)", identifier, status)
	case http.StatusUnauthorized:
		return Project{}, fmt.Errorf("the Redmine rejected the key in ~/.isotty/redmine.yaml (%d): %s", status, strings.TrimSpace(string(body)))
	default:
		return Project{}, fmt.Errorf("project %s: the Redmine answered %d: %s", identifier, status, strings.TrimSpace(string(body)))
	}
	var answer struct {
		Project Project `json:"project"`
	}
	if err := json.Unmarshal(body, &answer); err != nil || answer.Project.ID == 0 {
		return Project{}, fmt.Errorf("project %s: answer names no project: %s", identifier, strings.TrimSpace(string(body)))
	}
	return answer.Project, nil
}

// WhoAmI asks the Redmine whose key this is, which proves the key before
// it is written down.
func WhoAmI(cfg HostConfig) (string, error) {
	body, status, err := newAPI(cfg).get("/users/current.json", nil)
	if err != nil {
		return "", fmt.Errorf("%s: %w", cfg.URL, err)
	}
	if status != http.StatusOK {
		return "", fmt.Errorf("%s answered %d to the key: %s", cfg.URL, status, strings.TrimSpace(string(body)))
	}
	var answer struct {
		User struct {
			Login     string `json:"login"`
			Firstname string `json:"firstname"`
			Lastname  string `json:"lastname"`
		} `json:"user"`
	}
	if err := json.Unmarshal(body, &answer); err != nil || answer.User.Login == "" {
		return "", fmt.Errorf("%s: /users/current.json names no user: %s", cfg.URL, strings.TrimSpace(string(body)))
	}
	return answer.User.Login, nil
}

// download fetches an attachment's content with the key. Redmine serves
// attachments from the same host, under /attachments/download. The errors
// leave out the URL, which carries the file name.
func (a *api) download(contentURL string, limit int64) ([]byte, error) {
	if !strings.HasPrefix(contentURL, a.url+"/attachments/download/") {
		return nil, fmt.Errorf("its URL is not under %s/attachments/download/", a.url)
	}
	req, err := http.NewRequest(http.MethodGet, contentURL, nil)
	if err != nil {
		return nil, withoutURL(err)
	}
	req.Header.Set("X-Redmine-API-Key", a.key)
	resp, err := (&http.Client{Timeout: 10 * time.Minute}).Do(req)
	if err != nil {
		return nil, withoutURL(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("the Redmine answered %d to the attachment", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, fmt.Errorf("read attachment: %w", err)
	}
	if int64(len(body)) > limit {
		return nil, fmt.Errorf("the attachment is larger than %d bytes", limit)
	}
	return body, nil
}

func withoutURL(err error) error {
	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		return fmt.Errorf("%s: %w", urlErr.Op, urlErr.Err)
	}
	return err
}
