package presentation

import "strings"

// groupPermissions shows permissions such as "projects:read" as one line per
// resource, for example "Projects: read, create".
func groupPermissions(data any) any {
	m, ok := data.(map[string]any)
	if !ok {
		return data
	}
	list, ok := m["permissions"].([]any)
	if !ok {
		return data
	}
	result := make(map[string]any, len(m))
	for key, value := range m {
		result[key] = value
	}
	if len(list) == 0 {
		result["permissions"] = "none"
		return result
	}
	groups := map[string]any{}
	for _, item := range list {
		permission, _ := item.(string)
		resource, action, found := strings.Cut(permission, ":")
		if !found {
			resource, action = "other", permission
		}
		if previous, ok := groups[resource].(string); ok {
			action = previous + ", " + action
		}
		groups[resource] = action
	}
	result["permissions"] = groups
	return result
}
