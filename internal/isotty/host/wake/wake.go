// Package wake keeps, on the host, the times an agent in the VM asked to be
// woken at, so that `isotty schedule run` can start the VM and resume the
// agent's conversation when the time comes, even after the VM powered
// itself off in between.
package wake

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// Request is one wake: the conversation to resume and what to tell it.
// A recurring one has Cron, and At is its next time.
type Request struct {
	ID      string    `json:"id"`
	Agent   string    `json:"agent"`
	Session string    `json:"session"`
	At      time.Time `json:"at"`
	Cron    string    `json:"cron,omitempty"`
	Prompt  string    `json:"prompt"`
	// Failures counts the tries of a one-time wake that could not deliver
	// it, so one that never can is given up instead of starting the VM
	// every few minutes for ever.
	Failures int `json:"failures,omitempty"`
}

// MaxPrompt bounds a wake's prompt, which goes to the VM in one command.
const MaxPrompt = 8 << 10

// MaxFailures is how many tries a one-time wake gets.
const MaxFailures = 3

// storeMu keeps the reads and writes of the wakes of this process in turn:
// the gateway adds a wake while a delivery moves another on.
var storeMu sync.Mutex

func (r Request) validate() error {
	if r.Agent != "claude" && r.Agent != "codex" {
		return fmt.Errorf("agent %q cannot be woken; claude and codex can", r.Agent)
	}
	if r.Session == "" || r.Prompt == "" || r.At.IsZero() {
		return errors.New("a wake needs a session, a time and a prompt")
	}
	if len(r.Prompt) > MaxPrompt {
		return fmt.Errorf("a wake's prompt is at most %d bytes", MaxPrompt)
	}
	if r.Cron != "" {
		if _, err := ParseCron(r.Cron); err != nil {
			return err
		}
	}
	return nil
}

// Path is where the project's wakes are kept, never synced: the VM asks
// the host through the gateway, and only the host acts on them.
func Path(homeDir, projectHash string) string {
	return filepath.Join(homeDir, "projects", projectHash+".wakes.json")
}

func Load(homeDir, projectHash string) ([]Request, error) {
	data, err := os.ReadFile(Path(homeDir, projectHash))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var requests []Request
	if err := json.Unmarshal(data, &requests); err != nil {
		return nil, fmt.Errorf("parse %s: %w", Path(homeDir, projectHash), err)
	}
	return requests, nil
}

func save(homeDir, projectHash string, requests []Request) error {
	sort.Slice(requests, func(i, j int) bool { return requests[i].At.Before(requests[j].At) })
	data, err := json.MarshalIndent(requests, "", "  ")
	if err != nil {
		return err
	}
	path := Path(homeDir, projectHash)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	next := path + ".next"
	if err := os.WriteFile(next, data, 0o600); err != nil {
		return err
	}
	return os.Rename(next, path)
}

// Add keeps a new wake and returns it with its id.
func Add(homeDir, projectHash string, request Request) (Request, error) {
	if err := request.validate(); err != nil {
		return Request{}, err
	}
	id := make([]byte, 6)
	if _, err := rand.Read(id); err != nil {
		return Request{}, err
	}
	request.ID = hex.EncodeToString(id)
	storeMu.Lock()
	defer storeMu.Unlock()
	requests, err := Load(homeDir, projectHash)
	if err != nil {
		return Request{}, err
	}
	return request, save(homeDir, projectHash, append(requests, request))
}

// update changes the stored copy of one wake, or removes it when change
// says so.
func update(homeDir, projectHash, id string, change func(*Request) (keep bool)) error {
	storeMu.Lock()
	defer storeMu.Unlock()
	requests, err := Load(homeDir, projectHash)
	if err != nil {
		return err
	}
	kept := requests[:0]
	for _, r := range requests {
		if r.ID == id && !change(&r) {
			continue
		}
		kept = append(kept, r)
	}
	return save(homeDir, projectHash, kept)
}

// Done is what follows a wake's time: a recurring one moves to its next
// time after now, which drops the times it missed; a one-time one is
// forgotten.
func Done(homeDir, projectHash string, request Request, now time.Time) error {
	if request.Cron == "" {
		_, _, err := Remove(homeDir, projectHash, request.ID)
		return err
	}
	cron, err := ParseCron(request.Cron)
	if err != nil {
		return err
	}
	return update(homeDir, projectHash, request.ID, func(r *Request) bool {
		r.At = cron.Next(now)
		return true
	})
}

// Postpone moves a wake to at, as when its conversation still had work.
func Postpone(homeDir, projectHash, id string, at time.Time) error {
	return update(homeDir, projectHash, id, func(r *Request) bool {
		r.At = at
		return true
	})
}

// Failed counts a try that could not deliver a one-time wake and moves it
// to retry, or forgets it once it had MaxFailures; it tells which.
func Failed(homeDir, projectHash, id string, retry time.Time) (dropped bool, err error) {
	err = update(homeDir, projectHash, id, func(r *Request) bool {
		r.Failures++
		r.At = retry
		dropped = r.Failures >= MaxFailures
		return !dropped
	})
	return dropped, err
}

// Remove forgets a wake, and returns it.
func Remove(homeDir, projectHash, id string) (Request, bool, error) {
	var removed Request
	found := false
	err := update(homeDir, projectHash, id, func(r *Request) bool {
		removed, found = *r, true
		return false
	})
	return removed, found, err
}

// Forget removes every wake of the project, as `down` does: the
// conversations they would resume went with the VM.
func Forget(homeDir, projectHash string) error {
	storeMu.Lock()
	defer storeMu.Unlock()
	if err := os.Remove(Path(homeDir, projectHash)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}
