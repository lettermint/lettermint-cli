package auth

import (
	"context"
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

func TestOAuthFailureClassification(t *testing.T) {
	for _, tc := range []struct {
		name       string
		status     int
		body       string
		wantStatus int
		wantCode   string
		wantExit   int
	}{
		{"expired refresh", 400, `{"error":"invalid_grant"}`, 401, "oauth_grant_expired", 3},
		{"revoked grant", 401, `{"error":"invalid_grant"}`, 401, "oauth_grant_expired", 3},
		{"invalid client", 401, `{"error":"invalid_client"}`, 401, "oauth_failed", 3},
		{"invalid request", 400, `{"error":"invalid_request"}`, 400, "oauth_failed", 2},
		{"rate limit", 429, `{"error":"invalid_grant"}`, 429, "rate_limited", 7},
		{"server failure", 500, `{"error":"invalid_grant"}`, 500, "oauth_unavailable", 8},
		{"service unavailable", 503, `<html>private response</html>`, 503, "oauth_unavailable", 8},
		{"non JSON rejection", 403, `<html>private response</html>`, 403, "oauth_failed", 4},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
				io.WriteString(w, tc.body)
			}))
			defer server.Close()
			_, err := Exchange(context.Background(), api.Transport(), server.URL, url.Values{"grant_type": {"refresh_token"}})
			var remote *api.Error
			if !errors.As(err, &remote) || remote.Status != tc.wantStatus || remote.Code != tc.wantCode || api.ExitCode(err) != tc.wantExit {
				t.Fatalf("error=%v; want status=%d code=%s exit=%d", err, tc.wantStatus, tc.wantCode, tc.wantExit)
			}
			if strings.Contains(err.Error(), "private response") {
				t.Fatal("exposed the raw OAuth response")
			}
		})
	}
}

func TestFailedRefreshPreservesCredentialsForRetry(t *testing.T) {
	for _, status := range []int{400, 429, 503} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if err := r.ParseForm(); err != nil || r.Form.Get("refresh_token") != "original-refresh" {
					t.Error("refresh did not use the saved token")
				}
				if calls == 1 {
					w.WriteHeader(status)
					io.WriteString(w, `{"error":"invalid_grant"}`)
					return
				}
				io.WriteString(w, `{"access_token":"next","refresh_token":"rotated","expires_in":3600,"token_type":"Bearer"}`)
			}))
			defer server.Close()
			original := config.Credentials{ConnectionID: "grant-one", AccessToken: "expired", RefreshToken: "original-refresh", ExpiresAt: time.Now().Add(-time.Minute)}
			v := &vault{c: original}
			store := &config.Store{Dir: t.TempDir(), Vault: v}
			source := TokenSource(store, "work", config.Profile{ConnectionID: "grant-one", APIURL: server.URL, ClientID: "public"}, api.Transport())
			if token, err := source(context.Background()); err == nil || token != "" {
				t.Fatal("refresh failure returned a token")
			}
			saved, _ := v.Get("work")
			if saved != original || calls != 1 {
				t.Fatal("failed refresh changed credentials or made an extra request")
			}
			if status == 400 {
				return
			}
			if token, err := source(context.Background()); err != nil || token != "next" {
				t.Fatalf("retry token=%q error=%v", token, err)
			}
			saved, _ = v.Get("work")
			if saved.ConnectionID != original.ConnectionID || saved.RefreshToken != "rotated" || calls != 2 {
				t.Fatal("successful retry did not save the rotated token for the same grant")
			}
		})
	}
}
