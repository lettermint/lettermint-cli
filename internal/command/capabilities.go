package command

import (
	"context"
	"errors"
	"strings"

	"github.com/lettermint/lettermint-cli/internal/api"
)

// commandPermissions lists the team permissions the API checks for each
// command. Listener commands list the permissions for all default events.
var commandPermissions = map[string][]string{
	"projects list":                {"projects:read"},
	"projects get":                 {"projects:read"},
	"projects create":              {"projects:create"},
	"routes list":                  {"routes:read"},
	"routes get":                   {"routes:read"},
	"routes create":                {"routes:manage"},
	"routes update":                {"routes:manage"},
	"routes verify-inbound-domain": {"routes:manage"},
	"domains list":                 {"domains:read"},
	"domains get":                  {"domains:read"},
	"domains create":               {"domains:manage"},
	"domains verify":               {"domains:manage"},
	"domains assign":               {"domains:manage"},
	"messages list":                {"messages:read"},
	"messages get":                 {"messages:read"},
	"messages events":              {"messages:read"},
	"messages content":             {"messages:read", "messages:read_content"},
	"messages send":                {"messages:send"},
	"webhooks list":                {"webhooks:read"},
	"webhooks get":                 {"webhooks:read"},
	"webhooks create":              {"webhooks:manage"},
	"webhooks update":              {"webhooks:manage"},
	"webhooks test":                {"webhooks:manage"},
	"webhooks delete":              {"webhooks:delete"},
	"webhooks listen":              listenerPermissions,
	"listeners list":               {"webhooks:manage"},
	"listeners get":                listenerPermissions,
	"listeners stop":               listenerPermissions,
	"listeners replay":             listenerPermissions,
	"listeners secret":             listenerPermissions,
}

var listenerPermissions = []string{"webhooks:manage", "messages:read", "messages:read_content", "suppressions:read"}

// withPermissions names the permissions a command needs when the API denies it.
func withPermissions(command string, err error) error {
	var remote *api.Error
	if !errors.As(err, &remote) || remote.Status != 403 {
		return err
	}
	if permissions, ok := commandPermissions[command]; ok {
		return &api.PermissionError{Err: err, Permissions: permissions}
	}
	return err
}

// loginPermissions returns nil when the server cannot report permissions, so
// auth status keeps working with older servers.
func loginPermissions(ctx context.Context, client *api.Client) ([]string, error) {
	capabilities, ok, err := client.Capabilities(ctx)
	if errors.Is(err, context.Canceled) {
		return nil, err
	}
	if err != nil || !ok {
		return nil, nil
	}
	return capabilities.Permissions, nil
}

type capabilityCheck struct {
	Status          string `json:"status"`
	APIVersion      string `json:"api_version,omitempty"`
	PermissionCount *int   `json:"permission_count,omitempty"`
	Error           string `json:"error,omitempty"`
}

// checkCapabilities reports the capabilities check for doctor. Only a
// canceled command returns an error.
func checkCapabilities(ctx context.Context, client *api.Client) (capabilityCheck, []string, error) {
	capabilities, ok, err := client.Capabilities(ctx)
	switch {
	case errors.Is(err, context.Canceled):
		return capabilityCheck{}, nil, err
	case err != nil:
		return capabilityCheck{Status: "failed", Error: err.Error()}, nil, nil
	case !ok:
		return capabilityCheck{Status: "not available"}, nil, nil
	}
	count := len(capabilities.Permissions)
	check := capabilityCheck{Status: "available", APIVersion: capabilities.APIVersion, PermissionCount: &count}
	var warnings []string
	if !capabilities.Supported() {
		version := capabilities.APIVersion
		if version == "" {
			version = "(none)"
		}
		warnings = append(warnings, "API version "+version+" is not supported by this CLI, which supports "+strings.Join(api.SupportedAPIVersions, ", ")+". Update the CLI.")
	}
	return check, warnings, nil
}
