package listener

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lettermint/lettermint-cli/internal/api"
)

func TestConnectionObserverReportsTransitionsAndIgnoresScheduledReconnect(t *testing.T) {
	for _, temporaryFailure := range []bool{false, true} {
		name := "scheduled reconnect"
		if temporaryFailure {
			name = "connection loss"
		}
		t.Run(name, func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				n := calls.Add(1)
				if temporaryFailure && n <= 2 {
					w.WriteHeader(503)
					return
				}
				w.Header().Set("Content-Type", "text/event-stream")
				if !temporaryFailure && n == 1 {
					io.WriteString(w, "event: reconnect\ndata: {}\n\n")
					return
				}
				io.WriteString(w, "event: heartbeat\ndata: {}\n\nevent: error\ndata: {\"code\":\"connection_replaced\",\"message\":\"Done\"}\n\n")
			}))
			defer server.Close()
			client := &api.Client{BaseURL: server.URL, HTTP: api.Transport(), Token: func(context.Context) (string, error) { return "token", nil }}
			var transitions []bool
			ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
			defer cancel()
			err := RunWithObserver(ctx, client, api.Listener{ID: "session", Secret: "secret"}, "http://localhost:3000", Observer{
				Attempt:    func(api.Event, api.Attempt) error { t.Error("unexpected local attempt"); return nil },
				Connection: func(connected bool) { transitions = append(transitions, connected) },
			})
			var remote *api.Error
			if !errors.As(err, &remote) || remote.Code != "connection_replaced" {
				t.Fatal(err)
			}
			if temporaryFailure && !reflect.DeepEqual(transitions, []bool{false, true}) {
				t.Fatal(transitions)
			}
			if !temporaryFailure && len(transitions) != 0 {
				t.Fatal("routine reconnect was shown as a connection failure")
			}
		})
	}
}
