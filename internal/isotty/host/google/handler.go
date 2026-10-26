package google

import (
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
		gateway.WriteJSON(w, http.StatusOK, map[string]any{"service_account": issuer.key.ClientEmail, "scopes": issuer.cfg.Scopes})
	})
	mux.HandleFunc("GET /token", func(w http.ResponseWriter, _ *http.Request) {
		token, minted, err := issuer.Issue()
		if err != nil {
			gateway.WriteError(w, http.StatusBadGateway, "Google token request failed: "+err.Error())
			return
		}
		// A mint the host could not record is not handed out: the VM has
		// not used it yet, so failing here loses nothing but the attempt.
		if minted {
			if err := onMint(MintEvent{ServiceAccount: token.ServiceAccount, Scopes: token.Scopes}); err != nil {
				gateway.WriteError(w, http.StatusInternalServerError, "a token was minted but recording the audit event failed: "+err.Error())
				return
			}
		}
		gateway.WriteJSON(w, http.StatusOK, token)
	})
	return mux
}
