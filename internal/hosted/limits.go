package hosted

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/gruesomeparty/marginalia/internal/feedback"
)

// Limits are the hard caps on what agents can publish, and how long a
// review is kept before it is archived. A hosted instance runs on a metered
// container with billed storage behind it, and a looping agent can grow
// both without limit; every cap here is enforced before anything is written
// or rendered, and every refusal names the limit and its value so an agent
// tells the human instead of retrying blindly. The defaults are
// conservative on purpose.
type Limits struct {
	// DocumentBytes caps one document.
	DocumentBytes int64
	// DocumentsPerPublish caps one publish's document set.
	DocumentsPerPublish int
	// RevisionsPerReview caps how often a review may be republished.
	RevisionsPerReview int
	// PublishesPerHour caps publishes per agent identity, sliding.
	PublishesPerHour int
	// OpenReviews caps reviews not yet marked done, instance-wide.
	OpenReviews int
	// StoredBytes caps what the instance holds across every review — tracked
	// in memory, never by listing the store. Past it, new publishes are
	// refused; a human's note never is.
	StoredBytes int64
	// RetainDone is how long a review stays after its last activity once
	// done; RetainOpen the same for one never marked done. After that it is
	// moved under archive/ — out of the inbox, never deleted.
	RetainDone, RetainOpen time.Duration
}

// DefaultLimits are the caps a host runs with when nothing else is said.
func DefaultLimits() Limits {
	return Limits{
		DocumentBytes:       512 << 10,
		DocumentsPerPublish: 20,
		RevisionsPerReview:  50,
		PublishesPerHour:    30,
		OpenReviews:         200,
		StoredBytes:         1 << 30,
		RetainDone:          30 * 24 * time.Hour,
		RetainOpen:          90 * 24 * time.Hour,
	}
}

// withDefaults fills the zero fields from DefaultLimits, so a caller setting
// one cap keeps the others.
func (l Limits) withDefaults() Limits {
	d := DefaultLimits()
	if l.DocumentBytes <= 0 {
		l.DocumentBytes = d.DocumentBytes
	}
	if l.DocumentsPerPublish <= 0 {
		l.DocumentsPerPublish = d.DocumentsPerPublish
	}
	if l.RevisionsPerReview <= 0 {
		l.RevisionsPerReview = d.RevisionsPerReview
	}
	if l.PublishesPerHour <= 0 {
		l.PublishesPerHour = d.PublishesPerHour
	}
	if l.OpenReviews <= 0 {
		l.OpenReviews = d.OpenReviews
	}
	if l.StoredBytes <= 0 {
		l.StoredBytes = d.StoredBytes
	}
	if l.RetainDone <= 0 {
		l.RetainDone = d.RetainDone
	}
	if l.RetainOpen <= 0 {
		l.RetainOpen = d.RetainOpen
	}
	return l
}

// LimitError is a refusal by a cap. It names the limit and its value, which
// is the whole point: "too large" sends an agent back to try smaller; "the
// limit on bytes per document is 524288 and spec.md is 700000" sends it to
// the human.
type LimitError struct {
	Limit string
	Value any
	Have  any
}

func (e *LimitError) Error() string {
	return fmt.Sprintf("limit %s is %v (this would be %v)", e.Limit, e.Value, e.Have)
}

func limited(limit string, value, have any) error {
	return &LimitError{Limit: limit, Value: value, Have: have}
}

// usage is what the host tracks in memory to enforce the caps that depend on
// history: how much is stored, and who published when.
type usage struct {
	mu        sync.Mutex
	stored    int64
	publishes map[string][]time.Time
}

// checkPublish enforces every cap that can be decided before a write. It
// does not record the publish — that happens once the write succeeded —
// so a refused publish does not count against the agent either.
func (h *Host) checkPublish(meta Meta, p Publish) error {
	l := h.limits
	if n := len(p.Docs); n > l.DocumentsPerPublish {
		return limited("documents per publish", l.DocumentsPerPublish, n)
	}
	var bytes int64
	for _, d := range p.Docs {
		if n := int64(len(d.Content)); n > l.DocumentBytes {
			return limited("bytes per document", l.DocumentBytes, fmt.Sprintf("%d in %s", n, d.Name))
		}
		bytes += int64(len(d.Content))
	}
	if meta.Revision > l.RevisionsPerReview {
		return limited("revisions per review", l.RevisionsPerReview, meta.Revision)
	}
	if meta.Revision == 1 {
		if open := h.openReviews(); open >= l.OpenReviews {
			return limited("open reviews", l.OpenReviews, open+1)
		}
	}
	h.use.mu.Lock()
	defer h.use.mu.Unlock()
	if h.use.stored+bytes > l.StoredBytes {
		return limited("stored bytes", l.StoredBytes, h.use.stored+bytes)
	}
	now := h.opts.Now()
	recent := h.recentPublishes(meta.Agent, now)
	if len(recent) >= l.PublishesPerHour {
		return limited("publishes per agent per hour", l.PublishesPerHour, len(recent)+1)
	}
	return nil
}

// recentPublishes prunes and returns one agent's publishes inside the
// window. Called with use.mu held.
func (h *Host) recentPublishes(agent string, now time.Time) []time.Time {
	kept := h.use.publishes[agent][:0]
	for _, t := range h.use.publishes[agent] {
		if now.Sub(t) < time.Hour {
			kept = append(kept, t)
		}
	}
	h.use.publishes[agent] = kept
	return kept
}

// recordPublish counts a publish that succeeded: its bytes against the
// budget, its moment against the agent's rate.
func (h *Host) recordPublish(agent string, bytes int64) {
	h.use.mu.Lock()
	defer h.use.mu.Unlock()
	h.use.stored += bytes
	h.use.publishes[agent] = append(h.recentPublishes(agent, h.opts.Now()), h.opts.Now())
}

// recordStored adds bytes that reached the store by another road — a log
// line — to the running total. Never a refusal: the budget refuses
// publishes, not the human's notes.
func (h *Host) recordStored(bytes int64) {
	h.use.mu.Lock()
	h.use.stored += bytes
	h.use.mu.Unlock()
}

// Stored is how many bytes the instance believes it holds.
func (h *Host) Stored() int64 {
	h.use.mu.Lock()
	defer h.use.mu.Unlock()
	return h.use.stored
}

// openReviews counts reviews not yet marked done on every document.
func (h *Host) openReviews() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	n := 0
	for _, r := range h.reviews {
		if !isDone(r) {
			n++
		}
	}
	return n
}

// isDone reports whether every document of a review carries a review_done.
func isDone(r *Review) bool {
	entries := r.Server.Entries()
	if len(entries) == 0 {
		return false
	}
	for i := range entries {
		events, err := entries[i].Store.Load()
		if err != nil {
			return false
		}
		done := false
		for _, e := range events {
			if e.Type == feedback.TypeReviewDone {
				done = true
				break
			}
		}
		if !done {
			return false
		}
	}
	return true
}

// lastActivity is the latest of a review's publish and its events.
func lastActivity(r *Review) time.Time {
	last, _ := time.Parse(time.RFC3339, r.Meta.Updated)
	for _, e := range r.Server.Entries() {
		events, err := e.Store.Load()
		if err != nil {
			continue
		}
		for _, ev := range events {
			if ts, err := time.Parse(time.RFC3339, ev.Ts); err == nil && ts.After(last) {
				last = ts
			}
		}
	}
	return last
}

// Sweep archives every review that has outlived its retention: moved whole
// under archive/ with the store's prefix move — nothing in it edited, nothing
// deleted, out of the inbox and restorable — and dropped from memory. It
// runs at startup and then hourly; a move interrupted halfway is finished by
// the next run, since Move is re-runnable.
func (h *Host) Sweep(ctx context.Context) (archived []string, err error) {
	now := h.opts.Now()
	h.mu.RLock()
	ids := make([]string, 0, len(h.reviews))
	for id := range h.reviews {
		ids = append(ids, id)
	}
	h.mu.RUnlock()
	sort.Strings(ids)
	for _, id := range ids {
		r := h.Review(id)
		if r == nil {
			continue
		}
		keep := h.limits.RetainOpen
		if isDone(r) {
			keep = h.limits.RetainDone
		}
		if now.Sub(lastActivity(r)) <= keep {
			continue
		}
		if err := h.archive(ctx, r); err != nil {
			return archived, err
		}
		archived = append(archived, id)
	}
	return archived, nil
}

// archive moves one review out of the served set.
func (h *Host) archive(ctx context.Context, r *Review) error {
	id := r.Meta.ID
	if err := h.store.Move(ctx, prefixReviews+id+"/", prefixArchive+id+"/"); err != nil {
		return fmt.Errorf("hosted: archiving %s: %w", id, err)
	}
	h.mu.Lock()
	delete(h.reviews, id)
	h.mu.Unlock()
	// The documents' bytes leave the budget here; the logs' left it in the
	// metering layer, as the move went through it.
	h.recordStored(-r.Meta.Bytes)
	h.logf("marginalia: review %s archived (last activity %s)\n", id, lastActivity(r).Format(time.RFC3339))
	return nil
}

// sweeper runs Sweep hourly until ctx is cancelled.
func (h *Host) sweeper(ctx context.Context) {
	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if _, err := h.Sweep(ctx); err != nil {
				h.logf("marginalia: sweep: %v\n", err)
			}
		}
	}
}

// describeLimits is the startup line.
func (h *Host) describeLimits() string {
	l := h.limits
	return strings.Join([]string{
		fmt.Sprintf("%d KiB/document", l.DocumentBytes>>10),
		fmt.Sprintf("%d documents/publish", l.DocumentsPerPublish),
		fmt.Sprintf("%d revisions/review", l.RevisionsPerReview),
		fmt.Sprintf("%d publishes/agent/hour", l.PublishesPerHour),
		fmt.Sprintf("%d open reviews", l.OpenReviews),
		fmt.Sprintf("%d MiB stored", l.StoredBytes>>20),
		fmt.Sprintf("archive done after %s, open after %s", l.RetainDone, l.RetainOpen),
	}, ", ")
}
