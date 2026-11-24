package google

import (
	"bytes"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/yazawa/isotty/internal/isotty/host/gcloud"
)

// Issuer turns the service account into access tokens for the pinned
// scopes, in one of two ways: with its key, the way Google's client
// libraries do, a JWT signed with the key being exchanged at the token
// endpoint; or by impersonation, asking the IAM Credentials API with the
// human's gcloud login. A token is reused until it is about to expire, so
// the VM asking for one per command costs nothing.
type Issuer struct {
	cfg    ProjectConfig
	client *http.Client
	now    func() time.Time
	mint   func() (string, time.Duration, error)

	// With a key.
	key    Key
	signer *rsa.PrivateKey

	// By impersonation.
	iamEndpoint string
	humanToken  func() (string, error)

	mu      sync.Mutex
	token   string
	expires time.Time
}

// Token is what the VM receives.
type Token struct {
	AccessToken    string    `json:"access_token"`
	ExpiresAt      time.Time `json:"expires_at"`
	ServiceAccount string    `json:"service_account"`
	Scopes         []string  `json:"scopes"`
}

const tokenReuseMargin = 5 * time.Minute

func NewIssuer(key Key, cfg ProjectConfig) (*Issuer, error) {
	block, _ := pem.Decode([]byte(key.PrivateKey))
	if block == nil {
		return nil, errors.New("service account private_key is not PEM")
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parse service account private_key: %w", err)
	}
	signer, ok := parsed.(*rsa.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("service account private_key is %T, not RSA", parsed)
	}
	i := &Issuer{key: key, cfg: cfg, signer: signer, client: &http.Client{Timeout: 30 * time.Second}, now: time.Now}
	i.mint = i.mintWithKey
	return i, nil
}

const iamCredentialsEndpoint = "https://iamcredentials.googleapis.com/v1"

// NewImpersonatingIssuer issues tokens of the service account with the host's
// active gcloud account, which must hold
// roles/iam.serviceAccountTokenCreator on it.
func NewImpersonatingIssuer(cfg ProjectConfig) *Issuer {
	return newImpersonatingIssuer(cfg, iamCredentialsEndpoint, gcloud.AccessToken)
}

func newImpersonatingIssuer(cfg ProjectConfig, endpoint string, humanToken func() (string, error)) *Issuer {
	i := &Issuer{cfg: cfg, client: &http.Client{Timeout: 30 * time.Second}, now: time.Now, iamEndpoint: endpoint, humanToken: humanToken}
	i.mint = i.mintByImpersonation
	return i
}

// LoadIssuer returns the issuer the pinned credential asks for.
func LoadIssuer(homeDir string, cfg ProjectConfig) (*Issuer, error) {
	if cfg.CredentialKind() == CredentialImpersonation {
		return NewImpersonatingIssuer(cfg), nil
	}
	key, err := LoadKey(homeDir, cfg.ServiceAccount)
	if err != nil {
		return nil, err
	}
	return NewIssuer(key, cfg)
}

// RecordError is a token that was minted but could not be recorded; it is
// neither kept nor handed out.
type RecordError struct{ Err error }

func (e RecordError) Error() string {
	return "a token was minted but recording the audit event failed: " + e.Err.Error()
}

// Issue returns a token, minting one when none is fresh. A minted token is
// recorded first, under the lock, and kept for reuse only once recorded, so
// no request, a retry or a concurrent one, gets a token the log lacks.
func (i *Issuer) Issue(record func(Token) error) (Token, error) {
	i.mu.Lock()
	defer i.mu.Unlock()
	if i.token != "" && i.now().Add(tokenReuseMargin).Before(i.expires) {
		return i.current(), nil
	}
	access, expiresIn, err := i.mint()
	if err != nil {
		return Token{}, err
	}
	token := Token{AccessToken: access, ExpiresAt: i.now().Add(expiresIn), ServiceAccount: i.cfg.ServiceAccount, Scopes: i.cfg.Scopes}
	if err := record(token); err != nil {
		return Token{}, RecordError{err}
	}
	i.token, i.expires = token.AccessToken, token.ExpiresAt
	return token, nil
}

func (i *Issuer) current() Token {
	return Token{AccessToken: i.token, ExpiresAt: i.expires, ServiceAccount: i.cfg.ServiceAccount, Scopes: i.cfg.Scopes}
}

func (i *Issuer) mintByImpersonation() (string, time.Duration, error) {
	human, err := i.humanToken()
	if err != nil {
		return "", 0, err
	}
	body, err := json.Marshal(map[string]any{"scope": i.cfg.ScopeURLs(), "lifetime": "3600s"})
	if err != nil {
		return "", 0, err
	}
	endpoint := i.iamEndpoint + "/projects/-/serviceAccounts/" + url.PathEscape(i.cfg.ServiceAccount) + ":generateAccessToken"
	req, err := http.NewRequest(http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return "", 0, err
	}
	req.Header.Set("Authorization", "Bearer "+human)
	req.Header.Set("Content-Type", "application/json")
	resp, err := i.client.Do(req)
	if err != nil {
		return "", 0, fmt.Errorf("impersonate %s: %w", i.cfg.ServiceAccount, err)
	}
	defer resp.Body.Close()
	answer, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", 0, fmt.Errorf("read impersonation response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return "", 0, fmt.Errorf("impersonating %s was refused (%d): %s", i.cfg.ServiceAccount, resp.StatusCode, strings.TrimSpace(string(answer)))
	}
	var granted struct {
		AccessToken string    `json:"accessToken"`
		ExpireTime  time.Time `json:"expireTime"`
	}
	if err := json.Unmarshal(answer, &granted); err != nil {
		return "", 0, fmt.Errorf("parse impersonation response: %w", err)
	}
	lifetime := granted.ExpireTime.Sub(i.now())
	if granted.AccessToken == "" || lifetime <= 0 {
		// The body is left out: it may hold the token.
		return "", 0, errors.New("impersonation response lacks accessToken or a future expireTime")
	}
	return granted.AccessToken, lifetime, nil
}

func (i *Issuer) mintWithKey() (string, time.Duration, error) {
	assertion, err := i.assertion()
	if err != nil {
		return "", 0, err
	}
	form := url.Values{
		"grant_type": {"urn:ietf:params:oauth:grant-type:jwt-bearer"},
		"assertion":  {assertion},
	}
	resp, err := i.client.PostForm(i.key.TokenURI, form)
	if err != nil {
		return "", 0, fmt.Errorf("request token from %s: %w", i.key.TokenURI, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", 0, fmt.Errorf("read token response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return "", 0, fmt.Errorf("%s answered %d: %s", i.key.TokenURI, resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var granted struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err := json.Unmarshal(body, &granted); err != nil {
		return "", 0, fmt.Errorf("parse token response: %w", err)
	}
	if granted.AccessToken == "" || granted.ExpiresIn <= 0 {
		return "", 0, fmt.Errorf("token response lacks access_token or expires_in: %s", strings.TrimSpace(string(body)))
	}
	return granted.AccessToken, time.Duration(granted.ExpiresIn) * time.Second, nil
}

func (i *Issuer) assertion() (string, error) {
	now := i.now()
	header, _ := json.Marshal(map[string]string{"alg": "RS256", "typ": "JWT"})
	claims, _ := json.Marshal(map[string]any{
		"iss":   i.key.ClientEmail,
		"scope": strings.Join(i.cfg.ScopeURLs(), " "),
		"aud":   i.key.TokenURI,
		"iat":   now.Unix(),
		"exp":   now.Add(time.Hour).Unix(),
	})
	signingInput := base64.RawURLEncoding.EncodeToString(header) + "." + base64.RawURLEncoding.EncodeToString(claims)
	digest := sha256.Sum256([]byte(signingInput))
	signature, err := rsa.SignPKCS1v15(rand.Reader, i.signer, crypto.SHA256, digest[:])
	if err != nil {
		return "", fmt.Errorf("sign token request: %w", err)
	}
	return signingInput + "." + base64.RawURLEncoding.EncodeToString(signature), nil
}
