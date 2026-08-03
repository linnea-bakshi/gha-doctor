// Package docs embeds the rules reference so the binary can explain rules
// offline (`gha-doctor --explain D004`).
package docs

import (
	_ "embed"
	"strings"
)

// Rules is the full contents of docs/rules.md.
//
//go:embed rules.md
var Rules string

func init() {
	// A source checkout on Windows may have rewritten rules.md to CRLF
	// (git autocrlf); every consumer splits on "\n", so normalize once here.
	Rules = strings.ReplaceAll(Rules, "\r\n", "\n")
}
