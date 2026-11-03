package dns

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os/exec"
	"strings"
	"sync"
	"time"
)

const (
	apiUpstream = "https://dns.googleapis.com"
	// tokenLifetime is how long a gcloud access token is reused; gcloud
	// issues them for an hour.
	tokenLifetime = 50 * time.Minute
)

// Record is one resource record set as the VM and the audit log see it: the
// name without the trailing dot Cloud DNS uses.
type Record struct {
	Name    string   `json:"name"`
	Type    string   `json:"type"`
	TTL     int      `json:"ttl"`
	Rrdatas []string `json:"rrdatas"`
}

// Values is the record's data as the audit log writes it.
func (r Record) Values() string {
	return fmt.Sprintf("%d %s", r.TTL, strings.Join(r.Rrdatas, " "))
}

// Client calls the Cloud DNS API with the host's gcloud login: the token
// of the active gcloud account, renewed before gcloud's hour runs out. No
// key is kept for it.
type Client struct {
	http     *http.Client
	upstream string
	token    func() (string, error)

	mu      sync.Mutex
	cached  string
	expires time.Time
}

func NewClient() *Client {
	return newClient(apiUpstream, gcloudToken)
}

func newClient(upstream string, token func() (string, error)) *Client {
	return &Client{http: &http.Client{Timeout: 30 * time.Second}, upstream: upstream, token: token}
}

func gcloudToken() (string, error) {
	out, err := exec.Command("gcloud", "auth", "print-access-token").Output()
	if err != nil {
		var stderr string
		if exitErr, ok := err.(*exec.ExitError); ok {
			stderr = strings.TrimSpace(string(exitErr.Stderr))
		}
		return "", fmt.Errorf("gcloud auth print-access-token: %w %s", err, stderr)
	}
	return strings.TrimSpace(string(out)), nil
}

func (c *Client) bearer(renew bool) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if renew || c.cached == "" || time.Now().After(c.expires) {
		token, err := c.token()
		if err != nil {
			return "", err
		}
		c.cached = token
		c.expires = time.Now().Add(tokenLifetime)
	}
	return c.cached, nil
}

// ManagedZone is a zone of a project as `host dns add` sees it: its name
// and the domain it serves, without the trailing dot.
type ManagedZone struct {
	Name       string
	DNSName    string
	Visibility string
}

// ManagedZones lists the project's zones; it is how `host dns add` checks
// the login reaches the project and finds the zone that serves a name.
func (c *Client) ManagedZones(project string) ([]ManagedZone, error) {
	var zones []ManagedZone
	pageToken := ""
	for {
		query := url.Values{}
		if pageToken != "" {
			query.Set("pageToken", pageToken)
		}
		var payload struct {
			ManagedZones []struct {
				Name       string `json:"name"`
				DNSName    string `json:"dnsName"`
				Visibility string `json:"visibility"`
			} `json:"managedZones"`
			NextPageToken string `json:"nextPageToken"`
		}
		if err := c.call(http.MethodGet, fmt.Sprintf("/dns/v1/projects/%s/managedZones?%s", project, query.Encode()), nil, &payload); err != nil {
			return nil, err
		}
		for _, zone := range payload.ManagedZones {
			zones = append(zones, ManagedZone{Name: zone.Name, DNSName: Normalize(zone.DNSName), Visibility: zone.Visibility})
		}
		if payload.NextPageToken == "" {
			return zones, nil
		}
		pageToken = payload.NextPageToken
	}
}

// ChooseZone picks the zone that serves a name: the one whose domain is the
// longest suffix of it. Two zones with the same domain, a public and a
// private one say, need the human to name one.
func ChooseZone(zones []ManagedZone, name, managedZone string) (ManagedZone, error) {
	name = Normalize(name)
	if managedZone != "" {
		for _, zone := range zones {
			if zone.Name == managedZone {
				if name != zone.DNSName && !strings.HasSuffix(name, "."+zone.DNSName) {
					return ManagedZone{}, fmt.Errorf("%s is not in managed zone %s, which serves %s", name, zone.Name, zone.DNSName)
				}
				return zone, nil
			}
		}
		return ManagedZone{}, fmt.Errorf("managed zone %s does not exist in the project", managedZone)
	}
	var candidates []ManagedZone
	for _, zone := range zones {
		if name != zone.DNSName && !strings.HasSuffix(name, "."+zone.DNSName) {
			continue
		}
		if len(candidates) > 0 && len(zone.DNSName) < len(candidates[0].DNSName) {
			continue
		}
		if len(candidates) > 0 && len(zone.DNSName) > len(candidates[0].DNSName) {
			candidates = nil
		}
		candidates = append(candidates, zone)
	}
	switch len(candidates) {
	case 0:
		return ManagedZone{}, fmt.Errorf("no managed zone in the project serves %s", name)
	case 1:
		return candidates[0], nil
	}
	var names []string
	for _, zone := range candidates {
		names = append(names, fmt.Sprintf("%s (%s)", zone.Name, zone.Visibility))
	}
	return ManagedZone{}, fmt.Errorf("%s is served by more than one managed zone, %s; name one with --managed-zone", name, strings.Join(names, " and "))
}

// List returns the zone's record sets whose name is `name` or under it.
// The API filters by exact name only, so the zone is read whole.
func (c *Client) List(project, zone, name string) ([]Record, error) {
	name = Normalize(name)
	var records []Record
	pageToken := ""
	for {
		query := url.Values{}
		if pageToken != "" {
			query.Set("pageToken", pageToken)
		}
		var payload struct {
			Rrsets []struct {
				Name    string   `json:"name"`
				Type    string   `json:"type"`
				TTL     int      `json:"ttl"`
				Rrdatas []string `json:"rrdatas"`
			} `json:"rrsets"`
			NextPageToken string `json:"nextPageToken"`
		}
		path := fmt.Sprintf("/dns/v1/projects/%s/managedZones/%s/rrsets?%s", project, zone, query.Encode())
		if err := c.call(http.MethodGet, path, nil, &payload); err != nil {
			return nil, err
		}
		for _, rrset := range payload.Rrsets {
			record := Record{Name: Normalize(rrset.Name), Type: rrset.Type, TTL: rrset.TTL, Rrdatas: rrset.Rrdatas}
			if record.Name == name || strings.HasSuffix(record.Name, "."+name) {
				records = append(records, record)
			}
		}
		if payload.NextPageToken == "" {
			return records, nil
		}
		pageToken = payload.NextPageToken
	}
}

// Change applies deletions and additions as one change and returns its
// status, `done` or `pending`.
func (c *Client) Change(project, zone string, deletions, additions []Record) (string, error) {
	body := map[string]any{"deletions": apiRecords(deletions), "additions": apiRecords(additions)}
	var payload struct {
		Status string `json:"status"`
	}
	if err := c.call(http.MethodPost, fmt.Sprintf("/dns/v1/projects/%s/managedZones/%s/changes", project, zone), body, &payload); err != nil {
		return "", err
	}
	return payload.Status, nil
}

func apiRecords(records []Record) []map[string]any {
	out := make([]map[string]any, 0, len(records))
	for _, record := range records {
		out = append(out, map[string]any{"name": record.Name + ".", "type": record.Type, "ttl": record.TTL, "rrdatas": record.Rrdatas})
	}
	return out
}

// APIError is Cloud DNS's refusal, relayed to the VM as it came.
type APIError struct {
	Status  int
	Message string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("Cloud DNS answered %d: %s", e.Status, e.Message)
}

// call sends one request. gcloud hands out a token it cached itself, so
// the hour the client assumes may be mostly spent already; a 401 is
// answered with a fresh token and one more try.
func (c *Client) call(method, path string, body any, out any) error {
	var payload []byte
	if body != nil {
		var err error
		if payload, err = json.Marshal(body); err != nil {
			return err
		}
	}
	resp, data, err := c.send(method, path, payload, false)
	if err == nil && resp.StatusCode == http.StatusUnauthorized {
		resp, data, err = c.send(method, path, payload, true)
	}
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		var failure struct {
			Error struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		message := strings.TrimSpace(string(data))
		if json.Unmarshal(data, &failure) == nil && failure.Error.Message != "" {
			message = failure.Error.Message
		}
		return &APIError{Status: resp.StatusCode, Message: message}
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("decode Cloud DNS response: %w", err)
	}
	return nil
}

func (c *Client) send(method, path string, payload []byte, renew bool) (*http.Response, []byte, error) {
	var reader io.Reader
	if payload != nil {
		reader = bytes.NewReader(payload)
	}
	req, err := http.NewRequest(method, c.upstream+path, reader)
	if err != nil {
		return nil, nil, err
	}
	token, err := c.bearer(renew)
	if err != nil {
		return nil, nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, nil, fmt.Errorf("read Cloud DNS response: %w", err)
	}
	return resp, data, nil
}
