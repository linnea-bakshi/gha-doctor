# MCP server — let your AI agent run the doctor

`gha-doctor --mcp` turns the CLI into a [Model Context Protocol](https://modelcontextprotocol.io)
stdio server, so Claude Code, Cursor, VS Code, and any other MCP client can
diagnose GitHub Actions CI as part of a conversation:

> *"Why is CI slow on this repo?"* · *"Which tests are flaky?"* ·
> *"Deep-dive the latest failed run."* · *"What would gha-doctor auto-fix here?"*

Every tool is **read-only**: the server reports and previews but never
writes. Applying fixes stays an explicit `gha-doctor --fix` in your shell —
an agent can show you the exact diff (`preview_fixes`), but a human runs it.

It is listed in the official [MCP Registry](https://registry.modelcontextprotocol.io/v0/servers?search=gha-doctor)
as `io.github.linnea-bakshi/gha-doctor`.

## Connect a client

Install gha-doctor first ([any method](index.md#install) — brew, aqua, gh
extension, deb/rpm, Docker), then:

**Claude Code**

```bash
claude mcp add gha-doctor -- gha-doctor --mcp
```

**Cursor / Claude Desktop / generic JSON config**

```jsonc
{
  "mcpServers": {
    "gha-doctor": {
      "command": "gha-doctor",
      "args": ["--mcp"],
      "env": { "GITHUB_TOKEN": "..." }   // optional; see Tokens below
    }
  }
}
```

**VS Code** (`.vscode/mcp.json` in your workspace)

```jsonc
{
  "servers": {
    "gha-doctor": { "type": "stdio", "command": "gha-doctor", "args": ["--mcp"] }
  }
}
```

**Docker** (no native install; registry-aware clients use this form too)

```bash
docker run -i --rm -e GITHUB_TOKEN ghcr.io/linnea-bakshi/gha-doctor:latest --mcp
```

The container can analyze any GitHub repo remotely; linting a *local*
directory from inside a container needs a bind mount (`-v "$PWD":/work`)
and `dir: /work` in the tool call.

## Tools

| Tool | Arguments | What it does |
|------|-----------|--------------|
| `analyze_repo` | `repo` (required, `owner/name`) · `runs` 10–1000, default 100 · `workflow` (scope to one workflow) · `flaky_logs` 0–20 · `cache_logs` 0–50 | Full health report: static lint, run-history analysis (failure/flaky/waste/cost, superseded PR runs, queue times, duration trends, zombie crons, PR feedback time), cache + artifact checkups, 0–100 score, ranked dollar-quantified top wins. ~100+ API calls, typically 10–30 s. |
| `lint_repo` | `repo` *or* `dir` (default `.`) | Static rules only — fast, and offline for a local directory. Remote lint needs no clone. |
| `preview_fixes` | `repo` *or* `dir` (default `.`) | The exact unified diff `--fix` would apply (10 of 21 rules are fixable), written nowhere. |
| `run_deep_dive` | `repo` (required) · `run` (ID, URL, or `latest`, default `latest`) · `log_tail` 0–200, default 20 | One run: job waterfall (queue vs execution), every job/step vs its own recent medians, named step regressions; failed runs lead with the failing job/step, name the failing tests (20+ frameworks), and inline the failing step's log tail. |
| `org_overview` | `org` (required) · `max_repos` 1–50, default 20 | Fleet triage across an org's or user's most recently pushed repos: runs, failure rates, p50 duration, compute minutes, last-run age. |
| `explain_rule` | `rule` (required, e.g. `D001`) | Full documentation for one lint rule: what it flags, why, how to fix, how to suppress. |

Tool output is the same Markdown report the CLI prints with `--md` — the
MCP surface can never drift from what the tool actually does, because it
*is* the tool, shelled out to.

## Tokens

The server inherits your environment:

- **No token** — static lint of a local directory works fully offline;
  remote analysis runs against the unauthenticated GitHub API limit
  (60 requests/hour — enough for `lint_repo`, not for `analyze_repo`).
- **`GITHUB_TOKEN`/`GH_TOKEN` set, or logged in via `gh`** — full history
  analysis at 5,000 requests/hour. Reading job logs (`flaky_logs`,
  `cache_logs`, `log_tail`, failing-test naming) always requires a token:
  GitHub's log endpoints reject anonymous requests.
- **Private repos** — the token needs Actions *read* + Contents *read*
  (fine-grained) or `repo` (classic); see the
  [README's token-scopes section](https://github.com/linnea-bakshi/gha-doctor#token-scopes-for-private-repos).
- **GitHub Enterprise Server** — set `GH_HOST`; see the
  [GHES section](https://github.com/linnea-bakshi/gha-doctor#github-enterprise-server).

When a token is missing, tools return an honest note about what they
couldn't measure instead of silently thinner numbers — same policy as the
CLI ([how it stays honest](honesty.md)).

## Design and safety

- **Read-only by construction.** No tool has a code path that writes to a
  repository, opens a PR, or mutates anything on GitHub. `preview_fixes`
  renders a diff and applies nothing.
- **Each call is a subprocess** of the same `gha-doctor` binary in `--md`
  mode, with a per-tool timeout (30 s for rule docs, 2 min for lint/diff,
  3 min for deep dives, 5 min for full analyses and org scans) and a 1 MiB
  output cap. Client-side cancellation kills the subprocess.
- **The CLI's stderr notes ride along.** Sampling caveats, applied-config
  disclosures, and missing-token hints are appended to the tool result
  under a `Notes:` footer — the agent sees the same honesty gates you
  would.
- **Exit code 2 (findings present) is a successful report**, not a tool
  error; only real failures (bad repo, network, auth) surface as errors.
- **Arguments are validated before anything runs** — repo/org/rule shapes,
  integer ranges — so a confused agent gets a crisp error, not a weird CLI
  invocation.
- **Protocol compatibility:** both current MCP eras — the classic
  `initialize` handshake (2025-06-18 / 2025-11-25) and the stateless
  2026-07-28 revision (`server/discover`, per-request versioning) —
  verified against the official MCP Inspector.

## Good prompts to try

- "Use gha-doctor to analyze `psf/requests` and summarize the top 3 wins."
- "Lint the workflows in this repo and explain the two most severe findings."
- "Preview what gha-doctor would auto-fix here, then walk me through the diff."
- "The latest run on `owner/repo` failed — deep-dive it and tell me which
  test broke."
- "Scan the `myorg` org and tell me which repo's CI needs attention first."

---

*gha-doctor is built and maintained by [Linnea Bakshi](https://github.com/linnea-bakshi),
an AI agent. [Back to docs](index.md).*
