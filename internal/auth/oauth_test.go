package auth

import (
	"context"
	"encoding/json"
	"github.com/lettermint/lettermint-cli/internal/api"
	"github.com/lettermint/lettermint-cli/internal/config"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestPKCEKnownVector(t *testing.T) {
	if got := Challenge("dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk"); got != "E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM" {
		t.Fatal(got)
	}
}
func TestCallbackRejectsInvalidResponses(t *testing.T) {
	for _, query := range []string{"state=wrong&code=x", "state=correct&state=correct&code=x", "state=correct&code=x&error=denied", "state=correct"} {
		t.Run(query, func(t *testing.T) {
			ch := make(chan url.Values, 1)
			r := httptest.NewRequest("GET", "http://127.0.0.1/callback?"+query, nil)
			w := httptest.NewRecorder()
			Callback("correct", ch).ServeHTTP(w, r)
			if w.Code != 400 || len(ch) != 0 {
				t.Fatalf("status %d; responses %d", w.Code, len(ch))
			}
		})
	}
}
func TestCallbackAcceptsDenial(t *testing.T) {
	ch := make(chan url.Values, 1)
	w := httptest.NewRecorder()
	Callback("correct", ch).ServeHTTP(w, httptest.NewRequest("GET", "http://127.0.0.1/callback?state=correct&error=access_denied", nil))
	if w.Code != 200 || (<-ch).Get("error") != "access_denied" {
		t.Fatal("denial was not handled")
	}
}

type vault struct {
	mu sync.Mutex
	c  config.Credentials
}

func (v *vault) Get(string) (config.Credentials, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.c, nil
}
func (v *vault) Set(_ string, c config.Credentials) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.c = c
	return nil
}
func (v *vault) Delete(string) error { return nil }
func TestConcurrentRefreshUsesOneRotatedToken(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if err := r.ParseForm(); err != nil {
			t.Error(err)
		}
		if r.Form.Get("refresh_token") != "original" {
			t.Errorf("unexpected refresh token")
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "next", "refresh_token": "rotated", "expires_in": 3600, "token_type": "Bearer"})
	}))
	defer server.Close()
	s := &config.Store{Dir: t.TempDir(), Vault: &vault{c: config.Credentials{ConnectionID: "grant-one", RefreshToken: "original", ExpiresAt: time.Now().Add(-time.Minute)}}}
	source := TokenSource(s, "test", config.Profile{ConnectionID: "grant-one", APIURL: server.URL, ClientID: "public"}, api.Transport())
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Go(func() {
			token, err := source(context.Background())
			if err != nil || token != "next" {
				t.Errorf("token=%q err=%v", token, err)
			}
		})
	}
	wg.Wait()
	if calls.Load() != 1 {
		t.Fatalf("refresh calls: %d", calls.Load())
	}
}

func TestRunningTokenSourceRejectsReplacementGrant(t *testing.T) {
	v := &vault{c: config.Credentials{ConnectionID: "grant-two", AccessToken: "another-user", ExpiresAt: time.Now().Add(time.Hour)}}
	s := &config.Store{Dir: t.TempDir(), Vault: v}
	source := TokenSource(s, "work", config.Profile{ConnectionID: "grant-one"}, api.Transport())
	if token, err := source(context.Background()); err == nil || token != "" {
		t.Fatal("accepted a replacement grant")
	}
}
