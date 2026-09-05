package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/lettermint/lettermint-cli/internal/api"
	"github.com/lettermint/lettermint-cli/internal/config"
	"io"
	"net"
	"net/http"
	"net/url"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

func random() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(b)
}
func Challenge(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}
func Exchange(ctx context.Context, httpClient *http.Client, base string, form url.Values) (config.Credentials, error) {
	var c config.Credentials
	if err := api.ValidateBase(base); err != nil {
		return c, err
	}
	req, err := http.NewRequestWithContext(ctx, "POST", strings.TrimRight(base, "/")+"/oauth/token", strings.NewReader(form.Encode()))
	if err != nil {
		return c, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	resp, err := httpClient.Do(req)
	if err != nil {
		return c, fmt.Errorf("OAuth request failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		failure := &api.Error{Status: resp.StatusCode, Code: "oauth_failed", Message: "OAuth request was rejected"}
		switch {
		case resp.StatusCode == http.StatusTooManyRequests:
			failure.Code = "rate_limited"
			failure.Message = "OAuth requests are rate limited; retry later"
		case resp.StatusCode >= 500:
			failure.Code = "oauth_unavailable"
			failure.Message = "The OAuth service is unavailable; retry later"
		default:
			var wire struct {
				Error string `json:"error"`
			}
			_ = json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&wire)
			if (resp.StatusCode == 400 || resp.StatusCode == 401) && wire.Error == "invalid_grant" {
				failure.Status = http.StatusUnauthorized
				failure.Code = "oauth_grant_expired"
				failure.Message = "The login has expired or was revoked; use auth logout --local for this profile, then auth login"
			}
		}
		return c, failure
	}
	var token struct {
		Access  string `json:"access_token"`
		Refresh string `json:"refresh_token"`
		Expires int64  `json:"expires_in"`
		Type    string `json:"token_type"`
	}
	if err = json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&token); err != nil {
		return c, errors.New("OAuth returned invalid JSON")
	}
	if token.Access == "" || token.Refresh == "" || token.Expires <= 0 || !strings.EqualFold(token.Type, "Bearer") {
		return c, errors.New("OAuth returned an incomplete token")
	}
	return config.Credentials{AccessToken: token.Access, RefreshToken: token.Refresh, ExpiresAt: time.Now().Add(time.Duration(token.Expires) * time.Second)}, nil
}
func TokenSource(s *config.Store, name string, p config.Profile, httpClient *http.Client) func(context.Context) (string, error) {
	return func(ctx context.Context) (string, error) {
		unlock, err := s.Lock(ctx, "profile-"+name)
		if err != nil {
			return "", err
		}
		defer unlock()
		c, err := s.Vault.Get(name)
		if err != nil {
			return "", fmt.Errorf("credential store is unavailable or this profile has no grant: %w", err)
		}
		if p.ConnectionID == "" || c.ConnectionID != p.ConnectionID {
			return "", errors.New("the saved grant changed; start a new command with the intended profile")
		}
		if time.Until(c.ExpiresAt) > time.Minute {
			return c.AccessToken, nil
		}
		c, err = Exchange(ctx, httpClient, p.APIURL, url.Values{"grant_type": {"refresh_token"}, "client_id": {p.ClientID}, "refresh_token": {c.RefreshToken}})
		if err != nil {
			return "", err
		}
		c.ConnectionID = p.ConnectionID
		if err = s.Vault.Set(name, c); err != nil {
			return "", fmt.Errorf("cannot save rotated credentials: %w", err)
		}
		return c.AccessToken, nil
	}
}
func Callback(state string, result chan<- url.Values) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Security-Policy", "default-src 'none'")
		if r.Method != "GET" || r.URL.Path != "/callback" {
			http.Error(w, "Not found", 404)
			return
		}
		q := r.URL.Query()
		if len(q["state"]) != 1 || subtle.ConstantTimeCompare([]byte(q.Get("state")), []byte(state)) != 1 {
			http.Error(w, "Invalid state", 400)
			return
		}
		if (q.Get("code") == "") == (q.Get("error") == "") || len(q["code"]) > 1 || len(q["error"]) > 1 {
			http.Error(w, "Invalid response", 400)
			return
		}
		select {
		case result <- q:
			fmt.Fprintln(w, "The CLI has received the response. You can close this page.")
		default:
			http.Error(w, "Response already received", 409)
		}
	})
}
func Login(ctx context.Context, base, clientID string, out io.Writer, noBrowser bool) (config.Credentials, error) {
	if clientID == "" {
		return config.Credentials{}, errors.New("the official OAuth client ID is not set in this build")
	}
	if err := api.ValidateBase(base); err != nil {
		return config.Credentials{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return config.Credentials{}, err
	}
	state, verifier := random(), random()
	callback := "http://" + ln.Addr().String() + "/callback"
	result := make(chan url.Values, 1)
	server := &http.Server{Handler: Callback(state, result), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 5 * time.Second, WriteTimeout: 5 * time.Second, MaxHeaderBytes: 8192}
	defer server.Close()
	go func() { _ = server.Serve(ln) }()
	target := strings.TrimRight(base, "/") + "/oauth/authorize?" + url.Values{"client_id": {clientID}, "response_type": {"code"}, "redirect_uri": {callback}, "scope": {"cli:use"}, "state": {state}, "code_challenge": {Challenge(verifier)}, "code_challenge_method": {"S256"}, "prompt": {"consent"}}.Encode()
	fmt.Fprintln(out, "Open this URL to approve access to one team:\n"+target)
	if !noBrowser {
		if err = OpenBrowser(target); err != nil {
			fmt.Fprintln(out, "The browser could not start. Open the URL above.")
		}
	}
	select {
	case <-ctx.Done():
		return config.Credentials{}, fmt.Errorf("login did not complete: %w", ctx.Err())
	case q := <-result:
		if q.Get("error") != "" {
			return config.Credentials{}, errors.New("OAuth approval was denied")
		}
		return Exchange(ctx, api.Transport(), base, url.Values{"grant_type": {"authorization_code"}, "client_id": {clientID}, "code": {q.Get("code")}, "code_verifier": {verifier}, "redirect_uri": {callback}})
	}
}
func OpenBrowser(target string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", target)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", target)
	default:
		cmd = exec.Command("xdg-open", target)
	}
	return cmd.Run()
}
