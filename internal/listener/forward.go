package listener

import (
	"bufio"
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
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

func Signature(secret string, body []byte, timestamp int64) string {
	stamp := strconv.FormatInt(timestamp, 10)
	h := hmac.New(sha256.New, []byte(secret))
	h.Write([]byte(stamp + "."))
	h.Write(body)
	return "t=" + stamp + ",v1=" + hex.EncodeToString(h.Sum(nil))
}
func LocalClient(target string) (*http.Client, error) {
	u, err := url.Parse(target)
	if err != nil {
		return nil, err
	}
	if (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.Fragment != "" {
		return nil, errors.New("forward destination must be a loopback HTTP or HTTPS URL without user information or a fragment")
	}
	host := u.Hostname()
	ip := net.ParseIP(host)
	if host != "localhost" && (ip == nil || !ip.IsLoopback()) {
		return nil, errors.New("forward destination must use localhost or a loopback IP address")
	}
	tr := &http.Transport{Proxy: nil, DisableCompression: true, DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, err
		}
		ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
		if err != nil {
			return nil, err
		}
		if len(ips) == 0 {
			return nil, errors.New("loopback address did not resolve")
		}
		for _, ip := range ips {
			if !ip.IP.IsLoopback() {
				return nil, errors.New("forward address resolved outside loopback")
			}
		}
		var last error
		for _, ip := range ips {
			conn, err := (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
			if err == nil {
				return conn, nil
			}
			last = err
		}
		return nil, last
	}}
	return &http.Client{Transport: tr, Timeout: 30 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, nil
}

// ReadSSE accepts named, multi-line SSE frames. It does not treat payload text as commands.
func ReadSSE(r io.Reader, handle func(string, []byte) error) error {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 4096), 64<<10)
	event := "message"
	var data []byte
	dispatch := func() error {
		if len(data) == 0 {
			event = "message"
			return nil
		}
		err := handle(event, bytes.TrimSuffix(data, []byte("\n")))
		data = nil
		event = "message"
		return err
	}
	for scanner.Scan() {
		line := strings.TrimSuffix(scanner.Text(), "\r")
		if line == "" {
			if err := dispatch(); err != nil {
				return err
			}
			continue
		}
		if strings.HasPrefix(line, ":") {
			continue
		}
		field, value, _ := strings.Cut(line, ":")
		value = strings.TrimPrefix(value, " ")
		switch field {
		case "event":
			event = value
		case "data":
			if len(data)+len(value) > 64<<10 {
				return errors.New("SSE frame exceeds limit")
			}
			data = append(data, []byte(value+"\n")...)
		}
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	return nil
}

type Observer struct {
	Attempt    func(api.Event, api.Attempt) error
	Connection func(connected bool)
}

// Run preserves the NDJSON interface for callers that do not need presentation.
func Run(ctx context.Context, c *api.Client, session api.Listener, target string, out io.Writer) error {
	encoder := json.NewEncoder(out)
	return RunWithObserver(ctx, c, session, target, Observer{Attempt: func(event api.Event, attempt api.Attempt) error {
		return encoder.Encode(map[string]any{"event": event, "result": attempt})
	}})
}

func RunWithObserver(ctx context.Context, c *api.Client, session api.Listener, target string, observer Observer) error {
	if observer.Attempt == nil {
		return errors.New("listener requires an attempt observer")
	}
	local, err := LocalClient(target)
	if err != nil {
		return err
	}
	defer local.CloseIdleConnections()
	if session.ID == "" || session.Secret == "" {
		return errors.New("listener response has no ID or signing secret")
	}
	path := "/v1/listeners/" + url.PathEscape(session.ID)
	delay := time.Second
	disconnected := false
	for {
		if err = ctx.Err(); err != nil {
			return err
		}
		requestedReconnect := false
		resp, openErr := c.Open(ctx, "GET", path+"/stream", nil, nil, http.Header{"Accept": {"text/event-stream"}})
		if openErr == nil {
			if !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/event-stream") {
				resp.Body.Close()
				return errors.New("server did not return an SSE stream")
			}
			if disconnected {
				if observer.Connection != nil {
					observer.Connection(true)
				}
				disconnected = false
			}
			err = ReadSSE(resp.Body, func(kind string, raw []byte) error {
				switch kind {
				case "heartbeat":
					return nil
				case "reconnect":
					requestedReconnect = true
					return io.EOF
				case "error":
					var event struct {
						Code    string `json:"code"`
						Message string `json:"message"`
					}
					if e := json.Unmarshal(raw, &event); e != nil {
						return e
					}
					status := 410
					switch event.Code {
					case "authentication_required":
						status = 401
					case "permission_denied":
						status = 403
					case "stream_failed":
						status = 500
					}
					return &api.Error{Status: status, Code: event.Code, Message: event.Message}
				case "data":
					var event api.Event
					if e := json.Unmarshal(raw, &event); e != nil {
						return e
					}
					if event.Sequence < 1 || event.DeliveryID == "" {
						return errors.New("invalid event envelope")
					}
					payload, e := c.Open(ctx, "GET", path+"/events/"+strconv.FormatInt(event.Sequence, 10)+"/payload", nil, nil, nil)
					if e != nil {
						return e
					}
					body, e := io.ReadAll(io.LimitReader(payload.Body, api.MaxResponse+1))
					payload.Body.Close()
					if e != nil {
						return e
					}
					if len(body) > api.MaxResponse {
						return errors.New("webhook payload exceeds limit")
					}
					attempt := Forward(ctx, local, target, session.Secret, event, body)
					if e = observer.Attempt(event, attempt); e != nil {
						return e
					}
					_, e = c.Do(ctx, "POST", path+"/acknowledgements", nil, attempt, nil)
					return e
				default:
					return errors.New("unsupported SSE event: " + kind)
				}
			})
			resp.Body.Close()
			delay = time.Second
		} else {
			err = openErr
		}
		var credential *api.CredentialError
		var remote *api.Error
		var network net.Error
		retryable := (errors.As(err, &remote) && (remote.Status >= 500 || remote.Status == 429)) || errors.As(err, &network)
		if errors.As(err, &credential) && !retryable {
			return err
		}
		if errors.As(err, &remote) && remote.Status < 500 && remote.Status != 429 {
			return err
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err != nil && remote == nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) && !errors.As(err, &network) {
			return err
		}
		if err != nil && !requestedReconnect && !disconnected {
			disconnected = true
			if observer.Connection != nil {
				observer.Connection(false)
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(delay):
		}
		if delay < 15*time.Second {
			delay *= 2
			if delay > 15*time.Second {
				delay = 15 * time.Second
			}
		}
	}
}
func Forward(ctx context.Context, client *http.Client, target, secret string, event api.Event, body []byte) api.Attempt {
	start := time.Now()
	attempt := api.Attempt{Sequence: event.Sequence}
	stamp := time.Now().Unix()
	req, err := http.NewRequestWithContext(ctx, "POST", target, bytes.NewReader(body))
	if err != nil {
		attempt.Error = "invalid_destination"
		return attempt
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Lettermint-Signature", Signature(secret, body, stamp))
	req.Header.Set("X-Lettermint-Event", event.Event)
	req.Header.Set("X-Lettermint-Delivery", strconv.FormatInt(stamp, 10))
	req.Header.Set("X-Lettermint-Attempt", strconv.Itoa(event.Attempt))
	req.Header.Set("X-Lettermint-Delivery-Id", event.DeliveryID)
	resp, err := client.Do(req)
	attempt.DurationMS = time.Since(start).Milliseconds()
	if err != nil {
		attempt.Error = "local_request_failed"
		return attempt
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
	attempt.Status = resp.StatusCode
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		attempt.Error = fmt.Sprintf("local_http_%d", resp.StatusCode)
	}
	return attempt
}
