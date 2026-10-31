# Design and limits

## The rules the design is built on

These constraints explain most of Marginalia's choices, and none of them is
negotiable. [`PRD.md`](../PRD.md) gives the full reasoning.

- **The source document is never mutated.** Review is read-only against the
  input. `suggestions` reports; the caller applies. In hosted mode a published
  snapshot is written once, and a republish is a new revision beside it.
- **Feedback events are append-only.** Later events override earlier ones only
  when *materializing* a view. The log itself is immutable and replayed
  chronologically, and the storage layer has no method that could rewrite it.
- **No export step in the local loop.** In server mode every comment is on disk
  the instant it is saved. A flow that makes the human copy, download or paste
  is a failure, tolerated only in share mode, where there is no alternative.
- **Never depend on the Clipboard API.** Hosted and embedded contexts block it.
  Every copy affordance degrades to a pre-selected textarea.
- **Self-contained HTML in every mode.** Inline CSS and JS, system fonts, no
  external requests. Share mode must survive a strict CSP, which is why code is
  highlighted on the server and diagrams are inlined SVG.
- **Server mode is the product.** Share mode and hosted mode are the same page
  for people off your machine, not a second UI. The phone opens the same URL.
- **An agent may answer and claim, never decide.** On every surface the only
  events an agent can write are `reply` and `addressed`. `review_done`,
  `confirm` and `reopen` are the reviewer's, and the server checks who is
  asking.
- **No database.** Documents in, HTML out, JSONL beside the source.

## Limits

What Marginalia does not do today. The
[issue tracker](https://github.com/gruesomeparty/marginalia/issues) is the live
roadmap.

| Limit | Detail |
|---|---|
| **Source code is not an input yet** ([#31](https://github.com/gruesomeparty/marginalia/issues/31)) | Markdown, schemas, data files, diagrams, diffs, SQL and HCL are. Reviewing a source file block-per-symbol is the next format. |
| **Only some mermaid types decompose** | Flowchart, sequence, state and ER diagrams anchor per statement and draw clickable. Other types stay reviewable as one source block. |
| **`serve` trusts the network by default** | Without `MARGINALIA_SERVE_TOKEN`, anyone who can reach a `serve` URL can write to the log under any name. That is fine on `127.0.0.1` and a tailnet you trust; set the token ([how](reviewing.md#reviewing-on-your-phone)) when it is not. Review across machines with real identities is [hosted mode](hosting.md). |
| **One reviewer per review, mostly** ([#53](https://github.com/gruesomeparty/marginalia/issues/53)) | Hosted mode records who wrote each note and can admit a list of reviewers, but there is no story yet for two people disagreeing on one block. |
| **`suggest_edit` is reported, never applied** | By design Marginalia will not edit your document. `suggestions` says which edits are safe to apply verbatim; the agent applies them. |
| **Drawing needs Node** | `mmdc` is the only non-Go dependency anywhere near the tool. It is optional and degrades to anchored source. |
