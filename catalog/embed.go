// Package catalog embeds the bundled framework metadata and skill content.
// It exposes only the embedded filesystem; all logic lives in
// github.com/Novirmor/homonto/internal/catalog.
package catalog

import "embed"

//go:embed all:frameworks all:skills all:commands all:subagents all:plugins all:tooling version.txt
var FS embed.FS
