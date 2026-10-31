# Framing a review

The requesting agent decides what the review asks for, at serve time.

## Shipped framings

Writing a config from scratch every time means the same question gets a
different vocabulary each time, and the events stop comparing across runs.

```bash
marginalia review list                        # names and their vocabularies
marginalia review show security > review.yaml # copy one to disk as a start
marginalia serve api.proto --review security  # or just use it by name
```

| Framing | Asks | Adds to the built-ins |
|---|---|---|
| `adr` | Is this decision sound? | `blocker`, `concern`, `alternative`, `context_missing` |
| `schema` | Is this change safe to ship? | `breaking` (with *what breaks*), `naming`, `optionality`, `migration` |
| `security` | Read it as an attacker. | `vulnerability` (with severity), `hardening`, `threat_model`, `out_of_scope` |
| `copy` | It goes in front of users. | `wrong`, `unclear`, `tone`, `typo` |

A preset is a starting point, not a cage: `--config` layers a file over it and
the file changes only what it names. `instructions:` alone reframes a review
without touching its verbs; naming `actions:` replaces the vocabulary wholesale.

## Writing your own

```yaml
# review.yaml — marginalia serve spec.md --config review.yaml
title: Ingest spec — retry policy
instructions: |
  Focus on the retry policy in §1. Ignore prose and wording.
  Tag every finding: Blocker for anything that must change before rollout.

actions:
  - type: blocker        # written to the log verbatim
    label: Blocker       # button text
    key: b               # optional keyboard shortcut on a focused block
  - type: nit
    key: n
  - type: finding
    requires_text: true
    fields:
      - name: severity
        options: [high, medium, low]
        required: true
      - name: owner

builtins: true           # false drops comment/suggest_edit/question/approve/reject
require_verdict: false   # true: no review_done until every block is answered
complete_when: any       # all, or quorum:2 — when several reviewers' Dones finish the review
reviewers: [ana, bo]     # optional: who counts; with it, "all" means these people

readonly:
  - "2"                  # section 2 is context: shown, muted, not commentable
skip:
  - "3"                  # generated appendix: folded, and refused if posted to

notes:                   # your questions, rendered beside the block
  - id: batch-cap         # optional; what the reviewer's answer names
    doc: spec.md         # which document — required in a set, optional for one
    block: "1/2"
    text: why 500? I took it from the queue's batch limit — is that right?
```

The instructions render as a banner above the document, so the framing survives
the switch from chat to browser. A note renders beside its block, labelled as
coming from the requester, with an **Answer** button. The answer is a `reply`
event whose `reply_to` is the note's id — `req:batch-cap`, or `req:` plus a hash
of the document, block and text when you gave no `id` — so it comes back tied to
the question even when one block carries several.

Give each note a `doc` when the review serves a set — block ids are
per-document, so `1/2` exists in every markdown file and an unnamed note would
follow its id into all of them. Startup names any note whose document is not in
the set rather than dropping it. **Notes never enter `<doc>.feedback.jsonl`** —
that log is the human's answers, not your questions.

**The server enforces all of it.** An event whose `type` is not in the
configured set is refused; so is one for a read-only block, a field that was not
declared, or a choice outside its options. Rendering a rule is not enforcing it,
and a tab left open across a restart must not be able to invent vocabulary.
