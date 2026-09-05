package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
)

// Response and Page keep the resource envelope separate from pagination metadata.
type Response[T any] struct {
	Data T `json:"data"`
}
type Page[T any] struct {
	Data       []T     `json:"data"`
	NextCursor *string `json:"next_cursor"`
}
type Attachment struct {
	Filename    string `json:"filename"`
	Content     string `json:"content"`
	ContentType string `json:"content_type,omitempty"`
	ContentID   string `json:"content_id,omitempty"`
}
type Tag struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}
type SendInput struct {
	From        string            `json:"from"`
	To          []string          `json:"to"`
	Subject     string            `json:"subject"`
	CC          []string          `json:"cc,omitempty"`
	BCC         []string          `json:"bcc,omitempty"`
	ReplyTo     []string          `json:"reply_to,omitempty"`
	HTML        *string           `json:"html,omitempty"`
	Text        *string           `json:"text,omitempty"`
	Headers     map[string]string `json:"headers,omitempty"`
	Metadata    map[string]string `json:"metadata,omitempty"`
	Attachments []Attachment      `json:"attachments,omitempty"`
	Tags        []Tag             `json:"tags,omitempty"`
	Settings    map[string]any    `json:"settings,omitempty"`
	RouteID     string            `json:"route_id,omitempty"`
	Route       string            `json:"route,omitempty"`
	Tag         string            `json:"tag,omitempty"`
}
type SendResult struct {
	MessageID        string `json:"message_id"`
	Status           string `json:"status"`
	IdempotentReplay *bool  `json:"idempotent_replay,omitempty"`
}

func Decode[T any](value json.RawMessage) (T, error) {
	var result T
	err := json.Unmarshal(value, &result)
	return result, err
}
func (c *Client) Send(ctx context.Context, project, key string, input SendInput) (Response[SendResult], error) {
	query := url.Values{"project_id": {project}}
	if input.RouteID != "" {
		query.Set("route_id", input.RouteID)
	}
	wire := input
	wire.RouteID = ""
	raw, err := c.Do(ctx, http.MethodPost, "/v1/send", query, wire, http.Header{"Idempotency-Key": {key}})
	if err != nil {
		return Response[SendResult]{}, err
	}
	result, err := Decode[SendResult](raw)
	if err != nil {
		return Response[SendResult]{}, err
	}
	if result.MessageID == "" || result.Status != "pending" {
		return Response[SendResult]{}, fmt.Errorf("unexpected immediate-send response")
	}
	result.Status = "accepted"
	return Response[SendResult]{Data: result}, nil
}
