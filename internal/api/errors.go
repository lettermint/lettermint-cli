package api

import (
	"encoding/json"
	"errors"
	"io"
)

// WriteError writes one JSON object, including field errors when the API supplies them.
func WriteError(w io.Writer, err error) error {
	body := map[string]any{"code": ErrorCode(err), "message": err.Error()}
	var remote *Error
	if errors.As(err, &remote) && len(remote.Details) > 0 && json.Valid(remote.Details) {
		body["details"] = remote.Details
	}
	return json.NewEncoder(w).Encode(map[string]any{"error": body})
}
