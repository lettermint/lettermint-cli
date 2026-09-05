package config

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/gofrs/flock"
	"github.com/zalando/go-keyring"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

type Profile struct {
	ConnectionID string `json:"connection_id"`
	APIURL       string `json:"api_url"`
	ClientID     string `json:"client_id"`
	UserID       string `json:"user_id"`
	TeamID       string `json:"team_id"`
	Project      string `json:"project,omitempty"`
	Route        string `json:"route,omitempty"`
}
type Config struct {
	Selected string             `json:"selected,omitempty"`
	Profiles map[string]Profile `json:"profiles"`
}
type Credentials struct {
	ConnectionID string    `json:"connection_id"`
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token"`
	ExpiresAt    time.Time `json:"expires_at"`
}
type Vault interface {
	Get(string) (Credentials, error)
	Set(string, Credentials) error
	Delete(string) error
}
type Keyring struct{}

var ErrCredentialsNotFound = keyring.ErrNotFound

func (Keyring) Get(name string) (Credentials, error) {
	var c Credentials
	raw, err := keyring.Get("lettermint-cli", name)
	if err == nil {
		err = json.Unmarshal([]byte(raw), &c)
	}
	return c, err
}
func (Keyring) Set(name string, c Credentials) error {
	b, err := json.Marshal(c)
	if err != nil {
		return err
	}
	return keyring.Set("lettermint-cli", name, string(b))
}
func (Keyring) Delete(name string) error { return keyring.Delete("lettermint-cli", name) }

type Store struct {
	Dir   string
	Vault Vault
}

func New() (*Store, error) {
	dir := os.Getenv("LETTERMINT_CONFIG_DIR")
	if dir == "" {
		root, err := os.UserConfigDir()
		if err != nil {
			return nil, err
		}
		dir = filepath.Join(root, "lettermint")
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	return &Store{Dir: dir, Vault: Keyring{}}, nil
}

var namePattern = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,64}$`)

func ValidateName(name string) error {
	if !namePattern.MatchString(name) {
		return errors.New("profile name must contain 1 to 64 letters, digits, underscores, or hyphens")
	}
	return nil
}
func (s *Store) Lock(ctx context.Context, name string) (func(), error) {
	if strings.ContainsAny(name, "/\\") || name == "" {
		return nil, errors.New("invalid lock name")
	}
	l := flock.New(filepath.Join(s.Dir, name+".lock"))
	ok, err := l.TryLockContext(ctx, 100*time.Millisecond)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, errors.New("could not lock configuration")
	}
	return func() { _ = l.Unlock() }, nil
}
func (s *Store) Read() (Config, error) {
	c := Config{Profiles: map[string]Profile{}}
	b, err := os.ReadFile(filepath.Join(s.Dir, "config.json"))
	if errors.Is(err, os.ErrNotExist) {
		return c, nil
	}
	if err != nil {
		return c, err
	}
	err = json.Unmarshal(b, &c)
	if c.Profiles == nil {
		c.Profiles = map[string]Profile{}
	}
	return c, err
}
func (s *Store) Update(ctx context.Context, f func(*Config) error) error {
	unlock, err := s.Lock(ctx, "config")
	if err != nil {
		return err
	}
	defer unlock()
	c, err := s.Read()
	if err != nil {
		return err
	}
	if err = f(&c); err != nil {
		return err
	}
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(s.Dir, "config-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err = tmp.Chmod(0600); err == nil {
		_, err = tmp.Write(b)
	}
	if err == nil {
		err = tmp.Sync()
	}
	closeErr := tmp.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(tmp.Name(), filepath.Join(s.Dir, "config.json"))
}
func (s *Store) Resolve(name string) (string, Profile, error) {
	c, err := s.Read()
	if err != nil {
		return "", Profile{}, err
	}
	if name == "" {
		name = c.Selected
	}
	p, ok := c.Profiles[name]
	if !ok {
		return "", p, fmt.Errorf("profile %q is not available; run auth login", name)
	}
	return name, p, ValidateName(name)
}
