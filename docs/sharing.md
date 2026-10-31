# Sharing off your machine

Server mode needs no export step — but it needs the reviewer to reach your
machine. When they cannot:

```bash
marginalia export spec.md -o review.html    # one self-contained file
# send review.html; they comment in their browser and send back a .json
marginalia import their-review.json         # merges onto spec.md.feedback.jsonl
```

The shared page loads **nothing**: inline CSS and JS, and every image reference
in the document is rewritten to say what it was rather than fetch it, so opening
the file makes no request at all. Comments live in that browser
(`localStorage`) until the reviewer hands them over through a pre-selected
textarea — no Clipboard API, which hosted contexts block — or the download
button, which says so if the viewer refuses it.

`import` is append-only and idempotent: it skips events already in the log, so
merging the same file twice changes nothing, and a malformed file is refused
before anything is written. A reply whose target is in neither the incoming file
nor the existing log is refused too — that is the last moment the mismatch can
be reported to someone who still has the review it answers. Feedback already on
disk travels with the exported page, so a second round shows the thread so far.

What the reviewer sends back names the document and the sha256 of the file it
was exported from. `import --doc` can say where a document moved to, but a reply
to a different document is refused, naming both. A reply to an earlier revision
of the same document is merged with a warning: its notes re-anchor, and those on
blocks that changed read as stale.

If the reviewer should be able to come back to a review from anywhere, and
the agent shares no filesystem with you either, [hosted mode](hosting.md) is
the always-on alternative.
