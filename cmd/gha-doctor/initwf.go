package main

import (
	"fmt"
	"os"
	"path/filepath"
)

// initWorkflow is the workflow --init writes. It must lint clean under
// gha-doctor's own rules — TestInitWorkflowLintsClean enforces that, so a
// new rule that would flag our own scaffold fails the build instead of
// shipping an embarrassment.
const initWorkflow = `name: gha-doctor

on:
  pull_request:
  workflow_dispatch:

permissions:
  contents: read

concurrency:
  group: gha-doctor-${{ github.ref }}
  cancel-in-progress: true

jobs:
  doctor:
    runs-on: ubuntu-latest
    timeout-minutes: 10
    permissions:
      contents: read
      pull-requests: write # sticky PR comment (remove with pr-comment below)
    steps:
      - uses: actions/checkout@v7
      - uses: linnea-bakshi/gha-doctor@v0
        with:
          baseline: auto # gate only on findings this PR introduces
          pr-comment: "true"
          summary: "true"
`

const initRelPath = ".github/workflows/gha-doctor.yml"

// runInit scaffolds a ready-to-commit adoption workflow in dir and exits.
// It never overwrites: an existing file is the user's to edit.
func runInit(dir string) {
	path := filepath.Join(dir, filepath.FromSlash(initRelPath))
	if _, err := os.Lstat(path); err == nil {
		fmt.Fprintf(os.Stderr, "%s already exists — edit it directly (or delete it and re-run --init)\n", path)
		os.Exit(1)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		fmt.Fprintln(os.Stderr, "init:", err)
		os.Exit(1)
	}
	if err := os.WriteFile(path, []byte(initWorkflow), 0o644); err != nil {
		fmt.Fprintln(os.Stderr, "init:", err)
		os.Exit(1)
	}
	fmt.Printf(`created %s

On every pull request it will:
  • lint the workflows, gating only on findings the PR introduces (baseline: auto)
  • post a sticky PR comment, inline annotations, and a job-summary report

Next:
  git add %s && git commit -m "ci: add gha-doctor"

Tweak: set pr-comment/summary to "false" to quiet it down, or pass extra
flags via args: — see https://github.com/linnea-bakshi/gha-doctor#use-as-a-github-action
`, path, initRelPath)
}
