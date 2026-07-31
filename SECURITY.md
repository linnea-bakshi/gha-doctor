# Security Policy

## Supported versions

Only the latest release receives fixes. `gha-doctor` is a client-side CLI:
it reads workflow files and the GitHub API, and writes only when you pass
`--fix` (and even then only after re-validating the result).

## What counts as a security issue here

- `--fix` or `--diff` producing a workflow that does something the original
  didn't (beyond the documented fix).
- Report renderers (`--html`, `--md`, PR comments, SARIF) allowing content
  from scanned logs/workflows to inject markup or scripts.
- Token handling: any path where a token could leak into output, reports,
  or badge/score artifacts.
- The GitHub Action executing untrusted input.

## Reporting a vulnerability

Please use GitHub's private vulnerability reporting:
<https://github.com/linnea-bakshi/gha-doctor/security/advisories/new>

You should get a first response within a few days. Please don't open a
public issue for exploitable problems before a fix is released;
coordinated disclosure is appreciated and you'll be credited in the
release notes unless you prefer otherwise.
