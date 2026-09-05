package listener

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/lettermint/lettermint-cli/internal/api"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestLocalDestinationValidation(t *testing.T) {
	for _, target := range []string{"https://example.com/a", "http://169.254.169.254/", "http://127.0.0.1.example.com/", "http://user@localhost/", "file:///tmp/mail", "http://localhost/#fragment"} {
		if _, err := LocalClient(target); err == nil {
			t.Errorf("accepted %s", target)
		}
	}
	for _, target := range []string{"http://127.0.0.1:3000/a", "https://[::1]/a", "http://localhost/a"} {
		if _, err := LocalClient(target); err != nil {
			t.Errorf("rejected %s: %v", target, err)
		}
	}
}
func TestForwardPreservesBytesAndDoesNotFollowRedirects(t *testing.T) {
	body := []byte("{\"text\":\"héllo\"}\n")
	var requests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		actual, _ := io.ReadAll(r.Body)
		if string(actual) != string(body) {
			t.Error("changed payload bytes")
		}
		if r.Header.Get("Authorization") != "" {
			t.Error("forwarded OAuth token")
		}
		stamp := r.Header.Get("X-Lettermint-Delivery")
		mac := hmac.New(sha256.New, []byte("secret"))
		mac.Write([]byte(stamp + "."))
		mac.Write(body)
		want := "t=" + stamp + ",v1=" + hex.EncodeToString(mac.Sum(nil))
		if r.Header.Get("X-Lettermint-Signature") != want {
			t.Error("invalid signature")
		}
		w.Header().Set("Location", "/redirect")
		w.WriteHeader(302)
	}))
	defer server.Close()
	client, err := LocalClient(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	result := Forward(context.Background(), client, server.URL, "secret", api.Event{Sequence: 12, DeliveryID: "stable", Event: "message.inbound", Attempt: 2}, body)
	if requests != 1 || result.Status != 302 || result.Error != "local_http_302" {
		t.Fatalf("requests=%d result=%+v", requests, result)
	}
}
func TestSSEHandlesChunkedMultiLineFrames(t *testing.T) {
	var count int
	err := ReadSSE(strings.NewReader(": heartbeat\r\nevent: data\r\ndata: {\r\ndata: \"sequence\":1}\r\n\r\n"), func(kind string, data []byte) error {
		count++
		if kind != "data" || string(data) != "{\n\"sequence\":1}" {
			t.Errorf("%s %s", kind, data)
		}
		return nil
	})
	if err != nil || count != 1 {
		t.Fatalf("count=%d error=%v", count, err)
	}
}
func TestSSERejectsOversizeFrame(t *testing.T) {
	err := ReadSSE(strings.NewReader("data: "+strings.Repeat("a", 70<<10)+"\n\n"), func(string, []byte) error { t.Fatal("emitted oversized data"); return nil })
	if err == nil {
		t.Fatal("accepted oversized frame")
	}
}

func TestLostAcknowledgementRepeatsStableDelivery(t *testing.T) {
	for _, eventType := range []string{"message.inbound", "suppression.added", "suppression.removed"} {
		t.Run(eventType, func(t *testing.T) {
			testLostAcknowledgement(t, eventType)
		})
	}
}

func testLostAcknowledgement(t *testing.T, eventType string) {
	t.Helper()
	var streams, attempts, acknowledgements atomic.Int32
	payload := []byte("{\"id\":\"delivery-one\",\"data\":\"caf\u00e9\\r\\n\"}")
	local := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts.Add(1)
		body, _ := io.ReadAll(r.Body)
		if !bytes.Equal(body, payload) || r.Header.Get("X-Lettermint-Delivery-Id") != "delivery-one" || r.Header.Get("Authorization") != "" {
			t.Error("local request changed payload or credentials")
		}
		w.WriteHeader(500)
	}))
	defer local.Close()
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-token" {
			t.Error("missing API token")
		}
		switch {
		case strings.HasSuffix(r.URL.Path, "/stream"):
			n := streams.Add(1)
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprintf(w, "event: data\ndata: {\"sequence\":1,\"delivery_id\":\"delivery-one\",\"event\":%q,\"attempt\":1}\n\n", eventType)
			if n > 1 {
				fmt.Fprint(w, "event: error\ndata: {\"code\":\"connection_replaced\",\"message\":\"Done\"}\n\n")
			}
		case strings.HasSuffix(r.URL.Path, "/payload"):
			w.Write(payload)
		case strings.HasSuffix(r.URL.Path, "/acknowledgements"):
			var attempt api.Attempt
			if err := json.NewDecoder(r.Body).Decode(&attempt); err != nil || attempt.Status != 500 {
				t.Error("failed local attempt was not acknowledged")
			}
			if acknowledgements.Add(1) == 1 {
				connection, _, _ := w.(http.Hijacker).Hijack()
				connection.Close()
				return
			}
			fmt.Fprint(w, `{"data":{"acknowledged_sequence":1}}`)
		default:
			t.Error(r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer remote.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	client := &api.Client{BaseURL: remote.URL, HTTP: api.Transport(), Token: func(context.Context) (string, error) { return "test-token", nil }}
	var out bytes.Buffer
	err := Run(ctx, client, api.Listener{ID: "session-one", Secret: "secret"}, local.URL, &out)
	var remoteError *api.Error
	if !errors.As(err, &remoteError) || remoteError.Code != "connection_replaced" {
		t.Fatal(err)
	}
	if attempts.Load() != 2 || acknowledgements.Load() != 2 || streams.Load() != 2 {
		t.Fatalf("attempts=%d acknowledgements=%d streams=%d", attempts.Load(), acknowledgements.Load(), streams.Load())
	}
	if bytes.Count(out.Bytes(), []byte("\n")) != 2 {
		t.Fatal(out.String())
	}
	if !strings.Contains(out.String(), eventType) {
		t.Fatal("missing event type in local attempt output")
	}
}
