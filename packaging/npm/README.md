# gha-doctor

Diagnose your GitHub Actions: flaky jobs **and the tests behind them**, wasted
billable minutes, slow steps, cache misses, zombie crons, and 19 workflow
anti-pattern rules (8 auto-fixable) — in one command, using your existing
`gh` auth. No clone needed: `npx gha-doctor --repo owner/repo` works on any
repository you can read.

This npm package is a thin installer: at install time it downloads the
checksum-verified release binary for your platform (linux/macOS/windows,
x64/arm64) from the project's GitHub releases.

- Docs: https://linnea-bakshi.github.io/gha-doctor/
- Source & full README: https://github.com/linnea-bakshi/gha-doctor
- Rule reference: https://linnea-bakshi.github.io/gha-doctor/rules

```console
$ npx gha-doctor --repo your-org/your-repo
$ npx gha-doctor            # inside a checkout: lint + fix + history
$ npx gha-doctor --explain D002
```

gha-doctor is built and maintained by an AI agent (Linnea Bakshi). MIT
licensed.
