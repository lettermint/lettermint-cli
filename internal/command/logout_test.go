package command

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/lettermint/lettermint-cli/internal/api"
	"github.com/lettermint/lettermint-cli/internal/config"
)

type recoveryVault struct {
	credentials *config.Credentials
	deleteError error
	deletes     int
}

func (v *recoveryVault) Get(string) (config.Credentials, error) {
	if v.credentials == nil {
		return config.Credentials{}, config.ErrCredentialsNotFound
	}
	return *v.credentials, nil
}
func (v *recoveryVault) Set(_ string, c config.Credentials) error {
	v.credentials = &c
	return nil
}
func (v *recoveryVault) Delete(string) error {
	v.deletes++
	if v.deleteError != nil {
		return v.deleteError
	}
	if v.credentials == nil {
		return config.ErrCredentialsNotFound
	}
	v.credentials = nil
	return nil
}

func TestLogoutRecoveryAfterRevocationOrExpiry(t *testing.T) {
	for _, expired := range []bool{false, true} {
		t.Run(map[bool]string{false: "revoked", true: "expired"}[expired], func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.URL.Path == "/oauth/token" {
					w.WriteHeader(400)
					io.WriteString(w, `{"error":"invalid_grant"}`)
					return
				}
				w.WriteHeader(401)
			}))
			defer server.Close()
			store := authenticatedStore(t, server.URL)
			credentials, _ := store.Vault.Get("work")
			if expired {
				credentials.ExpiresAt = time.Now().Add(-time.Minute)
			}
			vault := &recoveryVault{credentials: &credentials}
			store.Vault = vault
			logout := newWithStore("test", "public", store)
			logout.SetArgs([]string{"auth", "logout", "--profile", "work"})
			err := logout.Execute()
			if api.ExitCode(err) != 3 || !strings.Contains(err.Error(), "--local") || vault.deletes != 0 {
				t.Fatalf("logout error=%v deletes=%d", err, vault.deletes)
			}
			if _, _, err := store.Resolve("work"); err != nil {
				t.Fatal("failed remote logout removed profile", err)
			}
			beforeLocal := calls
			local := newWithStore("test", "public", store)
			local.SetArgs([]string{"auth", "logout", "--profile", "work", "--local", "--yes", "--no-input", "--json"})
			var out bytes.Buffer
			local.SetOut(&out)
			if err := local.Execute(); err != nil {
				t.Fatal(err)
			}
			var result map[string]bool
			if err := json.Unmarshal(out.Bytes(), &result); err != nil || result["revoked"] || !result["local_credentials_removed"] {
				t.Fatalf("local result=%s error=%v", out.Bytes(), err)
			}
			cfg, err := store.Read()
			if err != nil || cfg.Selected != "" || len(cfg.Profiles) != 0 || vault.credentials != nil || calls != beforeLocal {
				t.Fatalf("local removal did not clear only local state: %v", err)
			}
		})
	}
}

func TestNormalLogoutRevokesBeforeRemovingCredentials(t *testing.T) {
	store := authenticatedStore(t, "https://example.invalid")
	credentials, _ := store.Vault.Get("work")
	vault := &recoveryVault{credentials: &credentials}
	store.Vault = vault
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Method != "DELETE" || r.URL.Path != "/v1/connection" || r.Header.Get("Authorization") != "Bearer grant" || vault.deletes != 0 {
			t.Error("revocation did not use the original grant before local removal")
		}
		io.WriteString(w, `{"data":{"revoked":true}}`)
	}))
	defer server.Close()
	if err := store.Update(context.Background(), func(cfg *config.Config) error {
		p := cfg.Profiles["work"]
		p.APIURL = server.URL
		cfg.Profiles["work"] = p
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	cmd := newWithStore("test", "public", store)
	cmd.SetArgs([]string{"auth", "logout", "--profile", "work", "--json"})
	var out bytes.Buffer
	cmd.SetOut(&out)
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if calls != 1 || vault.credentials != nil || !strings.Contains(out.String(), `"revoked":true`) {
		t.Fatalf("calls=%d output=%s", calls, out.String())
	}
}

func TestLocalLogoutRequiresConfirmationAndHandlesMissingCredentials(t *testing.T) {
	for _, tc := range []struct {
		name, input string
		flags       []string
		deleteError error
		removed     bool
	}{
		{"no input", "", []string{"--no-input"}, nil, false},
		{"declined", "n\n", nil, nil, false},
		{"confirmed", "y\n", nil, nil, true},
		{"credential store unavailable", "", []string{"--yes"}, errors.New("keyring unavailable"), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := authenticatedStore(t, "https://example.invalid")
			vault := &recoveryVault{deleteError: tc.deleteError}
			store.Vault = vault
			cmd := newWithStore("test", "public", store)
			cmd.SetArgs(append([]string{"auth", "logout", "--local"}, tc.flags...))
			cmd.SetIn(strings.NewReader(tc.input))
			cmd.SetOut(io.Discard)
			cmd.SetErr(io.Discard)
			err := cmd.Execute()
			cfg, readErr := store.Read()
			if readErr != nil || (err == nil) != tc.removed || (len(cfg.Profiles) == 0) != tc.removed {
				t.Fatalf("error=%v profiles=%v readError=%v", err, cfg.Profiles, readErr)
			}
		})
	}
}

type approvalWriter struct{ approve func() }

func (w approvalWriter) Write(b []byte) (int, error) {
	w.approve()
	return len(b), nil
}

func TestLocalLogoutDoesNotDeleteAReplacementGrant(t *testing.T) {
	store := authenticatedStore(t, "https://example.invalid")
	credentials, _ := store.Vault.Get("work")
	vault := &recoveryVault{credentials: &credentials}
	store.Vault = vault
	cmd := newWithStore("test", "public", store)
	cmd.SetArgs([]string{"auth", "logout", "--local"})
	cmd.SetIn(strings.NewReader("y\n"))
	cmd.SetErr(approvalWriter{approve: func() {
		if err := store.Update(context.Background(), func(cfg *config.Config) error {
			p := cfg.Profiles["work"]
			p.ConnectionID = "replacement"
			cfg.Profiles["work"] = p
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		vault.credentials.ConnectionID = "replacement"
	}})
	if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), "grant changed") || vault.deletes != 0 {
		t.Fatalf("error=%v deletes=%d", err, vault.deletes)
	}
}

type browserApproval struct{ t *testing.T }

func (b browserApproval) Write(data []byte) (int, error) {
	for _, line := range strings.Split(string(data), "\n") {
		if !strings.HasPrefix(line, "http://127.0.0.1:") {
			continue
		}
		u, err := url.Parse(line)
		if err != nil {
			return 0, err
		}
		callback, err := url.Parse(u.Query().Get("redirect_uri"))
		if err != nil {
			return 0, err
		}
		callback.RawQuery = url.Values{"state": {u.Query().Get("state")}, "code": {"approved"}}.Encode()
		resp, err := (&http.Client{Timeout: time.Second}).Get(callback.String())
		if err != nil {
			return 0, err
		}
		resp.Body.Close()
		if resp.StatusCode != 200 {
			b.t.Errorf("callback status=%d", resp.StatusCode)
		}
	}
	return len(data), nil
}

func TestLoginCanReuseProfileAfterLocalLogout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/oauth/token":
			if err := r.ParseForm(); err != nil || r.Form.Get("code") != "approved" || r.Form.Get("grant_type") != "authorization_code" {
				t.Error("unexpected OAuth exchange")
			}
			io.WriteString(w, `{"access_token":"new-token","refresh_token":"new-refresh","expires_in":3600,"token_type":"Bearer"}`)
		case "/v1/identity":
			if r.Header.Get("Authorization") != "Bearer new-token" {
				t.Error("identity used an old token")
			}
			io.WriteString(w, `{"data":{"user":{"id":"user-one"},"team":{"id":"team-one"},"connection_id":"new-grant"}}`)
		default:
			t.Errorf("unexpected request: %s", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	store := authenticatedStore(t, server.URL)
	credentials, _ := store.Vault.Get("work")
	store.Vault = &recoveryVault{credentials: &credentials}
	logout := newWithStore("test", "public", store)
	logout.SetArgs([]string{"auth", "logout", "--local", "--yes"})
	logout.SetOut(io.Discard)
	if err := logout.Execute(); err != nil {
		t.Fatal(err)
	}
	login := newWithStore("test", "public", store)
	login.SetArgs([]string{"auth", "login", "--name", "work", "--api-url", server.URL, "--no-browser"})
	login.SetOut(io.Discard)
	login.SetErr(browserApproval{t})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := login.ExecuteContext(ctx); err != nil {
		t.Fatal(err)
	}
	_, profile, err := store.Resolve("work")
	saved, credentialErr := store.Vault.Get("work")
	if err != nil || credentialErr != nil || profile.ConnectionID != "new-grant" || saved.AccessToken != "new-token" {
		t.Fatalf("profile=%v error=%v credentialError=%v", profile, err, credentialErr)
	}
}
