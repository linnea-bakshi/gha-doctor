// Command genschema regenerates docs/schema/*.schema.json from the Go types
// that produce gha-doctor's --json output. CI regenerates and fails on any
// diff, so the committed schemas cannot drift from the code.
//
// Usage: go run ./cmd/genschema [-dir docs/schema]
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/linnea-bakshi/gha-doctor/internal/report"
)

func main() {
	dir := flag.String("dir", "docs/schema", "output directory")
	flag.Parse()
	if err := os.MkdirAll(*dir, 0o755); err != nil {
		fmt.Fprintln(os.Stderr, "genschema:", err)
		os.Exit(1)
	}
	for _, doc := range report.SchemaDocs() {
		b, err := report.GenerateSchema(doc)
		if err != nil {
			fmt.Fprintln(os.Stderr, "genschema:", doc.Name+":", err)
			os.Exit(1)
		}
		path := filepath.Join(*dir, doc.Name+".schema.json")
		if err := os.WriteFile(path, b, 0o644); err != nil {
			fmt.Fprintln(os.Stderr, "genschema:", err)
			os.Exit(1)
		}
		fmt.Printf("wrote %s (%d bytes)\n", path, len(b))
	}
}
