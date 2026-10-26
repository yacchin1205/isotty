package google

import (
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
)

// Issuer turns the service account key into access tokens for the pinned
// scopes, the way Google's client libraries do: a JWT signed with the key is
// exchanged at the token endpoint. A token is reused until it is about to
// expire, so the VM asking for one per command costs nothing.
type Issuer struct {
	key    Key
	cfg    ProjectConfig
	signer *rsa.PrivateKey
	client *http.Client
	now    func() time.Time

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
	return &Issuer{key: key, cfg: cfg, signer: signer, client: &http.Client{Timeout: 30 * time.Second}, now: time.Now}, nil
}

// Issue returns a token and whether it was minted by this call, so the
// caller records mints and not reuses.
func (i *Issuer) Issue() (Token, bool, error) {
	i.mu.Lock()
	defer i.mu.Unlock()
	if i.token != "" && i.now().Add(tokenReuseMargin).Before(i.expires) {
		return i.current(), false, nil
	}
	token, expiresIn, err := i.mint()
	if err != nil {
		return Token{}, false, err
	}
	i.token = token
	i.expires = i.now().Add(expiresIn)
	return i.current(), true, nil
}

func (i *Issuer) current() Token {
	return Token{AccessToken: i.token, ExpiresAt: i.expires, ServiceAccount: i.key.ClientEmail, Scopes: i.cfg.Scopes}
}

func (i *Issuer) mint() (string, time.Duration, error) {
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
