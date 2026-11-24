package google

import (
	"errors"
	"net/http"

	"github.com/yazawa/isotty/internal/isotty/host/gateway"
)

// MintEvent describes a token the gateway minted so the caller can record it
// in the VM audit log.
type MintEvent struct {
	ServiceAccount string
	Scopes         []string
}

// Handler serves the token to the VM; the host gateway mounts it under /google.
func Handler(issuer *Issuer, onMint func(MintEvent) error) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /status", func(w http.ResponseWriter, _ *http.Request) {
		gateway.WriteJSON(w, http.StatusOK, map[string]any{"service_account": issuer.cfg.ServiceAccount, "scopes": issuer.cfg.Scopes})
	})
	mux.HandleFunc("GET /token", func(w http.ResponseWriter, _ *http.Request) {
		token, err := issuer.Issue(func(t Token) error {
			return onMint(MintEvent{ServiceAccount: t.ServiceAccount, Scopes: t.Scopes})
		})
		var notRecorded RecordError
		switch {
		case errors.As(err, &notRecorded):
			gateway.WriteError(w, http.StatusInternalServerError, err.Error())
		case err != nil:
			gateway.WriteError(w, http.StatusBadGateway, "Google token request failed: "+err.Error())
		default:
			gateway.WriteJSON(w, http.StatusOK, token)
		}
	})
	return mux
}
