# Reviewing a document

You get a page. Click a block, pick a verdict, done. What the verdicts are, and
which parts you are asked about, is up to the agent that asked
([framing a review](review-config.md)).

- **One tap where possible.** An action with nothing to fill in saves on the
  tap. Most feedback is a verdict, not prose, and a review where saying "nit"
  means typing is a review that gets abandoned. Those one-tap actions sit
  apart in the composer as one compact control, Approve in green and Reject
  in red (always labelled, never colour alone), so a verdict no longer looks
  like the start of a comment.
- **Keyboard.** `Enter` on a focused block opens the composer. A configured
  action's key writes it without one. And on a long document:

  | | |
  |---|---|
  | `↓` / `↑` | next / previous block |
  | `⇧↓` / `⇧↑` | next / previous **unanswered** block, wrapping |
  | `j` `k` `n` `p` | the same, when the review has not claimed those letters |
  | `i` | back to the inbox, on a hosted review (also the header's **← Inbox** link) |
  | `?` | what is actually bound right now |

  A configured action always wins its key — the agent asked for that
  vocabulary. That is why the arrows exist: a configured key is a single
  character, so `ArrowDown` can never be taken.

- **Comment on a sentence, not the paragraph.** Select text inside a block and
  an offer appears; take it and the note is about that sentence, which the page
  marks in the prose. Ignore it and selecting text is exactly what it always
  was — you can still copy a quote out, and clicking a block still comments on
  the block.
- **Progress that means something.** The header reads *answered of
  commentable*, not a raw comment count, because only that ratio says how much
  is left:

  ```
  12 of 61 answered · 3 of 9 sections · 12 comments · 3 to re-check
  ```

  On a long document the bar barely moves, so the page also groups blocks by
  top-level section: an outline in the left column lists each one with how
  many of its blocks are answered, ticks it when all of them are, and takes
  you there on a click. That is a markdown document's outermost chapters (a
  lone title over H2 chapters groups by chapter), a diff's files, and a
  tree's top-level nodes. The rule for what counts as answered is the same.

- **Filters** for how you actually work: not answered, ones you answered, ones
  the requester asked about, ones waiting to be re-checked. The menu label says
  how many blocks are out of view, so a filter is never a silent lie. An inline
  block — a list item, a diagram statement — dims instead of hiding, because
  removing a bullet renumbers the list around it.
- **Done is a summary, not a click.** It opens a sheet that shows what you
  said (counts by type), how many blocks you left unanswered, and a box for an
  optional overall note. Nothing is written until you press **Submit**. Cancel
  or Esc leaves the log untouched. With `require_verdict` the sheet lists the
  blocks still waiting, takes you to any of them, and will not submit until
  they are answered. The overall note becomes the `text` of the `review_done`
  event, so the agent reads it alongside the signal to go.
- **Your display is yours.** The **Display** menu picks System / Light / Dark
  and turns code ligatures off. Those choices live in your browser, never on
  the agent's disk. `--theme` picks the palette it arrives in: `default`,
  `light`, `dark`, `catppuccin`, `catppuccin-latte`, `catppuccin-mocha`.

## Reviewing on your phone

There is no second UI. The server binds where you tell it, so bind it to your
Tailscale interface and open the same URL:

```bash
marginalia serve doc.md --host 100.x.y.z
```

Know what that means: `serve` has no login. Anyone who can reach the port can
read the document and write notes under any name. On loopback that is only you.
On a tailnet it is everyone on the tailnet. When the network is not the boundary
you want, set a token. It comes from the environment, never a flag, because flags
show up in process lists:

```bash
export MARGINALIA_SERVE_TOKEN="$(openssl rand -hex 16)"
marginalia serve doc.md --host 100.x.y.z
# open http://100.x.y.z:8787/?token=<the token> once per browser
```

Every request then needs the token. A browser passes it once as `?token=`, which
leaves a cookie and drops the token from the address bar; a script sends it as
`Authorization: Bearer`. Every note is written under `--author` (default
`$USER`), whatever the page says. Without the variable, nothing changes.
