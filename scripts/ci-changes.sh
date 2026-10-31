#!/usr/bin/env bash
# Says which checks a pull request's changed files need, so CI runs only those.
#
#   gh api --paginate repos/R/pulls/N/files --jq '.[] | .filename, (.previous_filename // empty)' \
#     | scripts/ci-changes.sh >>"$GITHUB_OUTPUT"
#   scripts/ci-changes.sh --all >>"$GITHUB_OUTPUT"     # a push, a schedule: everything
#
# stdin is one path per line (a rename names both sides). Prints key=value
# lines, each true or false:
#
#   test        go test: anything that is not documentation. Tests read far
#               more than Go files (the workflows, the deploy template, the
#               plugin manifest, the goreleaser config), so this is the
#               inverse of a short list, not a list of what it reads.
#   go          lint and build: Go source and what it embeds (internal/,
#               cmd/), the module, the linter's config.
#   image       the hosted image: go, plus what builds and smoke-tests it.
#   cloudflare  the deploy template.
#   workflows   zizmor: anything under .github/.
#   deps        dependency review: the module, the template's npm lockfile,
#               or the workflows (it checks the actions they pin).
#   codeql      Go source and the module.
#   docs_only   every file is documentation a reader reads and no agent
#               follows: the review loop records a scripted pass instead of
#               running an agent. docs/, skills, the plugin's commands, agent
#               config, CLAUDE.md and the PRD are prose that steers agents or the
#               project, so they keep their review.
#
#   --steers    instead prints the steers pattern, for pick-model.sh.
#
# Fails open: no paths, or a change to CI itself (its workflows or this
# script), means every check runs. Documentation is a Markdown file or docs/,
# never testdata, which tests read, nor under internal/ or cmd/, which the
# binary may embed; site/ is the homepage, which only the
# Pages workflow builds.
set -euo pipefail

keys=(test go image cloudflare workflows deps codeql docs_only)

inert='(\.md$|^docs/|^site/)'
testdata='/testdata/'
ci_itself='^(\.github/workflows/(ci|codeql)\.yml|scripts/ci-changes\.sh)$'
go_src='(\.go$|^go\.(mod|sum)$|^(internal|cmd)/|^\.golangci)'
image_src='^(Dockerfile|\.goreleaser\.ya?ml|\.dockerignore|scripts/(image-smoke|trivy-new)\.sh)$'
cloudflare_src='^deploy/cloudflare/'
deps_src='^(go\.(mod|sum)|deploy/cloudflare/package(-lock)?\.json|\.github/(workflows|actions)/.*)$'
codeql_src='(\.go$|^go\.(mod|sum)$)'
# Markdown that agents follow (skills, the plugin's commands, agents and
# hooks, docs/ which CLAUDE.md sends them to) or that the merge gate guards:
# not docs_only.
steers='(^(docs|skills|commands|agents|hooks|\.claude|\.claude-plugin|\.github)/|(^|/)(CLAUDE|AGENTS|PRD|SECURITY)\.md$)'

all() {
  for k in "${keys[@]}"; do
    if [ "$k" = docs_only ]; then echo "$k=false"; else echo "$k=true"; fi
  done
  exit 0
}

[ "${1:-}" = --all ] && all
if [ "${1:-}" = --steers ]; then echo "$steers"; exit 0; fi
[ $# -eq 0 ] || {
  echo "usage: $0 [--all|--steers] <paths" >&2
  exit 2
}

paths=()
while IFS= read -r p || [ -n "$p" ]; do
  [ -n "$p" ] && paths+=("$p")
done
[ ${#paths[@]} -gt 0 ] || all

# One variable per key (no associative arrays: macOS ships bash 3).
for k in "${keys[@]}"; do printf -v "on_$k" false; done
docs=true

for p in "${paths[@]}"; do
  [[ "$p" =~ $ci_itself ]] && all
  doc=false
  # Markdown under internal/ or cmd/ may be embedded; it counts as code.
  if [[ "$p" =~ $inert ]] && ! [[ "$p" =~ $testdata ]] && ! [[ "$p" =~ ^(internal|cmd)/ ]]; then doc=true; fi
  if [ "$doc" = false ] || [[ "$p" =~ $steers ]] || [[ "$p" =~ ^site/ ]]; then docs=false; fi
  [ "$doc" = true ] && continue
  on_test=true
  [[ "$p" =~ $go_src ]] && on_go=true && on_image=true
  [[ "$p" =~ $image_src ]] && on_image=true
  [[ "$p" =~ $cloudflare_src ]] && on_cloudflare=true
  [[ "$p" =~ ^\.github/ ]] && on_workflows=true
  [[ "$p" =~ $deps_src ]] && on_deps=true
  [[ "$p" =~ $codeql_src ]] && on_codeql=true
done
on_docs_only=$docs

for k in "${keys[@]}"; do v="on_$k"; echo "$k=${!v}"; done
