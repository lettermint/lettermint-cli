package api

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
)

// SupportedAPIVersions lists the capabilities API versions this CLI understands.
var SupportedAPIVersions = []string{"v1"}

type Capabilities struct {
	APIVersion  string   `json:"api_version"`
	Permissions []string `json:"permissions"`
}

// Supported reports whether this CLI understands the server's API version.
func (c Capabilities) Supported() bool { return slices.Contains(SupportedAPIVersions, c.APIVersion) }

// Capabilities returns the permissions of the login in its approved team.
// ok is false when the server does not provide the endpoint.
func (c *Client) Capabilities(ctx context.Context) (Capabilities, bool, error) {
	var v Response[Capabilities]
	b, err := c.Do(ctx, "GET", "/v1/capabilities", nil, nil, nil)
	var remote *Error
	if errors.As(err, &remote) && remote.Status == 404 {
		return Capabilities{}, false, nil
	}
	if err == nil {
		err = json.Unmarshal(b, &v)
	}
	if err != nil {
		return Capabilities{}, false, err
	}
	if v.Data.Permissions == nil {
		v.Data.Permissions = []string{}
	}
	return v.Data, true, nil
}

// PermissionError names the permissions a command needs after the API denies access.
type PermissionError struct {
	Err         error
	Permissions []string
}

func (e *PermissionError) Error() string { return e.Err.Error() }
func (e *PermissionError) Unwrap() error { return e.Err }
