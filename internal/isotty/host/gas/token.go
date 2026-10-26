package gas

import (
	"net/http"
	"net/url"
	"sync"
	"time"
)

// Refresher turns the refresh token into access tokens and reuses each
// until it is about to expire.
type Refresher struct {
	cred   Credential
	client *http.Client
	now    func() time.Time

	mu      sync.Mutex
	token   string
	expires time.Time
}

// A run may last six minutes and fails with an authorization error if the
// token expires meanwhile, so a token with less than that left is replaced.
const tokenReuseMargin = 7 * time.Minute

func NewRefresher(cred Credential) *Refresher {
	return &Refresher{cred: cred, client: &http.Client{Timeout: 30 * time.Second}, now: time.Now}
}

func (r *Refresher) Access() (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.token != "" && r.now().Add(tokenReuseMargin).Before(r.expires) {
		return r.token, nil
	}
	granted, err := requestToken(r.client, r.cred.TokenURI, url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {r.cred.RefreshToken},
		"client_id":     {r.cred.ClientID},
		"client_secret": {r.cred.ClientSecret},
	})
	if err != nil {
		return "", err
	}
	r.token = granted.AccessToken
	r.expires = r.now().Add(time.Duration(granted.ExpiresIn) * time.Second)
	return r.token, nil
}
