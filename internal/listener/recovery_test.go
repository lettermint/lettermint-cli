package listener

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lettermint/lettermint-cli/internal/api"
)

func TestListenerRetriesOnlyTemporaryCredentialFailures(t *testing.T) {
	for _, tc := range []struct {
		name  string
		err   error
		retry bool
	}{
		{"OAuth unavailable", &api.Error{Status: 503, Code: "oauth_unavailable"}, true},
		{"OAuth rate limit", &api.Error{Status: 429, Code: "rate_limited"}, true},
		{"network timeout", &net.DNSError{Err: "timeout", IsTimeout: true}, true},
		{"revoked grant", &api.Error{Status: 401, Code: "oauth_grant_expired"}, false},
		{"permission denied", &api.Error{Status: 403, Code: "permission_denied"}, false},
		{"credential store unavailable", errors.New("credential store unavailable"), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				if r.Header.Get("Authorization") != "Bearer recovered" {
					t.Error("stream used the wrong token")
				}
				w.Header().Set("Content-Type", "text/event-stream")
				io.WriteString(w, "event: error\ndata: {\"code\":\"permission_denied\",\"message\":\"Access removed\"}\n\n")
			}))
			defer server.Close()
			calls := 0
			client := &api.Client{BaseURL: server.URL, HTTP: api.Transport(), Token: func(context.Context) (string, error) {
				calls++
				if calls == 1 {
					return "", tc.err
				}
				return "recovered", nil
			}}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			err := Run(ctx, client, api.Listener{ID: "session-one", Secret: "test-secret"}, "http://127.0.0.1:3000/hook", io.Discard)
			if tc.retry {
				var remote *api.Error
				if calls != 2 || requests.Load() != 1 || !errors.As(err, &remote) || remote.Code != "permission_denied" {
					t.Fatalf("calls=%d requests=%d error=%v", calls, requests.Load(), err)
				}
			} else if calls != 1 || requests.Load() != 0 || !errors.Is(err, tc.err) {
				t.Fatalf("terminal credential failure was retried: calls=%d requests=%d error=%v", calls, requests.Load(), err)
			}
		})
	}
}
