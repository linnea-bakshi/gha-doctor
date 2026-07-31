package api

// Temporary corpus scan: GHA_DOCTOR_LOG_CORPUS=/dir go test -run TestScanLogCorpus -v
// Not part of CI; used to validate extractors against real downloaded logs.

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestScanLogCorpus(t *testing.T) {
	dir := os.Getenv("GHA_DOCTOR_LOG_CORPUS")
	if dir == "" {
		t.Skip("set GHA_DOCTOR_LOG_CORPUS to run")
	}
	files, _ := filepath.Glob(filepath.Join(dir, "*.log"))
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		got := parseTestFailures(string(b))
		fmt.Printf("%s: %d\n", filepath.Base(f), len(got))
		for i, tf := range got {
			if i >= 6 {
				fmt.Printf("    ... %d more\n", len(got)-6)
				break
			}
			n := tf.name
			if len(n) > 130 {
				n = n[:130] + "…"
			}
			fmt.Printf("    [%s] %s\n", tf.framework, n)
		}
	}
}
