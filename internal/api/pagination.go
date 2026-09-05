package api

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/url"
	"strconv"
	"strings"
)

// Continuations contain parameters, never a URL to which credentials can be sent.
type continuation struct {
	Version int        `json:"version"`
	Origin  string     `json:"origin"`
	Path    string     `json:"path"`
	Context string     `json:"context"`
	Page    url.Values `json:"page"`
}

func validatePage(page url.Values) error {
	for key, values := range page {
		if len(values) != 1 || len(values[0]) > 8192 {
			return errors.New("invalid pagination parameters")
		}
		switch key {
		case "page[size]", "limit":
			n, err := strconv.Atoi(values[0])
			if err != nil || n < 1 || n > 100 {
				return errors.New("invalid page size")
			}
		case "page[number]":
			n, err := strconv.Atoi(values[0])
			if err != nil || n < 1 {
				return errors.New("invalid page number")
			}
		case "page[cursor]", "cursor":
		default:
			return errors.New("invalid pagination parameter")
		}
	}
	return nil
}

func (c *Client) List(ctx context.Context, path string, filters url.Values, limit int, cursor string) (json.RawMessage, error) {
	if limit < 1 || limit > 100 {
		return nil, errors.New("--limit must be between 1 and 100")
	}
	if filters == nil {
		filters = url.Values{}
	}
	query, _ := url.ParseQuery(filters.Encode())
	page := url.Values{"page[size]": {strconv.Itoa(limit)}}
	if path == "/v1/listeners" {
		page = url.Values{"limit": {strconv.Itoa(limit)}}
	}
	if cursor != "" {
		if len(cursor) > 32768 {
			return nil, errors.New("invalid cursor")
		}
		bytes, err := base64.RawURLEncoding.DecodeString(cursor)
		var saved continuation
		if err != nil || json.Unmarshal(bytes, &saved) != nil || saved.Version != 1 || saved.Origin != c.BaseURL || saved.Path != path || saved.Context != filters.Encode() {
			return nil, errors.New("cursor does not match this API, resource, or context")
		}
		page = saved.Page
		if !hasPosition(page) {
			return nil, errors.New("cursor has no page position")
		}
	}
	if err := validatePage(page); err != nil {
		return nil, err
	}
	for k, v := range page {
		query[k] = v
	}
	raw, err := c.Do(ctx, "GET", path, query, nil, nil)
	if err != nil {
		return nil, err
	}
	var response map[string]json.RawMessage
	if err := json.Unmarshal(raw, &response); err != nil {
		return nil, err
	}
	var next string
	var links struct {
		Next *string `json:"next"`
	}
	_ = json.Unmarshal(response["links"], &links)
	if links.Next != nil {
		next = *links.Next
	}
	if next == "" {
		_ = json.Unmarshal(response["next_page_url"], &next)
	}
	nextPage := url.Values{}
	if next != "" {
		u, err := url.Parse(next)
		base, _ := url.Parse(c.BaseURL)
		if err != nil || u.User != nil || u.Fragment != "" || u.Path != path || (u.IsAbs() && (u.Scheme != base.Scheme || u.Host != base.Host)) || (!u.IsAbs() && u.Host != "") {
			return nil, errors.New("API returned an invalid continuation")
		}
		for key, values := range u.Query() {
			if strings.HasPrefix(key, "page[") || key == "cursor" || key == "limit" {
				nextPage[key] = values
				continue
			}
			if strings.Join(values, "\x00") != strings.Join(filters[key], "\x00") {
				return nil, errors.New("API continuation changed the request context")
			}
		}
	} else if path == "/v1/listeners" {
		var listenerCursor string
		_ = json.Unmarshal(response["next_cursor"], &listenerCursor)
		if listenerCursor != "" {
			nextPage.Set("cursor", listenerCursor)
		}
	}
	response["next_cursor"] = json.RawMessage("null")
	if len(nextPage) > 0 {
		if !hasPosition(nextPage) {
			return nil, errors.New("API continuation has no page position")
		}
		for key, values := range page {
			if key == "page[size]" || key == "limit" {
				nextPage[key] = values
			}
		}
		if err := validatePage(nextPage); err != nil {
			return nil, err
		}
		encoded, err := json.Marshal(continuation{1, c.BaseURL, path, filters.Encode(), nextPage})
		if err != nil {
			return nil, err
		}
		response["next_cursor"], _ = json.Marshal(base64.RawURLEncoding.EncodeToString(encoded))
	}
	return json.Marshal(response)
}

func hasPosition(page url.Values) bool {
	return page.Get("page[number]") != "" || page.Get("page[cursor]") != "" || page.Get("cursor") != ""
}
