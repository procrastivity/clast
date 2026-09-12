// Package assets embeds the shipped default asset tree as the last-resort
// fallback the asset-resolution chain reaches for when neither a user
// override ($XDG_CONFIG_HOME/clast/) nor an installed share tree
// (<prefix>/share/clast/assets/) has the requested file — e.g. a bare
// `go run` with nothing installed. Assets are inputs, never state (C5.3):
// this package only ever reads the tree baked in at build time.
package assets

import "embed"

// FS holds every shipped asset file. New assets need no code change here:
// "all:*" matches any file added under this directory.
//
//go:embed all:*
var FS embed.FS
