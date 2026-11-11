// Package apps holds the built-in app definitions embedded into the binary.
// The definitions are pure data; Go code only embeds and exposes them.
package apps

import "embed"

//go:embed *.toml
var FS embed.FS
