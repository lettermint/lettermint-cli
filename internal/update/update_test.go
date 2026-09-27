package update

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func fixture(t *testing.T, handler http.HandlerFunc) *Checker {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	return &Checker{
		CachePath: filepath.Join(t.TempDir(), "update.json"),
		HTTP:      server.Client(), Endpoint: server.URL,
		Now: func() time.Time { return time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC) },
		OS:  "linux", Arch: "amd64",
	}
}

func release(version, system, arch string) map[string]any {
	installer, suffix := "install.sh", ".tar.gz"
	if system == "windows" {
		installer, suffix = "install.ps1", ".zip"
	}
	assets := []map[string]any{}
	for _, name := range []string{
		fmt.Sprintf("lettermint_%s_%s_%s%s", version, system, arch, suffix),
		installer, "checksums.txt", "provenance.jsonl",
	} {
		assets = append(assets, map[string]any{"name": name, "state": "uploaded", "size": 100})
	}
	return map[string]any{"tag_name": "v" + version, "draft": false, "prerelease": false, "assets": assets}
}

func complete(t *testing.T, check *Check, success bool) string {
	t.Helper()
	if check == nil {
		t.Fatal("check did not start")
	}
	t.Cleanup(check.Stop)
	select {
	case <-check.done:
	case <-time.After(5 * time.Second):
		t.Fatal("check did not finish")
	}
	return check.Finish(success)
}

func TestStableVersionComparison(t *testing.T) {
	for _, tc := range []struct {
		candidate, current string
		want               bool
	}{
		{"1.10.0", "v1.9.9", true}, {"v2.0.0", "1.99.99", true},
		{"1.2.4", "1.2.3+build.1", true}, {"1.2.3+other", "1.2.3+build", false},
		{"1.2.3", "1.2.3", false}, {"1.2.3", "1.3.0", false},
		{"1.2.3-rc.1", "1.2.2", false}, {"1.2.3", "1.2.3-rc.1", false},
		{"1.2.3", "dev", false}, {"1.2.3", "1.2.2-SNAPSHOT", false},
		{"01.2.3", "1.2.2", false}, {"1.2", "1.0.0", false},
		{"1.2.3\n", "1.0.0", false}, {"1.2.3+", "1.0.0", false},
		{"999999999999999999999999.0.0", "9.0.0", true},
	} {
		t.Run(tc.candidate+"/"+tc.current, func(t *testing.T) {
			if got := newer(tc.candidate, tc.current); got != tc.want {
				t.Fatalf("newer = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestCompleteReleaseForEachPlatform(t *testing.T) {
	for _, system := range []string{"linux", "darwin", "windows"} {
		for _, arch := range []string{"amd64", "arm64"} {
			t.Run(system+"/"+arch, func(t *testing.T) {
				c := fixture(t, func(w http.ResponseWriter, r *http.Request) {
					if r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" {
						t.Error("release request contains credentials")
					}
					if r.Method != http.MethodGet || r.Header.Get("User-Agent") != "lettermint-cli-update-check" {
						t.Error("unexpected release request")
					}
					_ = json.NewEncoder(w).Encode(release("1.10.0", system, arch))
				})
				c.OS, c.Arch = system, arch
				if got := complete(t, c.Start(context.Background(), "1.9.0"), true); got != "1.10.0" {
					t.Fatalf("alert = %q", got)
				}
			})
		}
	}
}

func TestIncompleteAndNonStableReleases(t *testing.T) {
	for _, name := range []string{"archive", "installer", "checksums", "provenance", "uploading", "empty", "draft", "prerelease", "rc-tag", "invalid-tag", "wrong-platform"} {
		t.Run(name, func(t *testing.T) {
			data := release("1.2.0", "linux", "amd64")
			assets := data["assets"].([]map[string]any)
			switch name {
			case "archive", "installer", "checksums", "provenance":
				index := map[string]int{"archive": 0, "installer": 1, "checksums": 2, "provenance": 3}[name]
				data["assets"] = append(assets[:index:index], assets[index+1:]...)
			case "uploading":
				assets[0]["state"] = "new"
			case "empty":
				assets[0]["size"] = 0
			case "draft", "prerelease":
				data[name] = true
			case "rc-tag":
				data["tag_name"] = "v1.2.0-rc.1"
			case "invalid-tag":
				data["tag_name"] = "v1.2.0\n"
			case "wrong-platform":
				data = release("1.2.0", "windows", "arm64")
			}
			c := fixture(t, func(w http.ResponseWriter, _ *http.Request) { _ = json.NewEncoder(w).Encode(data) })
			if got := complete(t, c.Start(context.Background(), "1.0.0"), true); got != "" {
				t.Fatalf("unexpected alert: %s", got)
			}
		})
	}
}

func TestDailyChecksAndAlerts(t *testing.T) {
	var requests atomic.Int32
	c := fixture(t, func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		_ = json.NewEncoder(w).Encode(release("1.2.0", "linux", "amd64"))
	})
	now := c.Now()
	c.Now = func() time.Time { return now }
	if got := complete(t, c.Start(context.Background(), "1.0.0"), true); got != "1.2.0" {
		t.Fatal(got)
	}
	for _, elapsed := range []time.Duration{0, 23 * time.Hour, time.Hour} {
		now = now.Add(elapsed)
		check := c.Start(context.Background(), "1.0.0")
		want := ""
		if elapsed == time.Hour {
			want = "1.2.0"
		}
		if check.Cached != want || complete(t, check, true) != "" {
			t.Fatalf("cached alert = %q, want %q", check.Cached, want)
		}
	}
	if requests.Load() != 2 {
		t.Fatalf("requests = %d", requests.Load())
	}
	// An installed update suppresses the cached notice immediately.
	now = now.Add(24 * time.Hour)
	check := c.Start(context.Background(), "1.2.0")
	if check.Cached != "" || complete(t, check, true) != "" {
		t.Fatal("alert after upgrade")
	}
}

func TestFailureAndListenerSaveNoticeForNextRun(t *testing.T) {
	c := fixture(t, func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(release("1.2.0", "linux", "amd64"))
	})
	if got := complete(t, c.Start(context.Background(), "1.0.0"), false); got != "" {
		t.Fatal("failed command displayed a fresh alert")
	}
	check := c.Start(context.Background(), "1.0.0")
	if check.Cached != "1.2.0" || complete(t, check, true) != "" {
		t.Fatal("next run did not claim the saved alert")
	}
}

func TestCompletedFailuresConsumeDailyCheck(t *testing.T) {
	for _, kind := range []string{"http", "json", "timeout"} {
		t.Run(kind, func(t *testing.T) {
			var requests atomic.Int32
			c := fixture(t, func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				switch kind {
				case "http":
					w.WriteHeader(http.StatusTooManyRequests)
				case "json":
					_, _ = io.WriteString(w, "not json")
				case "timeout":
					<-r.Context().Done()
				}
			})
			if kind == "timeout" {
				c.HTTP.Timeout = 30 * time.Millisecond
			}
			for range 2 {
				if got := complete(t, c.Start(context.Background(), "1.0.0"), true); got != "" {
					t.Fatal(got)
				}
			}
			if requests.Load() != 1 || c.read().CheckedAt.IsZero() {
				t.Fatal("failure was not cached")
			}
		})
	}
}

func TestExitCancellationIsPromptAndDoesNotConsumeCheck(t *testing.T) {
	started := make(chan struct{}, 2)
	c := fixture(t, func(w http.ResponseWriter, r *http.Request) {
		started <- struct{}{}
		<-r.Context().Done()
	})
	for range 2 {
		check := c.Start(context.Background(), "1.0.0")
		<-started
		start := time.Now()
		if check.Finish(true) != "" || time.Since(start) > time.Second {
			t.Fatal("exit waited for the request timeout")
		}
		if !c.read().CheckedAt.IsZero() {
			t.Fatal("cancellation consumed the next check")
		}
	}
}

func TestConcurrentRunsSkipHeldLock(t *testing.T) {
	started, releaseResponse := make(chan struct{}), make(chan struct{})
	c := fixture(t, func(w http.ResponseWriter, r *http.Request) {
		close(started)
		select {
		case <-releaseResponse:
			_ = json.NewEncoder(w).Encode(release("1.2.0", "linux", "amd64"))
		case <-r.Context().Done():
		}
	})
	first := c.Start(context.Background(), "1.0.0")
	t.Cleanup(first.Stop)
	<-started
	if second := c.Start(context.Background(), "1.0.0"); second != nil {
		second.Stop()
		t.Fatal("concurrent check did not skip the held lock")
	}
	close(releaseResponse)
	if complete(t, first, true) != "1.2.0" {
		t.Fatal("missing alert")
	}
	third := c.Start(context.Background(), "1.0.0")
	if third.Cached != "" || complete(t, third, true) != "" {
		t.Fatal("duplicate alert")
	}
}

func TestCorruptCacheAndUnavailableCache(t *testing.T) {
	c := fixture(t, func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(release("1.2.0", "linux", "amd64"))
	})
	if err := os.WriteFile(c.CachePath, []byte("{"), 0600); err != nil {
		t.Fatal(err)
	}
	if complete(t, c.Start(context.Background(), "1.0.0"), true) != "1.2.0" {
		t.Fatal("did not recover from corrupt cache")
	}
	entries, _ := os.ReadDir(filepath.Dir(c.CachePath))
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), "update-") {
			t.Fatal("temporary cache file was not removed")
		}
	}
	c.CachePath = filepath.Join(c.CachePath, "not-a-directory", "update.json")
	if check := c.Start(context.Background(), "1.0.0"); check != nil {
		check.Stop()
		t.Fatal("started with unavailable cache")
	}
}

func TestInvalidBuildAndCanceledContextHaveNoSideEffects(t *testing.T) {
	c := fixture(t, func(http.ResponseWriter, *http.Request) { t.Error("unexpected request") })
	c.CachePath = filepath.Join(t.TempDir(), "not-created", "update.json")
	for _, version := range []string{"dev", "test", "1.0.0-rc.1", "1.0.0-SNAPSHOT"} {
		if c.Start(context.Background(), version) != nil {
			t.Fatal("check started for", version)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if c.Start(ctx, "1.0.0") != nil {
		t.Fatal("check started after cancellation")
	}
	if _, err := os.Stat(filepath.Dir(c.CachePath)); !os.IsNotExist(err) {
		t.Fatal("created cache directory")
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestRequestDeadline(t *testing.T) {
	c := fixture(t, func(http.ResponseWriter, *http.Request) { t.Error("unexpected server request") })
	c.HTTP = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		deadline, ok := r.Context().Deadline()
		if remaining := time.Until(deadline); !ok || remaining <= 0 || remaining > 3*time.Second {
			t.Error("request does not have a three-second deadline")
		}
		return nil, errors.New("offline")
	})}
	if complete(t, c.Start(context.Background(), "1.0.0"), true) != "" || c.read().CheckedAt.IsZero() {
		t.Fatal("offline check was not saved as a completed failure")
	}
}

func TestCacheWriteFailureReleasesLock(t *testing.T) {
	c := fixture(t, func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(release("1.2.0", "linux", "amd64"))
	})
	// A directory at the cache file path prevents atomic replacement on all OSes.
	if err := os.Mkdir(c.CachePath, 0700); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if got := complete(t, c.Start(context.Background(), "1.0.0"), true); got != "" {
			t.Fatal("claimed an alert without a writable cache")
		}
	}
}
