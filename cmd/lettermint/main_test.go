package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"testing"
)

func TestJSONModeLocalFailure(t *testing.T) {
	process := exec.Command(os.Args[0], "-test.run=^TestCLIErrorProcess$")
	process.Env = append(os.Environ(), "LETTERMINT_TEST_ERROR_PROCESS=1")
	var stdout, stderr bytes.Buffer
	process.Stdout = &stdout
	process.Stderr = &stderr
	err := process.Run()
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 1 || stdout.Len() != 0 {
		t.Fatalf("error=%v stdout=%s stderr=%s", err, stdout.String(), stderr.String())
	}
	decoder := json.NewDecoder(&stderr)
	var result struct{ Error map[string]any }
	if err := decoder.Decode(&result); err != nil || result.Error["code"] != "command_failed" {
		t.Fatalf("result=%v error=%v", result, err)
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		t.Fatal("non-JSON text followed the error", err)
	}
}

func TestCLIErrorProcess(t *testing.T) {
	if os.Getenv("LETTERMINT_TEST_ERROR_PROCESS") != "1" {
		return
	}
	os.Args = []string{"lettermint", "--json", "unknown-command"}
	main()
}
