#!/usr/bin/env bash
# The merge loop's driver: for each pull request named, ask the gate what to
# do and do it — merge, ask the maintainer in Marginalia, or leave it.
#
# Usage (what .github/workflows/merge-prs.yml does):
#   REPO=owner/name OWNER=owner GH_TOKEN=… scripts/merge-loop.sh 170 171
#
# Environment:
#   REPO, OWNER          the repository and its owner (always trusted)
#   GH_TOKEN             reads, labels and comments; also merges unless
#   MERGE_TOKEN          is set, which then merges (a token whose merge
#                        triggers the push workflows on main)
#   MERGE_TRUSTED        more trusted authors, space-separated
#   MERGE_LOOP_AUTO      "true" lets low-risk PRs merge with no human
#   MARGINALIA_URL       a hosted Marginalia (marginalia host) and the bearer
#   MARGINALIA_MCP_TOKEN token for its /mcp; without both, a PR that needs the
#                        maintainer waits for a GitHub approval instead
#   IMPLEMENT_CHAIN      "true": a merged claude/* PR dispatches the work loop
#   DRY_RUN=1            decide and print, change nothing
#
# Every decision is scripts/merge-gate.sh's and every reading of the
# maintainer's review is scripts/merge-verdict.sh's; both are tested in
# internal/pipeline. This script only fetches their inputs and carries out
# what they say, so what it may do is: merge the exact commit the gate saw
# (--match-head-commit), publish a PR's diff for review, and edit its own
# labels (merge:human, merge:changes, merge:conflict) and its own comments.
#
# State lives on the PR, in one comment of this workflow's own, edited in place:
#   <!-- agent:merge v1
#   sha: <head the maintainer was asked about>
#   review: <Marginalia review id, empty without one>
#   published: <when that revision was published, RFC 3339 UTC>
#   state: asked | changes
#   -->
# Only a comment by github-actions[bot] counts: anyone can paste the marker.
set -euo pipefail

: "${REPO:?}" "${OWNER:?}" "${GH_TOKEN:?}"
here="$(cd "$(dirname "$0")" && pwd)"
bot="github-actions[bot]"
merge_mark="<!-- agent:merge v1"
verdict_mark="<!-- agent:merge-verdict v1"
marginalia=false
if [ -n "${MARGINALIA_URL:-}" ] && [ -n "${MARGINALIA_MCP_TOKEN:-}" ]; then marginalia=true; fi

run() {
  if [ -n "${DRY_RUN:-}" ]; then echo "  dry run: $*"; else "$@"; fi
}

label_add() { run gh api --method POST "repos/$REPO/issues/$1/labels" -f "labels[]=$2" >/dev/null; }
label_rm() {
  if jq -e --arg l "$2" '[.labels[].name] | index($l)' >/dev/null <<<"$3"; then
    run gh api --method DELETE "repos/$REPO/issues/$1/labels/$(jq -rn --arg l "$2" '$l | @uri')" >/dev/null
  fi
}

# This workflow's own comments on a PR, oldest first, as one JSON array.
comments() { gh api --paginate "repos/$REPO/issues/$1/comments" | jq -s --arg bot "$bot" '[add[] | select(.user.login == $bot)]'; }

# The newest of them that starts with the marker: its id, or its text.
own() { # number marker id|body
  comments "$1" | jq -r --arg m "$2" --arg f "$3" '[.[] | select(.body | startswith($m))] | last // {} | .[$f] // empty'
}

# Edits this workflow's comment carrying the marker in place, or posts it.
sticky() { # number marker body-file
  local id
  id="$(own "$1" "$2" id)"
  if [ -n "$id" ]; then
    run gh api --method PATCH "repos/$REPO/issues/comments/$id" -F body=@"$3" >/dev/null
  else
    run gh api --method POST "repos/$REPO/issues/$1/comments" -F body=@"$3" >/dev/null
  fi
}

field() { sed -n "s/^$1: //p" <<<"$2" | head -n1; }

gate_of() { # pr-json review-sha approved-sha
  jq -n --argjson pr "$1" --arg owner "$OWNER" --arg trusted "${MERGE_TRUSTED:-}" \
    --arg review_sha "$2" --arg approved_sha "$3" --arg auto "${MERGE_LOOP_AUTO:-}" \
    '{pr: $pr, owner: $owner, trusted: $trusted, review_sha: $review_sha, approved_sha: $approved_sha, auto: $auto}' |
    "$here/merge-gate.sh"
}

# A conflict with main is the author's to resolve, never this loop's: it
# labels the PR merge:conflict with one comment naming the head, and takes
# both away once the conflict is gone. Not for a PR the gate skips: Renovate
# and release-please rebase their own, and a draft is not ready.
conflict_mark="<!-- agent:merge-conflict v1"
conflicts() { # number pr-json
  local head mergeable labelled said
  head="$(jq -r .headRefOid <<<"$2")"
  mergeable="$(jq -r '.mergeable // "UNKNOWN"' <<<"$2")"
  [ "$mergeable" != UNKNOWN ] || return 0
  labelled=false
  if jq -e '[.labels[].name] | index("merge:conflict")' >/dev/null <<<"$2"; then labelled=true; fi
  if [ "$mergeable" = CONFLICTING ]; then
    # Skipped now (on hold, a draft) but labelled earlier: still conflicting.
    [ "$(gate_of "$2" "" "" | jq -r .decision)" != skip ] || return 0
    said="$(field sha "$(own "$1" "$conflict_mark" body)")"
    $labelled || label_add "$1" merge:conflict
    if ! $labelled || [ "$said" != "$head" ]; then
      echo "#$1: has a merge conflict with main"
      printf '%s\nsha: %s\n-->\n**Merge conflict with main** at %s: rebase onto main and push, and the merge loop picks it up again.\n' \
        "$conflict_mark" "$head" "${head:0:7}" >"$work/conflict.md"
      sticky "$1" "$conflict_mark" "$work/conflict.md"
    fi
  elif $labelled; then
    echo "#$1: the merge conflict is resolved"
    label_rm "$1" merge:conflict "$2"
    printf '%s\nsha: %s\n-->\n~~Merge conflict with main~~ resolved at %s.\n' \
      "$conflict_mark" "$head" "${head:0:7}" >"$work/conflict.md"
    sticky "$1" "$conflict_mark" "$work/conflict.md"
  fi
}

work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT

for n in "$@"; do
  [[ "$n" =~ ^[0-9]+$ ]] || continue
  # One PR GitHub will not describe must not end the sweep for the rest.
  if ! pr="$(gh pr view "$n" --repo "$REPO" --json number,title,body,author,baseRefName,headRefName,headRefOid,isDraft,isCrossRepository,labels,files,additions,deletions,mergeable,statusCheckRollup,latestReviews,state)"; then
    echo "::warning::#$n: could not read the PR; skipped"
    continue
  fi
  # GraphQL names a renamed file by its new path only; the REST listing also
  # gives the old one, so moving a file out of a sensitive directory still
  # counts as touching it. Without that listing the gate would decide on the
  # weaker one, so the PR waits for the next run instead.
  if ! files="$(gh api --paginate "repos/$REPO/pulls/$n/files" | jq -s '[add[] | {path: .filename} + (if .previous_filename then {previous_filename} else {} end)]')"; then
    echo "::warning::#$n: could not list the PR's files; skipped"
    continue
  fi
  pr="$(jq --argjson files "$files" '.files = $files' <<<"$pr")"
  [ "$(jq -r .state <<<"$pr")" = OPEN ] || continue
  head="$(jq -r .headRefOid <<<"$pr")"
  # GitHub computes mergeability when asked, so a PR read just after main
  # moved is often UNKNOWN; one more look usually settles it.
  if [ "$(jq -r '.mergeable // "UNKNOWN"' <<<"$pr")" = UNKNOWN ]; then
    sleep "${MERGEABLE_RETRY:-5}"
    if m="$(gh pr view "$n" --repo "$REPO" --json mergeable --jq .mergeable)"; then
      pr="$(jq --arg m "$m" '.mergeable = $m' <<<"$pr")"
    fi
  fi
  conflicts "$n" "$pr"

  review_comment="$(own "$n" "<!-- agent:review v1" body)"
  review_sha="$(field sha "$review_comment")"
  marker="$(own "$n" "$merge_mark" body)"
  asked_sha="$(field sha "$marker")"
  review_id="$(field review "$marker")"
  published="$(field published "$marker")"
  state="$(field state "$marker")"

  # The maintainer's approval of this exact head: a GitHub approval always
  # counts; a Marginalia "Merge it" counts when this head is what was asked.
  approved_sha="$(jq -r --arg o "$OWNER" --arg h "$head" \
    '[.latestReviews[]? | select(.author.login == $o and .state == "APPROVED" and .commit.oid == $h)] | if length > 0 then $h else "" end' <<<"$pr")"
  if [ -z "$approved_sha" ] && [ "$asked_sha" = "$head" ] && [ "$state" = asked ] &&
    [ -n "$review_id" ] && [ -n "$published" ] && $marginalia; then
    if events="$(jq -n --arg id "$review_id" '{review_id: $id}' | "$here/marginalia-call.sh" feedback_since | jq '[.docs[].events[]]')"; then
      verdict="$(jq -n --argjson events "$events" --arg at "$published" --argjson n "$n" \
        '{events: $events, published_at: $at, number: $n}' | "$here/merge-verdict.sh")" || {
        echo "::warning::#$n: could not read the maintainer's decision; skipped"
        continue
      }
      case "$(jq -r .decision <<<"$verdict")" in
        merge) approved_sha="$head" ;;
        changes)
          echo "#$n: the maintainer asked for changes"
          jq -r .comment <<<"$verdict" >"$work/verdict.md"
          sticky "$n" "$verdict_mark" "$work/verdict.md"
          printf '%s\n' "${marker/state: asked/state: changes}" >"$work/marker.md"
          sticky "$n" "$merge_mark" "$work/marker.md"
          label_add "$n" merge:changes
          label_rm "$n" merge:human "$pr"
          continue ;;
        *) echo "#$n: waiting on the maintainer's review"; continue ;;
      esac
    else
      echo "::warning::#$n: could not read its Marginalia review"
      continue
    fi
  fi

  gate="$(gate_of "$pr" "$review_sha" "$approved_sha")"
  decision="$(jq -r .decision <<<"$gate")"
  echo "#$n: $decision — $(jq -r '.reasons | join("; ")' <<<"$gate")"

  case "$decision" in
    merge)
      if ! GH_TOKEN="${MERGE_TOKEN:-$GH_TOKEN}" run gh pr merge "$n" --repo "$REPO" --squash --delete-branch --match-head-commit "$head"; then
        echo "::warning::#$n: the merge was refused; the next run tries again"
        continue
      fi
      {
        echo "$merge_mark"
        echo "sha: $head"
        echo "state: merged"
        echo "-->"
        echo "**Merged** by the merge loop: $(jq -r '.reasons | join("; ")' <<<"$gate")."
      } >"$work/marker.md"
      sticky "$n" "$merge_mark" "$work/marker.md"
      label_rm "$n" merge:human "$pr"
      label_rm "$n" merge:changes "$pr"
      # A merge made with the workflow token starts no push workflow, so
      # release-please would not see it; a dispatch is the one event that
      # token may start.
      if [ -z "${MERGE_TOKEN:-}" ]; then run gh workflow run release.yml --repo "$REPO" --ref main; fi
      if [ "${IMPLEMENT_CHAIN:-}" = true ] && [[ "$(jq -r .headRefName <<<"$pr")" == claude/* ]]; then
        run gh workflow run implement-approved.yml --repo "$REPO" --ref main
      fi
      ;;
    human)
      # Asked about this head already (and maybe answered): nothing new to ask.
      [ "$asked_sha" != "$head" ] || continue
      new_id="" url_note="approve the PR on GitHub"
      now="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
      if $marginalia && ! gh pr diff "$n" --repo "$REPO" >"$work/pr.diff"; then
        # GitHub refuses to serve some diffs (too large, too many files).
        echo "::warning::#$n: GitHub would not serve the diff; asking for a GitHub approval instead"
      elif $marginalia; then
        jq -rn --argjson pr "$pr" --argjson gate "$gate" --arg repo "$REPO" '
          def inert: tostring | gsub("[\r\n]+"; " ") | gsub("[<>`*_\\[\\]#|]"; "") | .[0:200];
          "# Merge #\($pr.number)?\n\n"
          + "\($pr.title | inert)\n\n"
          + "- **Branch:** `\($pr.headRefName | inert)` into `main`, commit `\($pr.headRefOid[0:7])`\n"
          + "- **Size:** +\($pr.additions) −\($pr.deletions) in \($pr.files | length) files\n"
          + "- **Pull request:** https://github.com/\($repo)/pull/\($pr.number)\n\n"
          + "## Why you are asked\n\n"
          + ($gate.reasons | map("- \(.)") | join("\n")) + "\n"' >"$work/cover.md"
        args="$(jq -n --arg n "$n" --arg title "$(jq -r '"Merge #\(.number): \(.title | .[0:80])"' <<<"$pr")" \
          --rawfile cover "$work/cover.md" --rawfile diff "$work/pr.diff" \
          --rawfile config "$here/../.github/merge-review.yaml" --arg id "$review_id" '
          {title: $title, config: $config,
           documents: [{name: "merge-\($n).md", content: $cover}, {name: "pr-\($n).diff", content: $diff}]}
          + (if $id != "" then {review_id: $id} else {} end)')"
        if [ -n "${DRY_RUN:-}" ]; then
          echo "  dry run: publish_document for #$n"; new_id="$review_id"
        elif out="$("$here/marginalia-call.sh" publish_document <<<"$args")"; then
          new_id="$(jq -r .review_id <<<"$out")"
          url_note="review it in Marginalia (it is in your inbox), or approve the PR on GitHub"
        else
          echo "::warning::#$n: publishing to Marginalia failed; asking for a GitHub approval instead"
        fi
      fi
      {
        echo "$merge_mark"
        echo "sha: $head"
        echo "review: $new_id"
        echo "published: $now"
        echo "state: asked"
        echo "-->"
        echo "**Needs the maintainer** before it merges ($(cut -c1-7 <<<"$head")):"
        echo
        jq -r '.reasons[] | "- \(.)"' <<<"$gate"
        echo
        echo "<sub>The merge loop merges this commit once the maintainer says so: $url_note. A push asks again.</sub>"
      } >"$work/marker.md"
      sticky "$n" "$merge_mark" "$work/marker.md"
      label_add "$n" merge:human
      label_rm "$n" merge:changes "$pr"
      ;;
    *) ;;
  esac
done
