// Package docs embeds the rules reference so the binary can explain rules
// offline (`gha-doctor --explain D004`).
package docs

import _ "embed"

// Rules is the full contents of docs/rules.md.
//
//go:embed rules.md
var Rules string
