package config

import (
	"os"
	"path/filepath"
	"testing"
)

func env(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

func TestDefaults(t *testing.T) {
	dir := t.TempDir()
	c, err := Load(env(map[string]string{"DATA_DIR": dir}))
	if err != nil {
		t.Fatal(err)
	}
	if c.Listen != ":8080" || c.LogLevel != "info" || c.StateURL != dir || len(c.Locked) != 0 {
		t.Fatalf("unexpected defaults: %+v", c)
	}
	if got, _ := c.ReplicaURL(); got != "file://"+filepath.Join(dir, "replica") {
		t.Fatalf("replica = %q", got)
	}
	if c.SQLitePath() != filepath.Join(dir, "docibis.db") {
		t.Fatalf("sqlite path = %q", c.SQLitePath())
	}
}

func TestRelativeDataDir(t *testing.T) {
	t.Chdir(t.TempDir())
	c, err := Load(env(map[string]string{"DATA_DIR": ".data"}))
	if err != nil {
		t.Fatal(err)
	}
	if !filepath.IsAbs(c.DataDir) || c.StateURL != c.DataDir {
		t.Fatalf("data dir must become absolute: %+v", c)
	}
}

func TestFileThenEnvironment(t *testing.T) {
	dir := t.TempDir()
	file := "instance_name: REC\nlog_level: debug\nstate_url: s3://bucket/rec/\n"
	if err := os.WriteFile(filepath.Join(dir, FileName), []byte(file), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := Load(env(map[string]string{"DATA_DIR": dir, "INSTANCE_NAME": "INT"}))
	if err != nil {
		t.Fatal(err)
	}
	if c.InstanceName != "INT" || !c.IsLocked("instance_name") {
		t.Fatalf("environment must win and lock: %+v", c)
	}
	if c.LogLevel != "debug" || c.IsLocked("log_level") {
		t.Fatalf("file value expected, unlocked: %+v", c)
	}
	if got, _ := c.ReplicaURL(); got != "s3://bucket/rec/db" {
		t.Fatalf("replica = %q", got)
	}
}

func TestInvalidValues(t *testing.T) {
	dir := t.TempDir()
	for name, e := range map[string]map[string]string{
		"state":    {"STATE_URL": "relative/dir"},
		"level":    {"LOG_LEVEL": "loud"},
		"database": {"DATABASE_URL": "mysql://x"},
		"bucket":   {"STATE_URL": "s3:///nobucket"},
	} {
		e["DATA_DIR"] = dir
		if _, err := Load(env(e)); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
}

func TestNoReplica(t *testing.T) {
	c, err := Load(env(map[string]string{"DATA_DIR": t.TempDir(), "STATE_URL": "none"}))
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := c.ReplicaURL(); got != "" {
		t.Fatalf("replica = %q, want none", got)
	}
}
