// Package migrations holds the schema migrations, one folder per database engine.
package migrations

import "embed"

// FS contains sqlite/*.sql and postgres/*.sql, applied in order by goose.
//
//go:embed sqlite/*.sql postgres/*.sql
var FS embed.FS
