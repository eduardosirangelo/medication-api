// Package migrations embeds the SQL migration files into the binary, so the
// container image needs no extra files and code and schema always ship together.
//
// Conventions: goose format, sequential numeric prefixes (00001_...), forward-
// only in production (Down sections exist for local development), and every
// migration must be safe to run while the previous app version is serving.
package migrations

import "embed"

// FS holds every *.sql file in this directory.
//
//go:embed *.sql
var FS embed.FS
