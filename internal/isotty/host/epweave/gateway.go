package epweave

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
)

const maxRequestBody = 4 << 20

// WriteEvent describes a successful write so the caller can record it in the VM audit log.
type WriteEvent struct {
	Operation string
	Title     string
	PadID     string
}

type createRequest struct {
	Title    string `json:"title"`
	Markdown string `json:"markdown"`
	Tags     string `json:"tags"`
}

type appendRequest struct {
	Title    string `json:"title"`
	Markdown string `json:"markdown"`
}

// Handler serves the Notes operations; the host gateway mounts it under
// /epweave for the VM.
func Handler(notes *Notes, onWrite func(WriteEvent) error) http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /status", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"url": notes.URL(), "prefix": notes.Prefix()})
	})

	mux.HandleFunc("GET /search", func(w http.ResponseWriter, r *http.Request) {
		query := r.URL.Query().Get("q")
		if query == "" {
			writeError(w, http.StatusBadRequest, "q is required")
			return
		}
		rows := 20
		if raw := r.URL.Query().Get("rows"); raw != "" {
			parsed, err := strconv.Atoi(raw)
			if err != nil || parsed < 1 {
				writeError(w, http.StatusBadRequest, "rows must be a positive integer")
				return
			}
			rows = parsed
		}
		docs, err := notes.Search(query, rows)
		if err != nil {
			writeNotesError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"docs": docs})
	})

	mux.HandleFunc("GET /read", func(w http.ResponseWriter, r *http.Request) {
		title := r.URL.Query().Get("title")
		if title == "" {
			writeError(w, http.StatusBadRequest, "title is required")
			return
		}
		asHTML, ok := formatParam(w, r)
		if !ok {
			return
		}
		result, err := notes.Read(title, asHTML)
		if err != nil {
			writeNotesError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, result)
	})

	mux.HandleFunc("POST /create", func(w http.ResponseWriter, r *http.Request) {
		var req createRequest
		if !decodeBody(w, r, &req) {
			return
		}
		if req.Title == "" {
			writeError(w, http.StatusBadRequest, "title is required")
			return
		}
		result, err := notes.Create(req.Title, req.Markdown, req.Tags)
		if err != nil {
			writeNotesError(w, err)
			return
		}
		finishWrite(w, onWrite, WriteEvent{Operation: "create", Title: req.Title, PadID: result.PadID}, result)
	})

	mux.HandleFunc("POST /append", func(w http.ResponseWriter, r *http.Request) {
		var req appendRequest
		if !decodeBody(w, r, &req) {
			return
		}
		if req.Title == "" {
			writeError(w, http.StatusBadRequest, "title is required")
			return
		}
		result, err := notes.Append(req.Title, req.Markdown)
		if err != nil {
			writeNotesError(w, err)
			return
		}
		finishWrite(w, onWrite, WriteEvent{Operation: "append", Title: req.Title, PadID: result.PadID}, result)
	})

	mux.HandleFunc("GET /changes", func(w http.ResponseWriter, r *http.Request) {
		title := r.URL.Query().Get("title")
		if title == "" {
			writeError(w, http.StatusBadRequest, "title is required")
			return
		}
		since, err := strconv.Atoi(r.URL.Query().Get("since"))
		if err != nil || since < 0 {
			writeError(w, http.StatusBadRequest, "since must be a non-negative revision number")
			return
		}
		asHTML, ok := formatParam(w, r)
		if !ok {
			return
		}
		result, err := notes.Changes(title, since, asHTML)
		if err != nil {
			writeNotesError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, result)
	})

	return mux
}

// formatParam reads ?format=text|html; it answers the request itself when the value is invalid.
func formatParam(w http.ResponseWriter, r *http.Request) (asHTML bool, ok bool) {
	switch r.URL.Query().Get("format") {
	case "", "text":
		return false, true
	case "html":
		return true, true
	default:
		writeError(w, http.StatusBadRequest, "format must be text or html")
		return false, false
	}
}

func decodeBody(w http.ResponseWriter, r *http.Request, out any) bool {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxRequestBody))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(out); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return false
	}
	return true
}

// finishWrite reports the write even when recording it fails: the pad has
// already changed, so the agent must learn both facts.
func finishWrite(w http.ResponseWriter, onWrite func(WriteEvent) error, event WriteEvent, result WriteResult) {
	if err := onWrite(event); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{
			"error":  fmt.Sprintf("the note was written to %s but recording the audit event failed: %v", result.URL, err),
			"pad_id": result.PadID,
			"url":    result.URL,
		})
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func writeNotesError(w http.ResponseWriter, err error) {
	var prefixErr PrefixError
	switch {
	case errors.As(err, &prefixErr):
		writeError(w, http.StatusForbidden, err.Error())
	case errors.Is(err, ErrPadNotFound):
		writeError(w, http.StatusNotFound, err.Error())
	case errors.Is(err, ErrPadExists):
		writeError(w, http.StatusConflict, err.Error())
	default:
		writeError(w, http.StatusBadGateway, "ep_weave request failed: "+err.Error())
	}
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		panic(err)
	}
}
