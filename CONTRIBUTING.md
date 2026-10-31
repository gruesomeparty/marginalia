# Contributing

Thanks for looking. Bug reports, format requests and pull requests are all
welcome.

## Before you start

- [`PRD.md`](PRD.md) is the product spec and wins any disagreement.
  [`docs/design.md`](docs/design.md) lists the rules a change must not break:
  never mutate the source document, never rewrite the feedback log, keep every
  page self-contained, and an agent may answer but never decide.
- [`CLAUDE.md`](CLAUDE.md) is the architecture tour, written for coding agents
  but just as useful to people. It names the traps that are easy to fall into.
- For anything larger than a fix, open an issue first so the approach can be
  agreed before you write it.

## Setup

You need Go 1.26. Everything else is optional.

```bash
git clone https://github.com/gruesomeparty/marginalia.git
cd marginalia
go build ./...
go run . serve README.md --open
```

Optional tools:

- [golangci-lint](https://golangci-lint.run) v2.12.2, the version CI pins. An
  older build can exit with a version error that reads like success.
- Chrome or Chromium, for the browser tests in `internal/browsertest`. Without
  it those tests skip; `MARGINALIA_CHROME` names the binary.
- mermaid-cli, to see diagrams drawn. See
  [Drawing mermaid diagrams](docs/formats.md#drawing-mermaid-diagrams).

## Checks

| Command | What it does |
|---|---|
| `go test -race ./...` | All tests |
| `go test -run TestName ./internal/document/` | One test |
| `go test -coverprofile=coverage.out ./... && ./scripts/coverage.sh 80` | The coverage gate CI applies |
| `golangci-lint run` | Lint |
| `go run golang.org/x/vuln/cmd/govulncheck@v1.9.0 ./...` | Reachable vulnerabilities |

CI runs all of these, plus the browser tests (required on Linux) and an image
smoke test.

## Pull requests

1. Branch off `main` and keep a PR to one change.
2. Add tests with the code. A change to the page's JavaScript brings a browser
   test case. A change to the page's CSS speaks only in the design tokens of
   `internal/web/tokens.css`, which `tokens_test.go` enforces.
3. Title the PR as a [Conventional Commit](https://www.conventionalcommits.org),
   e.g. `feat(mermaid): anchor class diagrams`. `main` is squash-merged, so the
   title becomes the commit and release-please builds the changelog from it.
4. Update the docs in `docs/` when behaviour changes, and `CLAUDE.md` when the
   architecture does.

Every PR gets an automated review comment and a `review:*` label. The merge
loop then either merges it or asks the maintainer, depending on what it
touches; see "Agent feedback loop" in `CLAUDE.md` for the rules. Dependency
updates come from Renovate.

## Issues

Use an issue to report a bug or ask for a format or a feature. Agents file
them too: when the binary meets a format or flag it does not know, its error
message points at the `request-feature` skill, which files an
`agent-feedback` issue. A triage workflow labels, prioritises and sizes every
new issue.

Whoever writes it, an issue or PR **quotes the structure of a document, never
its content**. No secrets, no private text, no excerpts of the thing you were
reviewing.

## Security

Report vulnerabilities privately, as [SECURITY.md](SECURITY.md) describes, not
in a public issue.
