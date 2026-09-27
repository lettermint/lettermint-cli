// Package update checks for complete stable releases without changing the CLI.
package update

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"

	"github.com/gofrs/flock"
)

const interval = 24 * time.Hour

// Checker keeps release requests separate from the authenticated API client.
// Its fields allow tests to use a local server, temporary cache, and fixed clock.
type Checker struct {
	CachePath string
	HTTP      *http.Client
	Endpoint  string
	Now       func() time.Time
	OS, Arch  string
}

func New() *Checker {
	dir, err := os.UserCacheDir()
	if err != nil {
		return nil
	}
	return &Checker{
		CachePath: filepath.Join(dir, "lettermint", "update.json"),
		HTTP:      &http.Client{Timeout: 3 * time.Second},
		Endpoint:  "https://api.github.com/repos/lettermint/lettermint-cli/releases/latest",
		Now:       time.Now,
		OS:        runtime.GOOS,
		Arch:      runtime.GOARCH,
	}
}

type state struct {
	CheckedAt time.Time `json:"checked_at"`
	Latest    string    `json:"latest,omitempty"`
	AlertedAt time.Time `json:"alerted_at"`
}

// Check owns one worker. Only the caller may display Cached or Finish's result.
type Check struct {
	Cached  string
	checker *Checker
	current string
	cancel  context.CancelFunc
	done    chan struct{}
	ctx     context.Context
}

// Start claims any cached alert and starts a request only when a check is due.
// A held lock or an unavailable cache disables this invocation's check.
func (c *Checker) Start(ctx context.Context, current string) *Check {
	if c == nil || stable(current) == "" || ctx.Err() != nil {
		return nil
	}
	lock := c.lock()
	if lock == nil {
		return nil
	}
	s := c.read()
	checkCtx, cancel := context.WithCancel(ctx)
	check := &Check{checker: c, current: current, cancel: cancel, done: make(chan struct{}), ctx: ctx}
	check.Cached = c.claim(&s, current)
	if recent(s.CheckedAt, c.Now()) {
		_ = lock.Unlock()
		close(check.done)
		return check
	}
	go func() {
		defer close(check.done)
		defer lock.Unlock()
		requestCtx, stop := context.WithTimeout(checkCtx, 3*time.Second)
		defer stop()
		latest, err := c.fetch(requestCtx)
		// Exit cancellation must not consume the next invocation's check.
		// A request timeout is a completed failure and does consume it.
		if checkCtx.Err() != nil {
			return
		}
		s.CheckedAt = c.Now()
		if err == nil {
			s.Latest = latest
		}
		_ = c.write(s)
	}()
	return check
}

// Stop cancels network work and waits for cleanup, not for the request timeout.
func (c *Check) Stop() {
	if c != nil {
		c.cancel()
		<-c.done
	}
}

// Finish returns an unclaimed alert only after successful command completion.
func (c *Check) Finish(success bool) string {
	if c == nil {
		return ""
	}
	c.Stop()
	if !success || c.Cached != "" || c.ctx.Err() != nil {
		return ""
	}
	lock := c.checker.lock()
	if lock == nil {
		return ""
	}
	defer lock.Unlock()
	s := c.checker.read()
	return c.checker.claim(&s, c.current)
}

func (c *Checker) claim(s *state, current string) string {
	if !newer(s.Latest, current) || recent(s.AlertedAt, c.Now()) {
		return ""
	}
	s.AlertedAt = c.Now()
	if c.write(*s) != nil {
		return ""
	}
	return stable(s.Latest)
}

func recent(t, now time.Time) bool {
	return !t.IsZero() && !t.After(now) && now.Sub(t) < interval
}

func (c *Checker) lock() *flock.Flock {
	if c.CachePath == "" || os.MkdirAll(filepath.Dir(c.CachePath), 0700) != nil {
		return nil
	}
	lock := flock.New(c.CachePath + ".lock")
	ok, err := lock.TryLock()
	if err != nil || !ok {
		_ = lock.Close()
		return nil
	}
	return lock
}

func (c *Checker) read() state {
	f, err := os.Open(c.CachePath)
	if err != nil {
		return state{}
	}
	defer f.Close()
	var s state
	if json.NewDecoder(io.LimitReader(f, 64<<10)).Decode(&s) != nil {
		return state{}
	}
	return s
}

func (c *Checker) write(s state) error {
	f, err := os.CreateTemp(filepath.Dir(c.CachePath), "update-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	err = json.NewEncoder(f).Encode(s)
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(f.Name(), c.CachePath)
}

func (c *Checker) fetch(ctx context.Context) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.Endpoint, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "lettermint-cli-update-check")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("release request returned HTTP %d", resp.StatusCode)
	}
	var release struct {
		Tag        string `json:"tag_name"`
		Draft      bool   `json:"draft"`
		Prerelease bool   `json:"prerelease"`
		Assets     []struct {
			Name  string `json:"name"`
			State string `json:"state"`
			Size  int64  `json:"size"`
		} `json:"assets"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&release); err != nil {
		return "", err
	}
	version := stable(release.Tag)
	if release.Draft || release.Prerelease || version == "" || release.Tag != "v"+version {
		return "", nil
	}
	installer, extension := "install.sh", ".tar.gz"
	if c.OS == "windows" {
		installer, extension = "install.ps1", ".zip"
	}
	required := map[string]bool{
		fmt.Sprintf("lettermint_%s_%s_%s%s", version, c.OS, c.Arch, extension): false,
		installer: false, "checksums.txt": false, "provenance.jsonl": false,
	}
	for _, asset := range release.Assets {
		if _, ok := required[asset.Name]; ok && asset.State == "uploaded" && asset.Size > 0 {
			required[asset.Name] = true
		}
	}
	for _, found := range required {
		if !found {
			return "", nil
		}
	}
	return version, nil
}

// Only stable semantic versions are eligible. Build metadata has no precedence.
var stableVersion = regexp.MustCompile(`^v?((?:0|[1-9][0-9]*)\.(?:0|[1-9][0-9]*)\.(?:0|[1-9][0-9]*))(?:\+[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*)?$`)

func stable(version string) string {
	match := stableVersion.FindStringSubmatch(version)
	if match == nil {
		return ""
	}
	return match[1]
}

func newer(candidate, current string) bool {
	a, b := stable(candidate), stable(current)
	if a == "" || b == "" {
		return false
	}
	x, y := strings.Split(a, "."), strings.Split(b, ".")
	for i := range x {
		// Compare decimal components without integer overflow.
		if len(x[i]) != len(y[i]) {
			return len(x[i]) > len(y[i])
		}
		if x[i] != y[i] {
			return x[i] > y[i]
		}
	}
	return false
}
