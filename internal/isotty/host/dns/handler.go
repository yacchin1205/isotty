package dns

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/yazawa/isotty/internal/isotty/host/gateway"
)

const maxRequestBody = 1 << 20

// ChangeEvent describes a change the VM asked for, and how it ended, so the
// caller can record it in the VM audit log. Before and After are the record
// values as Record.Values writes them, empty when the record did not exist.
type ChangeEvent struct {
	Operation string
	Zone      string
	Name      string
	Type      string
	Before    string
	After     string
	Result    string
}

// Relay changes records for the VM within the pinned agreement.
type Relay struct {
	cfg         ProjectConfig
	client      *Client
	homeDir     string
	projectHash string
}

func NewRelay(cfg ProjectConfig, client *Client, homeDir, projectHash string) *Relay {
	return &Relay{cfg: cfg, client: client, homeDir: homeDir, projectHash: projectHash}
}

type setRequest struct {
	Name    string   `json:"name"`
	Type    string   `json:"type"`
	TTL     int      `json:"ttl"`
	Rrdatas []string `json:"rrdatas"`
}

type deleteRequest struct {
	Name string `json:"name"`
	Type string `json:"type"`
}

// Handler serves the VM; the host gateway mounts it under /dns.
func Handler(relay *Relay, onChange func(ChangeEvent) error) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /status", func(w http.ResponseWriter, _ *http.Request) {
		gateway.WriteJSON(w, http.StatusOK, map[string]any{"provider": relay.cfg.Provider, "zones": relay.cfg.Zones})
	})
	mux.HandleFunc("GET /list", func(w http.ResponseWriter, r *http.Request) {
		records, err := relay.list(r.URL.Query().Get("name"))
		if err != nil {
			writeError(w, err)
			return
		}
		gateway.WriteJSON(w, http.StatusOK, map[string]any{"records": records})
	})
	mux.HandleFunc("POST /set", func(w http.ResponseWriter, r *http.Request) {
		var req setRequest
		if !decodeBody(w, r, &req) {
			return
		}
		if req.TTL == 0 {
			req.TTL = 300
		}
		if req.Name == "" || req.Type == "" || req.TTL < 0 || len(req.Rrdatas) == 0 {
			gateway.WriteError(w, http.StatusBadRequest, "name, type and at least one rrdata are required; ttl must be positive")
			return
		}
		record := Record{Name: Normalize(req.Name), Type: strings.ToUpper(req.Type), TTL: req.TTL, Rrdatas: req.Rrdatas}
		result, event, err := relay.set(record)
		if recordErr := onChange(event); recordErr != nil {
			err = errors.Join(err, fmt.Errorf("recording the audit event failed: %w", recordErr))
		}
		if err != nil {
			writeError(w, err)
			return
		}
		gateway.WriteJSON(w, http.StatusOK, result)
	})
	mux.HandleFunc("POST /delete", func(w http.ResponseWriter, r *http.Request) {
		var req deleteRequest
		if !decodeBody(w, r, &req) {
			return
		}
		if req.Name == "" || req.Type == "" {
			gateway.WriteError(w, http.StatusBadRequest, "name and type are required")
			return
		}
		result, event, err := relay.delete(Normalize(req.Name), strings.ToUpper(req.Type))
		if recordErr := onChange(event); recordErr != nil {
			err = errors.Join(err, fmt.Errorf("recording the audit event failed: %w", recordErr))
		}
		if err != nil {
			writeError(w, err)
			return
		}
		gateway.WriteJSON(w, http.StatusOK, result)
	})
	return mux
}

// RefusedError reports a name outside the agreement; the credential is
// never used for it.
type RefusedError struct {
	Name string
	cfg  ProjectConfig
}

func (e *RefusedError) Error() string {
	return fmt.Sprintf("%s is outside what this project may change; pinned: %s", e.Name, e.cfg.summary())
}

var errNoRecord = errors.New("no such record")

// list returns the records under the pinned names, or under `name` when
// given, which must itself be under a pinned name.
func (r *Relay) list(name string) ([]Record, error) {
	records := []Record{}
	if name != "" {
		name = Normalize(name)
		zone, ok := r.cfg.Allows(name)
		if !ok {
			return nil, &RefusedError{Name: name, cfg: r.cfg}
		}
		found, err := r.client.List(zone.Project, zone.ManagedZone, name)
		if err != nil {
			return nil, err
		}
		return append(records, found...), nil
	}
	for _, zone := range r.cfg.Zones {
		for _, pinned := range zone.Names {
			found, err := r.client.List(zone.Project, zone.ManagedZone, pinned)
			if err != nil {
				return nil, err
			}
			records = append(records, found...)
		}
	}
	return records, nil
}

// set creates the record or replaces it in one change; the record it
// replaced is the event's Before. The event is returned for every outcome,
// a refusal included.
func (r *Relay) set(record Record) (map[string]any, ChangeEvent, error) {
	event := ChangeEvent{Operation: "set", Name: record.Name, Type: record.Type, After: record.Values()}
	zone, ok := r.cfg.Allows(record.Name)
	if !ok {
		event.Result = "refused"
		return nil, event, &RefusedError{Name: record.Name, cfg: r.cfg}
	}
	event.Zone = zone.Project + "/" + zone.ManagedZone
	before, err := r.current(zone, record.Name, record.Type)
	if err != nil {
		event.Result = err.Error()
		return nil, event, err
	}
	var deletions []Record
	if before != nil {
		event.Before = before.Values()
		deletions = []Record{*before}
	}
	status, err := r.client.Change(zone.Project, zone.ManagedZone, deletions, []Record{record})
	if err != nil {
		event.Result = err.Error()
		return nil, event, err
	}
	event.Result = status
	if before == nil {
		if err := Track(r.homeDir, r.projectHash, Tracked{Provider: r.cfg.Provider, Project: zone.Project, ManagedZone: zone.ManagedZone, Name: record.Name, Type: record.Type}); err != nil {
			return nil, event, fmt.Errorf("the record was written but noting it for cleanup failed: %w", err)
		}
	}
	return map[string]any{"record": record, "status": status}, event, nil
}

func (r *Relay) delete(name, rtype string) (map[string]any, ChangeEvent, error) {
	event := ChangeEvent{Operation: "delete", Name: name, Type: rtype}
	zone, ok := r.cfg.Allows(name)
	if !ok {
		event.Result = "refused"
		return nil, event, &RefusedError{Name: name, cfg: r.cfg}
	}
	event.Zone = zone.Project + "/" + zone.ManagedZone
	before, err := r.current(zone, name, rtype)
	if err != nil {
		event.Result = err.Error()
		return nil, event, err
	}
	if before == nil {
		event.Result = errNoRecord.Error()
		return nil, event, fmt.Errorf("%s %s: %w", rtype, name, errNoRecord)
	}
	event.Before = before.Values()
	status, err := r.client.Change(zone.Project, zone.ManagedZone, []Record{*before}, nil)
	if err != nil {
		event.Result = err.Error()
		return nil, event, err
	}
	event.Result = status
	if err := Untrack(r.homeDir, r.projectHash, Tracked{Provider: r.cfg.Provider, Project: zone.Project, ManagedZone: zone.ManagedZone, Name: name, Type: rtype}); err != nil {
		return nil, event, fmt.Errorf("the record was deleted but the cleanup note failed: %w", err)
	}
	return map[string]any{"record": before, "status": status}, event, nil
}

func (r *Relay) current(zone Zone, name, rtype string) (*Record, error) {
	records, err := r.client.List(zone.Project, zone.ManagedZone, name)
	if err != nil {
		return nil, err
	}
	for _, record := range records {
		if record.Name == name && record.Type == rtype {
			return &record, nil
		}
	}
	return nil, nil
}

func decodeBody(w http.ResponseWriter, r *http.Request, out any) bool {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxRequestBody))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(out); err != nil {
		gateway.WriteError(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return false
	}
	return true
}

func writeError(w http.ResponseWriter, err error) {
	var refused *RefusedError
	var api *APIError
	switch {
	case errors.As(err, &refused):
		gateway.WriteError(w, http.StatusForbidden, err.Error())
	case errors.Is(err, errNoRecord):
		gateway.WriteError(w, http.StatusNotFound, err.Error())
	case errors.As(err, &api):
		gateway.WriteJSON(w, http.StatusBadGateway, map[string]any{"error": map[string]any{"status": api.Status, "message": api.Message}})
	default:
		gateway.WriteError(w, http.StatusBadGateway, err.Error())
	}
}
