package wake

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/yazawa/isotty/internal/isotty/host/gateway"
)

// Handler serves the VM's side: POST /wakes keeps a wake, GET /wakes lists
// them. A wake is an agent's own request, so it is kept as asked; the human
// agreed to wakes at all by running `isotty schedule run`.
// record is told of each wake kept (wake-request) and cancelled
// (wake-cancel).
func Handler(homeDir, projectHash string, record func(kind string, r Request) error) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /wakes", func(w http.ResponseWriter, r *http.Request) {
		enabled, err := Enabled(homeDir, projectHash)
		if err != nil {
			gateway.WriteError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if !enabled {
			gateway.WriteError(w, http.StatusForbidden, "this project does not take wakes: starting its VM is the human's to allow, with `isotty schedule enable` on the host")
			return
		}
		var body struct {
			Agent   string `json:"agent"`
			Session string `json:"session"`
			After   int    `json:"after_seconds"`
			Cron    string `json:"cron"`
			Prompt  string `json:"prompt"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			gateway.WriteError(w, http.StatusBadRequest, err.Error())
			return
		}
		at := time.Now().Add(time.Duration(body.After) * time.Second).Truncate(time.Second)
		switch {
		case body.Cron != "" && body.After != 0:
			gateway.WriteError(w, http.StatusBadRequest, "a wake has after_seconds or cron, not both")
			return
		case body.Cron != "":
			cron, err := ParseCron(body.Cron)
			if err != nil {
				gateway.WriteError(w, http.StatusBadRequest, err.Error())
				return
			}
			at = cron.Next(time.Now())
		case body.After <= 0:
			gateway.WriteError(w, http.StatusBadRequest, "after_seconds must be positive")
			return
		}
		request, err := Add(homeDir, projectHash, Request{Agent: body.Agent, Session: body.Session, Prompt: body.Prompt, Cron: body.Cron, At: at})
		if err != nil {
			gateway.WriteError(w, http.StatusBadRequest, err.Error())
			return
		}
		if err := record("wake-request", request); err != nil {
			gateway.WriteError(w, http.StatusInternalServerError, "the wake is kept but recording it failed: "+err.Error())
			return
		}
		gateway.WriteJSON(w, http.StatusOK, request)
	})
	mux.HandleFunc("DELETE /wakes/{id}", func(w http.ResponseWriter, r *http.Request) {
		removed, found, err := Remove(homeDir, projectHash, r.PathValue("id"))
		if err != nil {
			gateway.WriteError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if !found {
			gateway.WriteError(w, http.StatusNotFound, "no such wake")
			return
		}
		if err := record("wake-cancel", removed); err != nil {
			gateway.WriteError(w, http.StatusInternalServerError, "the wake is cancelled but recording it failed: "+err.Error())
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("GET /wakes", func(w http.ResponseWriter, r *http.Request) {
		requests, err := Load(homeDir, projectHash)
		if err != nil {
			gateway.WriteError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if requests == nil {
			requests = []Request{}
		}
		gateway.WriteJSON(w, http.StatusOK, requests)
	})
	return mux
}
