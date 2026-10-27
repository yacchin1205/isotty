package epweave

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

const apiVersion = "1.3.1"

// lookupRetryDelay paces the title lookup; the search index is updated
// asynchronously after a write. Tests shorten it.
var lookupRetryDelay = time.Second

// Reindexing a pad right after a write has been observed to take more than
// five seconds, so a lookup keeps asking for up to ten.
const lookupAttempts = 10

var searchEscapePattern = regexp.MustCompile(`([\s/():"\\])`)

// Client speaks the Etherpad HTTP API plus ep_weave's search endpoint.
type Client struct {
	URL  string
	cfg  HostConfig
	http *http.Client

	mu    sync.Mutex
	token string
}

type SearchDoc struct {
	ID    string `json:"id"`
	Title string `json:"title"`
}

func NewClient(cfg HostConfig) (*Client, error) {
	client := &Client{
		URL:  strings.TrimRight(cfg.URL, "/"),
		cfg:  cfg,
		http: &http.Client{Timeout: 30 * time.Second},
	}
	if err := client.renewToken(); err != nil {
		return nil, fmt.Errorf("obtain ep_weave token from %s: %w", client.URL, err)
	}
	return client, nil
}

func (c *Client) renewToken() error {
	token, err := c.fetchToken()
	if err != nil {
		return err
	}
	c.mu.Lock()
	c.token = token
	c.mu.Unlock()
	return nil
}

func (c *Client) currentToken() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.token
}

func (c *Client) fetchToken() (string, error) {
	form := url.Values{
		"grant_type":    {"client_credentials"},
		"client_id":     {c.cfg.ClientID},
		"client_secret": {c.cfg.ClientSecret},
		// Without `resource` the provider issues an opaque token that the API rejects.
		"resource": {c.URL},
	}
	req, err := http.NewRequest(http.MethodPost, c.URL+"/oidc/token", strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	body, err := c.do(req)
	if err != nil {
		return "", err
	}
	var payload struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return "", fmt.Errorf("decode token response: %w", err)
	}
	if payload.AccessToken == "" {
		return "", fmt.Errorf("token response has no access_token: %s", strings.TrimSpace(string(body)))
	}
	return payload.AccessToken, nil
}

// api calls an Etherpad API method. The token expires after an hour while
// an attach lasts as long as the human stays, so a 401 is answered with a
// new token and one more try.
func (c *Client) api(method string, params url.Values) (json.RawMessage, error) {
	resp, body, err := c.post(method, params)
	if err == nil && resp.StatusCode == http.StatusUnauthorized {
		if err := c.renewToken(); err != nil {
			return nil, fmt.Errorf("%s: renew ep_weave token: %w", method, err)
		}
		resp, body, err = c.post(method, params)
	}
	if err != nil {
		return nil, fmt.Errorf("%s: %w", method, err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s: %s: %s", method, resp.Status, strings.TrimSpace(string(body)))
	}
	var payload struct {
		Code    int             `json:"code"`
		Message string          `json:"message"`
		Data    json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("%s: decode response: %w", method, err)
	}
	if payload.Code != 0 {
		return nil, fmt.Errorf("%s: %s", method, payload.Message)
	}
	return payload.Data, nil
}

func (c *Client) apiInto(method string, params url.Values, out any) error {
	data, err := c.api(method, params)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("%s: decode data: %w", method, err)
	}
	return nil
}

func (c *Client) Search(query string, rows int) ([]SearchDoc, error) {
	q := url.Values{
		"query": {query},
		"sort":  {"indexed desc"},
		"start": {"0"},
		"rows":  {strconv.Itoa(rows)},
	}
	req, err := http.NewRequest(http.MethodGet, c.URL+"/search/?"+q.Encode(), nil)
	if err != nil {
		return nil, err
	}
	body, err := c.do(req)
	if err != nil {
		return nil, fmt.Errorf("search: %w", err)
	}
	var payload struct {
		Docs []SearchDoc `json:"docs"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("search: decode response: %w", err)
	}
	return payload.Docs, nil
}

func (c *Client) post(method string, params url.Values) (*http.Response, []byte, error) {
	req, err := http.NewRequest(http.MethodPost, fmt.Sprintf("%s/api/%s/%s", c.URL, apiVersion, method), strings.NewReader(params.Encode()))
	if err != nil {
		return nil, nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.currentToken())
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return c.send(req)
}

// do sends the request and returns the body of a 200 response; any other
// status is an error carrying the body text.
func (c *Client) do(req *http.Request) ([]byte, error) {
	resp, body, err := c.send(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s: %s", resp.Status, strings.TrimSpace(string(body)))
	}
	return body, nil
}

func (c *Client) send(req *http.Request) (*http.Response, []byte, error) {
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, nil, fmt.Errorf("read response: %w", err)
	}
	return resp, body, nil
}

// PadIDByTitle resolves a title through the search index. GET /t/<title>
// would create a missing pad, so it is never used.
func (c *Client) PadIDByTitle(title string) (string, bool, error) {
	escaped := searchEscapePattern.ReplaceAllString(title, `\$1`)
	for attempt := 0; attempt < lookupAttempts; attempt++ {
		if attempt > 0 {
			time.Sleep(lookupRetryDelay)
		}
		docs, err := c.Search("title:"+escaped, 20)
		if err != nil {
			return "", false, err
		}
		for _, doc := range docs {
			if doc.Title == title {
				return doc.ID, true, nil
			}
		}
	}
	return "", false, nil
}

func (c *Client) CreateAuthorIfNotExistsFor(mapper, name string) (string, error) {
	var data struct {
		AuthorID string `json:"authorID"`
	}
	if err := c.apiInto("createAuthorIfNotExistsFor", url.Values{"authorMapper": {mapper}, "name": {name}}, &data); err != nil {
		return "", err
	}
	return data.AuthorID, nil
}

func (c *Client) RevisionsCount(padID string) (int, error) {
	var data struct {
		Revisions int `json:"revisions"`
	}
	if err := c.apiInto("getRevisionsCount", url.Values{"padID": {padID}}, &data); err != nil {
		return 0, err
	}
	return data.Revisions, nil
}

func (c *Client) Text(padID string) (string, error) {
	var data struct {
		Text string `json:"text"`
	}
	if err := c.apiInto("getText", url.Values{"padID": {padID}}, &data); err != nil {
		return "", err
	}
	return data.Text, nil
}

func (c *Client) HTML(padID string) (string, error) {
	return c.html(url.Values{"padID": {padID}})
}

// HTMLAt is used for revision comparison because getText renders list items
// differently at the head and at an older revision, while getHTML is stable.
func (c *Client) HTMLAt(padID string, rev int) (string, error) {
	return c.html(url.Values{"padID": {padID}, "rev": {strconv.Itoa(rev)}})
}

func (c *Client) html(params url.Values) (string, error) {
	var data struct {
		HTML string `json:"html"`
	}
	if err := c.apiInto("getHTML", params, &data); err != nil {
		return "", err
	}
	return data.HTML, nil
}

func (c *Client) CreatePad(padID, authorID string) error {
	_, err := c.api("createPad", url.Values{"padID": {padID}, "authorId": {authorID}})
	return err
}

func (c *Client) SetHTML(padID, html, authorID string) error {
	_, err := c.api("setHTML", url.Values{"padID": {padID}, "html": {html}, "authorId": {authorID}})
	return err
}

func (c *Client) DiffHTML(padID string, startRev, endRev int) (string, []string, error) {
	var data struct {
		HTML    string   `json:"html"`
		Authors []string `json:"authors"`
	}
	params := url.Values{"padID": {padID}, "startRev": {strconv.Itoa(startRev)}, "endRev": {strconv.Itoa(endRev)}}
	if err := c.apiInto("createDiffHTML", params, &data); err != nil {
		return "", nil, err
	}
	return data.HTML, data.Authors, nil
}

func (c *Client) AuthorName(authorID string) (string, error) {
	var name string
	if err := c.apiInto("getAuthorName", url.Values{"authorID": {authorID}}, &name); err != nil {
		return "", err
	}
	return name, nil
}
