// Package config loads the startup configuration in three levels, from the
// weakest to the strongest: defaults, the bootstrap file in the data
// directory (written by the console or the docibis command), then environment
// variables. A setting fixed by an environment variable is "locked": the
// console shows it read-only.
package config

import (
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"go.yaml.in/yaml/v3"
)

// FileName is the bootstrap file, in the data directory.
const FileName = "docibis.yaml"

// Config is what DocIbis needs before it opens its database.
type Config struct {
	Listen       string `yaml:"listen,omitempty"`
	StateURL     string `yaml:"state_url,omitempty"`
	DatabaseURL  string `yaml:"database_url,omitempty"`
	InstanceName string `yaml:"instance_name,omitempty"`
	LogLevel     string `yaml:"log_level,omitempty"`

	// DataDir holds the SQLite file and the bootstrap file. Environment only.
	DataDir string `yaml:"-"`
	// Locked lists the settings fixed by environment variables.
	Locked []string `yaml:"-"`
	// FromFile lists the settings read from the bootstrap file.
	FromFile []string `yaml:"-"`
}

type setting struct {
	key, env string
	field    func(*Config) *string
}

var settings = []setting{
	{"listen", "LISTEN_ADDR", func(c *Config) *string { return &c.Listen }},
	{"state_url", "STATE_URL", func(c *Config) *string { return &c.StateURL }},
	{"database_url", "DATABASE_URL", func(c *Config) *string { return &c.DatabaseURL }},
	{"instance_name", "INSTANCE_NAME", func(c *Config) *string { return &c.InstanceName }},
	{"log_level", "LOG_LEVEL", func(c *Config) *string { return &c.LogLevel }},
}

// Load reads the three levels. getenv is os.Getenv outside tests.
func Load(getenv func(string) string) (Config, error) {
	c := Config{Listen: ":8080", LogLevel: "info", DataDir: "/data"}
	if v := getenv("DATA_DIR"); v != "" {
		c.DataDir = v
	}
	if abs, err := filepath.Abs(c.DataDir); err == nil {
		c.DataDir = abs
	}
	raw, err := os.ReadFile(filepath.Join(c.DataDir, FileName))
	switch {
	case err == nil:
		var f Config
		if err := yaml.Unmarshal(raw, &f); err != nil {
			return c, fmt.Errorf("%s: %w", FileName, err)
		}
		for _, s := range settings {
			if v := *s.field(&f); v != "" {
				*s.field(&c) = v
				c.FromFile = append(c.FromFile, s.key)
			}
		}
	case !errors.Is(err, fs.ErrNotExist):
		return c, fmt.Errorf("read %s: %w", FileName, err)
	}
	for _, s := range settings {
		if v := getenv(s.env); v != "" {
			*s.field(&c) = v
			c.Locked = append(c.Locked, s.key)
		}
	}
	if c.StateURL == "" {
		c.StateURL = c.DataDir
	}
	if _, err := c.ReplicaURL(); err != nil {
		return c, err
	}
	if _, err := c.SlogLevel(); err != nil {
		return c, err
	}
	if c.DatabaseURL != "" && !strings.HasPrefix(c.DatabaseURL, "postgres://") && !strings.HasPrefix(c.DatabaseURL, "postgresql://") {
		return c, errors.New("database_url must be a postgres:// or postgresql:// address")
	}
	return c, nil
}

// SQLitePath is the database file when no external database is set.
func (c Config) SQLitePath() string { return filepath.Join(c.DataDir, "docibis.db") }

// ReplicaURL is where Litestream copies the SQLite database: an S3 prefix, or
// a directory. "none" disables the copy (development only).
func (c Config) ReplicaURL() (string, error) {
	switch {
	case c.StateURL == "none":
		return "", nil
	case strings.HasPrefix(c.StateURL, "s3://"):
		u, err := url.Parse(c.StateURL)
		if err != nil || u.Host == "" {
			return "", fmt.Errorf("state_url %q: expected s3://bucket/prefix", c.StateURL)
		}
		u.Path = strings.TrimSuffix(u.Path, "/") + "/db"
		return u.String(), nil
	case strings.HasPrefix(c.StateURL, "file://"):
		return strings.TrimSuffix(c.StateURL, "/") + "/replica", nil
	case strings.HasPrefix(c.StateURL, "/"):
		return "file://" + filepath.Join(c.StateURL, "replica"), nil
	}
	return "", fmt.Errorf("state_url %q: expected s3://bucket/prefix, an absolute directory or none", c.StateURL)
}

// SlogLevel converts LogLevel.
func (c Config) SlogLevel() (slog.Level, error) {
	var l slog.Level
	if err := l.UnmarshalText([]byte(c.LogLevel)); err != nil {
		return l, fmt.Errorf("log_level %q: %w", c.LogLevel, err)
	}
	return l, nil
}

// IsLocked tells whether an environment variable fixes the setting.
func (c Config) IsLocked(key string) bool {
	for _, k := range c.Locked {
		if k == key {
			return true
		}
	}
	return false
}
