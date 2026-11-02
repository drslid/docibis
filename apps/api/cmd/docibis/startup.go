package main

import (
	"net/url"
	"os"
	"slices"

	"docibis/internal/config"
	"docibis/internal/httpapi"
)

// startupSettings lists, for the configuration page, the settings read
// before the database opens. They change with docibis.yaml or the environment.
func startupSettings(cfg config.Config) []httpapi.StartupSetting {
	source := func(key, env string) httpapi.StartupSettingSource {
		switch {
		case cfg.IsLocked(key) || (env != "" && os.Getenv(env) != ""):
			return httpapi.StartupSettingSourceEnvironment
		case slices.Contains(cfg.FromFile, key):
			return httpapi.StartupSettingSourceFile
		}
		return httpapi.StartupSettingSourceDefault
	}
	item := func(key, label, help, value, env string) httpapi.StartupSetting {
		return httpapi.StartupSetting{Key: key, Label: label, Help: &help, Value: value, Source: source(key, env)}
	}
	return []httpapi.StartupSetting{
		item("listen", "Adresse d'écoute", "LISTEN_ADDR", cfg.Listen, "LISTEN_ADDR"),
		item("data_dir", "Dossier des données", "DATA_DIR", cfg.DataDir, "DATA_DIR"),
		item("state_url", "Copie continue de la base", "STATE_URL", redact(cfg.StateURL), "STATE_URL"),
		item("database_url", "Base PostgreSQL", "DATABASE_URL", redact(cfg.DatabaseURL), "DATABASE_URL"),
	}
}

// redact hides the password of an address.
func redact(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.User == nil {
		return raw
	}
	if _, ok := u.User.Password(); ok {
		u.User = url.UserPassword(u.User.Username(), "xxxxx")
	}
	return u.String()
}
