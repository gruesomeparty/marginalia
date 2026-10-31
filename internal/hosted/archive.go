package hosted

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/gruesomeparty/marginalia/internal/identity"
	"github.com/gruesomeparty/marginalia/internal/storage"
)

// Archiving on demand. The sweep (limits.go) archives what has gone quiet;
// this is the same move, asked for: the reviewer archives or restores any
// review from the inbox, and an agent may withdraw one it published while
// the reviewer has not started on it. Nothing here deletes. An archived
// review is the whole tree moved under archive/, restorable byte for byte;
// removing it for good is the operator's business (a lifecycle rule on the
// bucket's archive/ prefix), so the store keeps having no delete.

// Archived is a review under archive/: its metadata, which version of it
// is the latest, and when it was archived.
type Archived struct {
	Meta Meta
	// version is the metadata version Meta was read from, which is what
	// open needs once the review is back under reviews/.
	version int
	// At is when it was archived; for a review archived before markers
	// existed, its last publish.
	At time.Time
}

// loadArchived lists archive/ once and reads each archived review's latest
// metadata, so the inbox can list them without touching the store again.
// The documents and logs are not read: nobody renders an archived review.
func (h *Host) loadArchived(ctx context.Context) error {
	keys, err := h.store.List(ctx, prefixArchive)
	if err != nil {
		return fmt.Errorf("hosted: listing the archive: %w", err)
	}
	latest := map[string]int{}
	at := map[string]time.Time{}
	for _, key := range keys {
		id, ok := idUnder(prefixArchive, key)
		if !ok {
			continue
		}
		if n := metaVersionUnder(prefixArchive, id, key); n > latest[id] {
			latest[id] = n
		}
		if t, ok := markTime(prefixArchive, id, markArchived, key); ok && t.After(at[id]) {
			at[id] = t
		}
	}
	for id, n := range latest {
		if h.reviews[id] != nil {
			// A move interrupted halfway leaves the id in both trees. It is
			// open, so it is not also listed as archived; the next archive
			// or restore of it finishes the move.
			continue
		}
		key := prefixArchive + strings.TrimPrefix(metaKey(id, n), prefixReviews)
		raw, err := h.store.GetBlob(ctx, key)
		if err != nil {
			h.logf("marginalia: archived review %s could not be read: %v\n", id, err)
			continue
		}
		var meta Meta
		if err := json.Unmarshal(raw, &meta); err != nil || meta.ID != id {
			h.logf("marginalia: archived review %s has unreadable metadata\n", id)
			continue
		}
		a := &Archived{Meta: meta, version: n, At: at[id]}
		if a.At.IsZero() {
			a.At, _ = time.Parse(time.RFC3339, meta.Updated)
		}
		h.archived[id] = a
	}
	return nil
}

// ArchivedReviews is every archived review, most recently archived first.
func (h *Host) ArchivedReviews() []*Archived {
	h.mu.RLock()
	out := make([]*Archived, 0, len(h.archived))
	for _, a := range h.archived {
		out = append(out, a)
	}
	h.mu.RUnlock()
	sort.Slice(out, func(i, j int) bool {
		if !out[i].At.Equal(out[j].At) {
			return out[i].At.After(out[j].At)
		}
		return out[i].Meta.ID < out[j].Meta.ID
	})
	return out
}

// archiveLocked moves one review out of the served set. The caller holds
// h.pub. The marker goes first, so it moves with the tree and the archive
// knows when; a move interrupted halfway is finished by the next one.
func (h *Host) archiveLocked(ctx context.Context, r *Review, by string) error {
	id := r.Meta.ID
	now := h.opts.Now().UTC()
	if err := h.mark(ctx, markKey(prefixReviews, id, markArchived, now), by); err != nil {
		return fmt.Errorf("hosted: archiving %s: %w", id, err)
	}
	if err := h.store.Move(ctx, prefixReviews+id+"/", prefixArchive+id+"/"); err != nil {
		return fmt.Errorf("hosted: archiving %s: %w", id, err)
	}
	h.mu.Lock()
	delete(h.reviews, id)
	h.archived[id] = &Archived{Meta: r.Meta, version: r.Meta.Revision, At: now}
	h.mu.Unlock()
	// The documents' bytes leave the budget here; the logs' left it in the
	// metering layer, as the move went through it.
	h.recordStored(-r.Meta.Bytes)
	h.logf("marginalia: review %s archived by %s (last activity %s)\n", id, by, lastActivity(r).Format(time.RFC3339))
	return nil
}

// mark writes a marker. One already at that key records the same moment,
// which is all a marker says, so it stands.
func (h *Host) mark(ctx context.Context, key, by string) error {
	if err := h.store.PutBlob(ctx, key, []byte(by)); err != nil && !errors.Is(err, storage.ErrExists) {
		return err
	}
	return nil
}

// Archive moves a review to the archive at the reviewer's request.
func (h *Host) Archive(ctx context.Context, id, by string) error {
	h.pub.Lock()
	defer h.pub.Unlock()
	r := h.Review(id)
	if r == nil {
		return fmt.Errorf("no review %q is open here", id)
	}
	return h.archiveLocked(ctx, r, by)
}

// Withdraw is an agent archiving a review it published, which it may do
// only while the reviewer has written nothing on it. After that the review
// is the reviewer's to put away: an agent may answer and claim, never
// decide, and taking a review out of someone's inbox mid-read is deciding.
func (h *Host) Withdraw(ctx context.Context, id, agent string) error {
	h.pub.Lock()
	defer h.pub.Unlock()
	r := h.Review(id)
	if r == nil {
		return fmt.Errorf("no review %q is open here — the id comes from publish_document", id)
	}
	if r.Meta.Agent != agent {
		return fmt.Errorf("review %s was published by another agent; only its publisher can withdraw it", id)
	}
	for _, e := range r.Server.Entries() {
		events, err := e.Store.Load()
		if err != nil {
			return fmt.Errorf("hosted: %s: %w", e.Store.Path(), err)
		}
		if len(events) > 0 {
			return fmt.Errorf("review %s cannot be withdrawn: the reviewer has started on it, so archiving it is theirs to do — publish a revision with review_id instead", id)
		}
	}
	return h.archiveLocked(ctx, r, "agent:"+agent)
}

// Restore brings an archived review back to the inbox, as it was. The
// restore counts as activity, so the next sweep does not archive it again.
func (h *Host) Restore(ctx context.Context, id, by string) (*Review, error) {
	h.pub.Lock()
	defer h.pub.Unlock()
	h.mu.RLock()
	a := h.archived[id]
	h.mu.RUnlock()
	if a == nil {
		return nil, fmt.Errorf("no archived review %q here", id)
	}
	now := h.opts.Now().UTC()
	if err := h.mark(ctx, markKey(prefixArchive, id, markRestored, now), by); err != nil {
		return nil, fmt.Errorf("hosted: restoring %s: %w", id, err)
	}
	if err := h.store.Move(ctx, prefixArchive+id+"/", prefixReviews+id+"/"); err != nil {
		return nil, fmt.Errorf("hosted: restoring %s: %w", id, err)
	}
	r, err := h.open(ctx, id, a.version)
	if err != nil {
		return nil, fmt.Errorf("hosted: restoring %s: %w", id, err)
	}
	r.Restored = now
	h.mu.Lock()
	delete(h.archived, id)
	h.reviews[id] = r
	h.mu.Unlock()
	h.recordStored(r.Meta.Bytes)
	h.logf("marginalia: review %s restored by %s\n", id, by)
	return r, nil
}

// handleArchive and handleRestore are the inbox's buttons. They are
// reviewer routes (requireHuman) behind cross-origin protection, since a
// form post is exactly what another site could forge. A form is answered
// with a redirect back to where it was posted from; anything else with
// JSON.
func (h *Host) handleArchive(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !ValidID(id) || h.Review(id) == nil {
		http.NotFound(w, r)
		return
	}
	if err := h.Archive(r.Context(), id, humanBy(r)); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	h.answerAction(w, r, id, "archived")
}

func (h *Host) handleRestore(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	h.mu.RLock()
	known := h.archived[id] != nil
	h.mu.RUnlock()
	if !ValidID(id) || !known {
		http.NotFound(w, r)
		return
	}
	if _, err := h.Restore(r.Context(), id, humanBy(r)); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	h.answerAction(w, r, id, "open")
}

func humanBy(r *http.Request) string {
	id, _ := identity.FromContext(r.Context())
	return "human:" + id.Name
}

func (h *Host) answerAction(w http.ResponseWriter, r *http.Request, id, state string) {
	if strings.HasPrefix(r.Header.Get("Content-Type"), "application/x-www-form-urlencoded") {
		http.Redirect(w, r, localBack(r.PostFormValue("back")), http.StatusSeeOther) //nolint:gosec // localBack admits only a path on this host
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(map[string]string{"id": id, "state": state})
}

// localBack is where a form returns to: a path on this host, or the inbox.
// Anything that could leave the origin — a scheme, "//host", a backslash
// some browsers read as a slash — is refused.
func localBack(back string) string {
	if back == "" || back[0] != '/' || (len(back) > 1 && (back[1] == '/' || back[1] == '\\')) || strings.ContainsRune(back, '\\') {
		return "/"
	}
	// A browser drops tabs and newlines from a URL before reading it, so
	// "/\t/host" would become "//host": no control byte gets through.
	for i := 0; i < len(back); i++ {
		if back[i] < 0x20 || back[i] == 0x7f {
			return "/"
		}
	}
	return back
}
