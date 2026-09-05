package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestErrorOutputPreservesFieldDetails(t *testing.T) {
	for _, body := range []string{
		`{"error":{"code":"VALIDATION_ERROR","message":"Invalid input","details":{"from":["Invalid sender"],"subject":["Required"]}}}`,
		`{"message":"Invalid input","errors":{"from":["Invalid sender"],"subject":["Required"]}}`,
	} {
		err := DecodeError(&http.Response{StatusCode: 422, Body: io.NopCloser(strings.NewReader(body))})
		var out bytes.Buffer
		if writeErr := WriteError(&out, fmt.Errorf("send failed: %w", err)); writeErr != nil {
			t.Fatal(writeErr)
		}
		var result struct {
			Error struct {
				Code, Message string
				Details       map[string][]string
			}
		}
		if decodeErr := json.Unmarshal(out.Bytes(), &result); decodeErr != nil {
			t.Fatal(decodeErr)
		}
		if result.Error.Code != "validation_failed" || result.Error.Details["from"][0] != "Invalid sender" || result.Error.Details["subject"][0] != "Required" || !strings.Contains(result.Error.Message, "send failed") || ExitCode(err) != 2 {
			t.Fatal(out.String())
		}
	}
}

func TestErrorOutputHasStableCodesAndOneJSONValue(t *testing.T) {
	for _, tc := range []struct {
		err  error
		code string
		exit int
	}{
		{errors.New("unknown flag\nwith newline"), "command_failed", 1},
		{context.Canceled, "command_failed", 130},
		{&CredentialError{Err: &Error{Status: 401, Code: "oauth_grant_expired", Message: "Expired"}}, "oauth_grant_expired", 3},
		{&Error{Status: 403, Code: "permission_denied", Message: "Denied"}, "permission_denied", 4},
		{&Error{Status: 429, Code: "rate_limited", Message: "Retry later"}, "rate_limited", 7},
		{&Error{Status: 503, Code: "oauth_unavailable", Message: "Retry later"}, "oauth_unavailable", 8},
	} {
		var out bytes.Buffer
		if err := WriteError(&out, tc.err); err != nil {
			t.Fatal(err)
		}
		decoder := json.NewDecoder(&out)
		var result struct{ Error map[string]any }
		if err := decoder.Decode(&result); err != nil || result.Error["code"] != tc.code || ExitCode(tc.err) != tc.exit {
			t.Fatalf("result=%v err=%v exit=%d", result, err, ExitCode(tc.err))
		}
		if err := decoder.Decode(new(any)); err != io.EOF {
			t.Fatal("extra error output", err)
		}
		if _, exists := result.Error["details"]; exists {
			t.Fatal("unexpected field details")
		}
	}
}
