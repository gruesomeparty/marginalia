# Marginalia M1 — Implementation Design Spec

**Date:** 2026-07-03
**Status:** Approved (brainstorming) — ready for implementation planning
**Scope:** PRD Milestone **M1** (server mode for markdown) + full CI/CD + installable Claude plugin. M2–M5 deferred.
**Product source of truth:** `PRD.md`. This spec covers *how* M1 is built; it does not restate product rationale.

---

## 1. Decisions locked for this cycle

- **Module:** `github.com/suTerminus/marginalia`. Repo private to start; MIT `LICENSE` added when it goes public.
- **Go:** 1.26 (toolchain available: 1.26.3).
- **Dependency automation split:** Dependabot → GitHub Actions bumps + security updates; Renovate → Go modules (grouped, patch/minor automerge after green CI).
- **Coverage:** self-contained threshold gate, floor **80%**, table written to the Actions job summary; plus an optional Codecov upload step that runs only when `CODECOV_TOKEN` is present.
- **CI OS matrix:** Ubuntu (Linux) **and** macOS.
- **Conventional Commits** are mandatory — release-please (`release-type: go`) derives versions from them.
- **No Black Mirror source dependency.** Design is independent; Berkay supplies any reference input on request.

## 2. Non-negotiable invariants (from PRD — restated so implementers can't miss them)

1. Never mutate the source document. Review is read-only against the input file.
2. Feedback events are append-only. Never rewrite or delete a JSONL line. Later events override earlier ones only when *materializing a view*.
3. No export step in the local agent loop — every comment hits disk immediately in server mode.
4. Never use the Clipboard API. Server mode makes it unnecessary; do not introduce it.
5. Self-contained HTML in server mode too: inline CSS/JS, no external requests, system fonts only, CSP-safe.

## 3. Repository layout

```
marginalia/
  go.mod  go.sum                      module github.com/suTerminus/marginalia
  main.go                             thin — calls cmd.Execute()
  cmd/
    root.go                           cobra root; wires version; SilenceUsage on runtime errors
    serve.go                          marginalia serve <doc> [--port 8787] [--host 127.0.0.1] [--open]
    version.go                        marginalia version (ldflags: version/commit/date)
    errors.go                         advertise-request-feature error helper (PRD §8.1)
  internal/
    document/
      block.go                        Block type + Document type
      parse.go                        goldmark parse + AST walk -> []Block
      anchor.go                       section-path counters, quote extraction, hash
      parse_test.go  anchor_test.go   table-driven + testdata/ golden markdown
    feedback/
      event.go                        Event struct, type constants, validation
      store.go                        append-only JSONL: Append / Load
      resolve.go                      chronological per-block resolution (data model for M2)
      store_test.go                   append/load/concurrent-append/resolve
    server/
      server.go                       http.Server, router, graceful shutdown
      handlers.go                     GET / , GET /api/doc, POST/GET /api/feedback
      open.go                         --open: darwin `open`, linux `xdg-open`
      server_test.go                  httptest per endpoint + restore-from-disk + round-trip
    web/
      review.html.tmpl                go:embed template — self-contained page
      web.go                          embed.FS + render helpers
      render_test.go                  golden-file HTML render test
  skills/
    review-doc/SKILL.md               agent review workflow (PRD §5.5)
    request-feature/SKILL.md          agent feedback workflow (PRD §8.2)
  commands/
    review-doc.md                     /review-doc slash command (thin — points at the skill)
  .claude-plugin/
    plugin.json                       plugin manifest
    marketplace.json                  marketplace manifest (repo doubles as its own marketplace)
  .github/
    workflows/ci.yml                  lint + test(matrix) + coverage gate + snapshot build
    workflows/codeql.yml              CodeQL (Go), PR + weekly
    workflows/release.yml             release-please + goreleaser on release
    dependabot.yml                    github-actions + security
    ISSUE_TEMPLATE/agent-feedback.yml structured agent-feedback form
    ISSUE_TEMPLATE/config.yml
  docs/superpowers/specs/             this spec + design docs
  renovate.json
  .goreleaser.yaml
  release-please-config.json  .release-please-manifest.json
  .golangci.yml
  README.md   CLAUDE.md   PRD.md   .gitignore
```

## 4. `internal/document` — block anchoring

**Types**
```go
type Block struct {
    ID       string // "5.3/2" — SectionPath + "/" + Ordinal
    Section  string // "5.3"
    Ordinal  int    // 1-based within section, counts the heading too
    Kind     string // heading|paragraph|list|code|table|blockquote|thematic_break
    Level    int    // heading level (0 for non-heading)
    Quote    string // first ~90 chars of plain text, word-boundary + ellipsis
    Hash     string // sha256(normalized plain text)[:12]
    HTML     string // goldmark-rendered HTML of this block only
    PlainText string // extracted text (used for quote/hash/suggest_edit prefill)
}

type Document struct {
    Path   string
    Blocks []Block
}
```

**Algorithm**
1. Parse the file bytes with goldmark into an AST (CommonMark + GFM tables extension; keep source positions).
2. Walk **top-level** block children of the document node in order.
3. Section counters: keep `counters [7]int` indexed by heading level (1–6). On a heading of level *L*: increment `counters[L]`, zero all `counters[L+1..6]`, and reset the section ordinal to 0. `Section` = dot-join of `counters[1..L]` (e.g. `5.3`). Blocks appearing before any heading use section `"0"`.
4. For every top-level block (including the heading itself): `Ordinal++`; `ID = Section + "/" + Ordinal`. → first paragraph under `5.3` is `5.3/2` (heading is `5.3/1`), matching the PRD example.
5. `PlainText` = concatenated text of the node's descendants, whitespace-collapsed, trimmed.
6. `Quote` = first ≤90 chars of `PlainText`, cut on a word boundary, `…` appended if truncated.
7. `Hash` = `hex(sha256(PlainText))[:12]` (PlainText already normalized).
8. `HTML` = goldmark rendering of that single node.

**Edge cases with tests:** document with no headings; content before the first heading; deeply nested lists; fenced code (must not have its text word-collapsed for HTML, but plain-text/quote may); GFM tables; blockquotes; consecutive headings; empty document; a heading level jump (h1 → h3).

## 5. `internal/feedback` — event store

**Event** (JSON tags exactly match PRD §5.3):
```go
type Event struct {
    Doc    string `json:"doc"`
    Block  string `json:"block"`
    Quote  string `json:"quote"`
    Hash   string `json:"hash"`
    Type   string `json:"type"`   // comment|suggest_edit|question|approve|reject|review_done
    Text   string `json:"text"`
    Author string `json:"author"`
    Ts     string `json:"ts"`     // RFC3339 UTC
}
```
- **Append:** open `<doc>.feedback.jsonl` with `O_APPEND|O_CREATE|O_WRONLY`, marshal one line, write line+`\n` under an in-process `sync.Mutex`. Never truncates/rewrites.
- **Load:** read file, unmarshal each non-blank line; a malformed line is a hard error (the append-only guarantee means lines are always whole). Genuinely blank lines are skipped.
- **Resolve:** group by `Block`, sort by `Ts`, produce the latest state per block (used by M2; implemented now with tests so the data model is proven).
- `review_done` is an ordinary Event with `Type: "review_done"`, empty `Block`/`Hash`.
- Feedback path: the doc path with `.feedback.jsonl` appended verbatim — `specs/foo.md` → `specs/foo.md.feedback.jsonl` (matches the PRD example).

## 6. `internal/server` — HTTP

Routes (PRD §5.4), `net/http` + `http.ServeMux`:
- `GET /` → rendered review page.
- `GET /api/doc` → `{doc, blocks, events}` JSON so a reopened page restores state **from disk**, not localStorage.
- `POST /api/feedback` → validate + append one Event (incl. `review_done`); `201`.
- `GET /api/feedback` → all events as JSON array.

Behavior: load existing `<doc>.feedback.jsonl` on startup; re-render is stateless (reads doc each request is fine for M1). `--host` default `127.0.0.1` (set to a Tailscale IP for phone review — same server, no second build); `--port` default `8787`; `--open` launches the browser after the listener is up. Graceful shutdown on SIGINT/SIGTERM. Bind errors and unsupported doc types route through the `cmd/errors.go` helper.

## 7. `internal/web` — review page

Single `go:embed` template, server-rendered blocks + an inline `<script>window.__MARGINALIA__={doc,events}</script>` for hydration; `/api/doc` remains for programmatic reload. Requirements (PRD §5.1, §5.6): ~68ch serif column, sans headings, mono code, system fonts; each block focusable (`tabindex`), visible focus, click/Enter → composer with type chips (comment / suggest edit / question), "suggest edit" prefills `PlainText`; margin markers on commented blocks; sticky bar with live count + **Done**; `prefers-reduced-motion` respected; fully inline (CSP-safe); **no Clipboard API**. Vanilla JS only, no framework/CDN.

## 8. CLI & error routing

- `main.go` → `cmd.Execute()`.
- `version` prints ldflags-injected `version`, `commit`, `date`.
- `cmd/errors.go`: helper formatting advertise-on-error messages, e.g.
  `TOML is not supported yet — agents: invoke the marginalia:request-feature skill to file it (marginalia v%s).`
  Used for unsupported input extensions and unknown flags/subcommands (cobra's unknown-command output augmented).
- Runtime errors set `SilenceUsage`/`SilenceErrors` appropriately so error text is the advertise message, not a usage dump.

## 9. Testing strategy ("strict")

- **document:** table-driven for section paths, ordinals, quote truncation, hash stability; `testdata/*.md` + golden `*.blocks.json`.
- **feedback:** append/load round-trip; concurrent `Append` (goroutines + `-race`); malformed-line handling; resolve ordering.
- **server:** `httptest.Server` for every endpoint; POST then assert the JSONL line landed on disk; restore-from-disk on restart; unsupported-extension → advertise error.
- **web:** golden-file render (`-update` flag convention) asserting self-containment (no `http`/`src=`/`href=` to external hosts).
- **CLI:** `version` output; unknown subcommand advertise message.
- CI runs `go test -race -covermode=atomic -coverprofile=coverage.out ./...`; a small script enforces total coverage ≥ 80% and prints the per-package table to `$GITHUB_STEP_SUMMARY`.
- Lint: `golangci-lint` (govet, staticcheck, errcheck, ineffassign, unused, gofmt/gofumpt, misspell) via `.golangci.yml`; `gofmt -l` gate.

## 10. CI/CD

**`ci.yml`** (on `pull_request` + push to `main`):
- `lint` job: golangci-lint (Linux).
- `test` job: matrix `os: [ubuntu-latest, macos-latest]`, Go 1.26, `go test -race -coverprofile`; coverage-gate step (Linux only) enforces the 80% floor + job-summary table; optional Codecov upload `if: env.CODECOV_TOKEN != ''`.
- `build` job: `goreleaser build --snapshot --clean` to validate the release config on every PR.

**`codeql.yml`**: CodeQL for `go`, on PR + weekly schedule.

**`release.yml`** (push to `main`): `googleapis/release-please-action` maintains a release PR from Conventional Commits (`release-type: go`). When the release PR merges and a tag/release is created, a gated job runs `goreleaser release --clean` building darwin+linux (amd64+arm64) archives + `checksums.txt`, uploading to the GitHub Release. Uses the default `GITHUB_TOKEN`.

**`dependabot.yml`**: `github-actions` ecosystem weekly + security updates.
**`renovate.json`**: `gomod` manager, grouped minor/patch, `automerge` for patch (and minor) after CI passes, sensible schedule; disable its github-actions manager (Dependabot owns that).

**Release config:** `release-please-config.json` + `.release-please-manifest.json` (start `0.0.0`); `.goreleaser.yaml` (builds, archives, checksums, `main: .`, ldflags stamping `cmd.version/commit/date`).

## 11. Plugin packaging (install + usage)

- `.claude-plugin/plugin.json` — the plugin manifest (name `marginalia`, version, description, author). **Verify current schema against official Claude Code plugin docs before writing.**
- `.claude-plugin/marketplace.json` — lists the `marginalia` plugin with `source` this repo, so the repo is its own marketplace.
- `skills/review-doc/SKILL.md` — PRD §5.5 workflow: `marginalia serve <doc> --open` → tell the human the URL + the ask → watch `<doc>.feedback.jsonl` for `review_done` (fallbacks: quiescence / human says done) → read, group by block, address each event quoting `block`+`quote`, flag hash-stale → offer re-render. Includes a "binary not found → `go install github.com/suTerminus/marginalia@latest` (or download release)" preflight.
- `skills/request-feature/SKILL.md` — PRD §8.2 workflow: dedupe via `gh issue list --label agent-feedback` + search → file via the issue form → label `agent-feedback` + category (`format-support`/`customization`/`skill-gap`/`bug`). Guardrails: quote structure not content; ≤1 issue per limitation per session.
- `commands/review-doc.md` — thin `/review-doc <path>` entry that invokes the review-doc skill.
- **README** documents: install plugin (`/plugin marketplace add suTerminus/marginalia` → `/plugin install marginalia`), install binary (`go install …@latest` / release download), and quickstart (`marginalia serve README.md --open`).

## 12. Deferred (explicitly out of scope this cycle)

M2 revision loop (hash-stale UI, resolution view, re-render with prior comments) — data model is built now, UI is not. M3 JSON/YAML trees. M4 approved-for-agent automation. M5 static `export`/`import` share mode (the `export.go`/`import.go` subcommands exist only as advertise-request-feature stubs).

## 13. Open questions carried forward (PRD §10, not blocking M1)

Auto-applicable `suggest_edit` (leaning yes in M3); `serve --watch`; multi-document sessions.
