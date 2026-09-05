package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const MaxResponse = 32 << 20

type CredentialError struct{ Err error }

func (e *CredentialError) Error() string { return e.Err.Error() }
func (e *CredentialError) Unwrap() error { return e.Err }

type Error struct {
	Status  int
	Code    string
	Message string
	Details json.RawMessage
}

func (e *Error) Error() string { return e.Code + ": " + e.Message }
func ErrorCode(err error) string {
	var e *Error
	if errors.As(err, &e) {
		return e.Code
	}
	return "command_failed"
}
func ExitCode(err error) int {
	var e *Error
	if errors.As(err, &e) {
		switch e.Status {
		case 401:
			return 3
		case 403:
			return 4
		case 404:
			return 5
		case 409, 410:
			return 6
		case 422, 400:
			return 2
		case 429:
			return 7
		}
		return 8
	}
	if errors.Is(err, context.Canceled) {
		return 130
	}
	return 1
}

type Client struct {
	BaseURL string
	HTTP    *http.Client
	Token   func(context.Context) (string, error)
}

func Transport() *http.Client {
	return &http.Client{Timeout: 70 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}
func ValidateBase(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return err
	}
	if u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return errors.New("API URL must contain only an origin")
	}
	if u.Scheme != "https" && !(u.Scheme == "http" && (u.Hostname() == "127.0.0.1" || u.Hostname() == "localhost" || u.Hostname() == "::1")) {
		return errors.New("API URL must use HTTPS, except on loopback")
	}
	if u.Host == "" {
		return errors.New("API URL has no host")
	}
	return nil
}
func (c *Client) Open(ctx context.Context, method, path string, query url.Values, body any, headers http.Header) (*http.Response, error) {
	if err := ValidateBase(c.BaseURL); err != nil {
		return nil, err
	}
	if !strings.HasPrefix(path, "/v1/") || strings.Contains(path, "..") {
		return nil, errors.New("invalid CLI API path")
	}
	var input io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		input = bytes.NewReader(b)
	}
	target := strings.TrimRight(c.BaseURL, "/") + path
	if len(query) > 0 {
		target += "?" + query.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, method, target, input)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	for key, values := range headers {
		for _, value := range values {
			req.Header.Add(key, value)
		}
	}
	token, err := c.Token(ctx)
	if err != nil {
		return nil, &CredentialError{Err: err}
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("API request failed: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		defer resp.Body.Close()
		return nil, DecodeError(resp)
	}
	return resp, nil
}
func DecodeError(resp *http.Response) error {
	var wire struct {
		Error struct {
			Code    string          `json:"code"`
			Message string          `json:"message"`
			Details json.RawMessage `json:"details"`
		}
		Message string          `json:"message"`
		Errors  json.RawMessage `json:"errors"`
	}
	_ = json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&wire)
	code := wire.Error.Code
	switch code {
	case "UNAUTHENTICATED":
		code = "authentication_required"
	case "FORBIDDEN":
		code = "permission_denied"
	case "NOT_FOUND":
		code = "not_found"
	case "VALIDATION_ERROR":
		code = "validation_failed"
	}
	if code == "" {
		code = map[int]string{401: "authentication_required", 403: "permission_denied", 404: "not_found", 409: "conflict", 410: "expired", 422: "validation_failed", 429: "rate_limited"}[resp.StatusCode]
		if code == "" {
			code = "http_error"
		}
	}
	message := wire.Error.Message
	if message == "" {
		message = wire.Message
	}
	if message == "" {
		message = http.StatusText(resp.StatusCode)
	}
	details := wire.Error.Details
	if len(details) == 0 {
		details = wire.Errors
	}
	return &Error{Status: resp.StatusCode, Code: code, Message: message, Details: details}
}
func (c *Client) Do(ctx context.Context, method, path string, query url.Values, body any, headers http.Header) (json.RawMessage, error) {
	resp, err := c.Open(ctx, method, path, query, body, headers)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, MaxResponse+1))
	if err != nil {
		return nil, err
	}
	if len(b) > MaxResponse {
		return nil, errors.New("API response exceeds the size limit")
	}
	if len(b) == 0 {
		return json.RawMessage(`{}`), nil
	}
	if !json.Valid(b) {
		return nil, errors.New("API returned invalid JSON")
	}
	return b, nil
}

type Identity struct {
	Data struct {
		User struct {
			ID    string `json:"id"`
			Email string `json:"email"`
		} `json:"user"`
		Team struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"team"`
		ConnectionID string `json:"connection_id"`
	} `json:"data"`
}
type Listener struct {
	ID                   string `json:"id"`
	Secret               string `json:"secret"`
	AcknowledgedSequence int64  `json:"acknowledged_sequence"`
}
type Event struct {
	Sequence   int64  `json:"sequence"`
	DeliveryID string `json:"delivery_id"`
	Event      string `json:"event"`
	Attempt    int    `json:"attempt"`
}
type Attempt struct {
	Sequence   int64  `json:"sequence"`
	Status     int    `json:"status"`
	Error      string `json:"error,omitempty"`
	DurationMS int64  `json:"duration_ms"`
}

func (c *Client) Identity(ctx context.Context) (Identity, error) {
	var v Identity
	b, err := c.Do(ctx, "GET", "/v1/identity", nil, nil, nil)
	if err == nil {
		err = json.Unmarshal(b, &v)
	}
	return v, err
}
func (c *Client) CreateListener(ctx context.Context, project, route string, events []string, machine bool) (Listener, error) {
	var v struct {
		Data Listener `json:"data"`
	}
	body := map[string]any{"project_id": project, "events": events, "include_machine_events": machine}
	if route != "" {
		body["route_id"] = route
	}
	b, err := c.Do(ctx, "POST", "/v1/listeners", nil, body, nil)
	if err == nil {
		err = json.Unmarshal(b, &v)
	}
	return v.Data, err
}
