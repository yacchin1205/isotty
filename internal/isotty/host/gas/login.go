package gas

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"
)

// Login authorizes the client for the scopes in the human's browser, the
// way a desktop application does: the authorization code comes back to a
// loopback port and is exchanged for a refresh token. open shows the
// authorization URL to the human.
func Login(ctx context.Context, client Client, scopes []string, open func(url string) error) (Credential, error) {
	verifier := randomToken()
	challenge := sha256.Sum256([]byte(verifier))
	state := randomToken()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return Credential{}, fmt.Errorf("listen for the authorization code: %w", err)
	}
	defer listener.Close()
	redirectURI := "http://" + listener.Addr().String() + "/"

	type callback struct {
		code string
		err  error
	}
	received := make(chan callback, 1)
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		query := req.URL.Query()
		var result callback
		switch {
		case query.Get("state") != state:
			result.err = errors.New("the authorization response carried an unexpected state")
		case query.Get("error") != "":
			result.err = fmt.Errorf("authorization refused: %s", query.Get("error"))
		case query.Get("code") == "":
			result.err = errors.New("the authorization response carried no code")
		default:
			result.code = query.Get("code")
		}
		if result.err != nil {
			http.Error(w, result.err.Error(), http.StatusBadRequest)
		} else {
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			fmt.Fprintln(w, "isotty received the authorization. You may close this window.")
		}
		select {
		case received <- result:
		default:
		}
	}), ReadHeaderTimeout: 5 * time.Second}
	go server.Serve(listener)
	defer server.Close()

	authURL := client.AuthURI + "?" + url.Values{
		"client_id":             {client.ClientID},
		"redirect_uri":          {redirectURI},
		"response_type":         {"code"},
		"scope":                 {strings.Join(scopes, " ")},
		"access_type":           {"offline"},
		"prompt":                {"consent"},
		"state":                 {state},
		"code_challenge":        {base64.RawURLEncoding.EncodeToString(challenge[:])},
		"code_challenge_method": {"S256"},
	}.Encode()
	if err := open(authURL); err != nil {
		return Credential{}, err
	}

	var result callback
	select {
	case result = <-received:
	case <-ctx.Done():
		return Credential{}, fmt.Errorf("waiting for the authorization: %w", ctx.Err())
	}
	if result.err != nil {
		return Credential{}, result.err
	}

	granted, err := requestToken(&http.Client{Timeout: 30 * time.Second}, client.TokenURI, url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {result.code},
		"client_id":     {client.ClientID},
		"client_secret": {client.ClientSecret},
		"redirect_uri":  {redirectURI},
		"code_verifier": {verifier},
	})
	if err != nil {
		return Credential{}, err
	}
	if granted.RefreshToken == "" {
		return Credential{}, errors.New("Google issued no refresh token; the client must be a desktop application client")
	}
	account, err := emailOf(granted.IDToken)
	if err != nil {
		return Credential{}, err
	}
	grantedScopes := strings.Fields(granted.Scope)
	slices.Sort(grantedScopes)
	return Credential{Client: client, Account: account, RefreshToken: granted.RefreshToken, Scopes: grantedScopes}, nil
}

type tokenResponse struct {
	AccessToken  string `json:"access_token"`
	ExpiresIn    int    `json:"expires_in"`
	RefreshToken string `json:"refresh_token"`
	Scope        string `json:"scope"`
	IDToken      string `json:"id_token"`
}

func requestToken(client *http.Client, tokenURI string, form url.Values) (tokenResponse, error) {
	resp, err := client.PostForm(tokenURI, form)
	if err != nil {
		return tokenResponse{}, fmt.Errorf("request token from %s: %w", tokenURI, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return tokenResponse{}, fmt.Errorf("read token response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return tokenResponse{}, fmt.Errorf("%s answered %d: %s", tokenURI, resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var granted tokenResponse
	if err := json.Unmarshal(body, &granted); err != nil {
		return tokenResponse{}, fmt.Errorf("parse token response: %w", err)
	}
	if granted.AccessToken == "" || granted.ExpiresIn <= 0 {
		return tokenResponse{}, fmt.Errorf("token response lacks access_token or expires_in: %s", strings.TrimSpace(string(body)))
	}
	return granted, nil
}

// emailOf reads the account out of the ID token. The token came straight
// from Google's token endpoint over TLS, so its signature is not checked.
func emailOf(idToken string) (string, error) {
	parts := strings.Split(idToken, ".")
	if len(parts) != 3 {
		return "", errors.New("token response carries no ID token; the login must ask for the openid and email scopes")
	}
	payload, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(parts[1], "="))
	if err != nil {
		return "", fmt.Errorf("decode ID token: %w", err)
	}
	var claims struct {
		Email string `json:"email"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil {
		return "", fmt.Errorf("parse ID token: %w", err)
	}
	if claims.Email == "" {
		return "", errors.New("ID token carries no email")
	}
	return claims.Email, nil
}

func randomToken() string {
	var buf [32]byte
	if _, err := rand.Read(buf[:]); err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(buf[:])
}
