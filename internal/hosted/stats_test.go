package hosted

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gruesomeparty/marginalia/internal/feedback"
	"github.com/gruesomeparty/marginalia/internal/storage"
	"github.com/gruesomeparty/marginalia/internal/storage/storagetest"
	"github.com/gruesomeparty/marginalia/internal/web"
)

// The acceptance criterion of issue #216: two agents, several finished
// reviews; /api/stats returns per-agent numbers that match a hand count
// from the logs, and neither the page nor the API reads the store.
func TestStatsMatchAHandCount(t *testing.T) {
	t0 := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	clock := t0
	counting := storagetest.NewCounting(storage.NewMemory())
	h, err := Open(context.Background(), Options{
		Store: counting, PublicURL: "https://review.example", Token: "tok", DevIdentity: "dev@example.com",
		Log: testWriter{t}, Now: func() time.Time { return clock },
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	pub := func(agent string, at time.Time) *Review {
		clock = at
		r, err := h.Publish(ctx, Publish{Title: "t", Agent: agent, Docs: []Document{{Name: "spec.md", Content: []byte(specDoc)}}})
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	add := func(id string, e feedback.Event, at time.Time) feedback.Event {
		e.Doc, e.Ts = "spec.md", at.Format(time.RFC3339)
		if e.Author == "" {
			e.Author = "dev@example.com"
		}
		if err := h.Review(id).Server.Entries()[0].Store.Append(e); err != nil {
			t.Fatal(err)
		}
		return e
	}
	hash := func(id, block string) string {
		for _, b := range h.Review(id).Server.Entries()[0].Doc.Blocks {
			if b.ID == block {
				return b.Hash
			}
		}
		t.Fatalf("no block %s", block)
		return ""
	}

	// Agent a, review 1: looked at after 1h, done after 3h, republished,
	// the agent claims a fix, the reviewer confirms it and finishes again.
	// Two rounds.
	a1 := pub("agent:a", t0).Meta.ID
	note := add(a1, feedback.Event{Block: "1/2", Type: "comment", Text: "n", Hash: hash(a1, "1/2")}, t0.Add(time.Hour))
	add(a1, feedback.Event{Block: "1.1/2", Type: "question", Text: "q", Hash: hash(a1, "1.1/2")}, t0.Add(2*time.Hour))
	add(a1, feedback.Event{Type: "review_done"}, t0.Add(3*time.Hour))
	clock = t0.Add(4 * time.Hour)
	if _, err := h.Republish(ctx, a1, Publish{Docs: []Document{{Name: "spec.md", Content: []byte(specDoc)}}}); err != nil {
		t.Fatal(err)
	}
	add(a1, feedback.Event{Block: "1/2", Type: "addressed", Text: "did", ReplyTo: feedback.NoteID(note), Author: "agent:a", Hash: note.Hash}, t0.Add(4*time.Hour))
	add(a1, feedback.Event{Block: "1/2", Type: "confirm", ReplyTo: feedback.NoteID(note)}, t0.Add(5*time.Hour))
	add(a1, feedback.Event{Type: "review_done"}, t0.Add(6*time.Hour))

	// Agent a, review 2: looked at after 30m, done after 1h. One round.
	a2 := pub("agent:a", t0.Add(24*time.Hour)).Meta.ID
	// The agent then claims a fix the text does not show, and nobody has
	// checked it yet: pending, and unclaimed.
	n2 := add(a2, feedback.Event{Block: "1/2", Type: "comment", Text: "n", Hash: hash(a2, "1/2")}, t0.Add(24*time.Hour+30*time.Minute))
	add(a2, feedback.Event{Type: "review_done"}, t0.Add(25*time.Hour))
	add(a2, feedback.Event{Block: "1/2", Type: "addressed", Text: "did", ReplyTo: feedback.NoteID(n2), Author: "agent:a", Hash: n2.Hash}, t0.Add(26*time.Hour))

	// Agent b: one review nobody opened, and one where the reviewer
	// rejected a block, the agent claimed a fix and the reviewer reopened it.
	pub("agent:b", t0)
	b2 := pub("agent:b", t0.Add(48*time.Hour)).Meta.ID
	add(b2, feedback.Event{Block: "1/2", Type: "approve", Hash: hash(b2, "1/2")}, t0.Add(48*time.Hour+10*time.Minute))
	rej := add(b2, feedback.Event{Block: "1.1/2", Type: "reject", Text: "no", Hash: hash(b2, "1.1/2")}, t0.Add(48*time.Hour+15*time.Minute))
	add(b2, feedback.Event{Block: "1.1/2", Type: "addressed", Text: "did", ReplyTo: feedback.NoteID(rej), Author: "agent:b", Hash: rej.Hash}, t0.Add(48*time.Hour+16*time.Minute))
	add(b2, feedback.Event{Block: "1.1/2", Type: "reopen", ReplyTo: feedback.NoteID(rej)}, t0.Add(48*time.Hour+17*time.Minute))
	add(b2, feedback.Event{Type: "review_done"}, t0.Add(48*time.Hour+20*time.Minute))

	clock = t0.Add(8 * 24 * time.Hour)
	// A second process on the same store: its startup reads, then none.
	h2, err := Open(ctx, Options{Store: counting, PublicURL: "https://review.example", Token: "tok", DevIdentity: "dev@example.com", Log: testWriter{t}, Now: func() time.Time { return clock }})
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(h2.Handler())
	t.Cleanup(srv.Close)
	ts2 := srv.URL
	counting.Reset()

	var s web.Stats
	code, body := get(t, ts2+"/api/stats")
	if code != 200 {
		t.Fatalf("/api/stats: %d %s", code, body)
	}
	if err := json.Unmarshal([]byte(body), &s); err != nil {
		t.Fatal(err)
	}
	if len(s.Agents) != 2 {
		t.Fatalf("agents %+v", s.Agents)
	}
	a, b := s.Agents[0], s.Agents[1]
	if a.Agent != "agent:a" || a.Reviews != 2 || a.Done != 2 || a.Rounds != 3 {
		t.Errorf("agent a: %+v", a)
	}
	if a.FirstLook != (web.Durations{Count: 2, Median: 3600, P90: 3600}) || a.TimeToDone != (web.Durations{Count: 2, Median: 3 * 3600, P90: 3 * 3600}) {
		t.Errorf("agent a timings: look %+v, done %+v", a.FirstLook, a.TimeToDone)
	}
	if a.Notes["comment"] != 2 || a.Notes["question"] != 1 || len(a.Notes) != 2 {
		t.Errorf("agent a notes: %v", a.Notes)
	}
	if a.Claims != (web.Claims{Addressed: 2, Confirmed: 1, Pending: 1, Unclaimed: 1}) {
		t.Errorf("agent a claims: %+v", a.Claims)
	}
	if b.Agent != "agent:b" || b.Reviews != 2 || b.Done != 1 || b.Rounds != 1 {
		t.Errorf("agent b: %+v", b)
	}
	if b.FirstLook != (web.Durations{Count: 1, Median: 600, P90: 600}) || b.TimeToDone.Median != 1200 {
		t.Errorf("agent b timings: look %+v, done %+v", b.FirstLook, b.TimeToDone)
	}
	if b.Notes["approve"] != 1 || b.Notes["reject"] != 1 || b.Claims != (web.Claims{Addressed: 1, Reopened: 1}) {
		t.Errorf("agent b notes %v claims %+v", b.Notes, b.Claims)
	}
	if s.Total.Reviews != 4 || s.Total.Done != 3 || s.Total.Rounds != 4 {
		t.Errorf("total: %+v", s.Total)
	}

	// The last 7 days hold a's second review and b's second; one agent alone.
	_, body = get(t, ts2+"/api/stats?period=7d&agent=agent:b")
	if err := json.Unmarshal([]byte(body), &s); err != nil {
		t.Fatal(err)
	}
	if len(s.Agents) != 1 || s.Agents[0].Reviews != 1 || s.Total.Reviews != 1 {
		t.Errorf("filtered: %+v", s)
	}
	code, page := get(t, ts2+"/stats")
	if code != 200 || !strings.Contains(page, `data-agent="agent:a"`) || !strings.Contains(page, "All agents") {
		t.Errorf("/stats: %d", code)
	}
	if strings.Contains(page, "did") || strings.Contains(body, "Capped") {
		t.Error("the stats carry document or note text")
	}
	if got := counting.Reads(); got != 0 {
		t.Errorf("stats cost %d store reads, want 0", got)
	}
}

// Done follows the review's completion rule, as the inbox does: with two
// reviewers required, one Done is neither done nor a finished round.
func TestStatsFollowTheCompletionRule(t *testing.T) {
	h, ts := open(t, storage.NewMemory())
	r, err := h.Publish(context.Background(), Publish{Agent: "a", Config: "complete_when: all\nreviewers: [ana, bo]\n", Docs: []Document{{Name: "spec.md", Content: []byte(specDoc)}}})
	if err != nil {
		t.Fatal(err)
	}
	store := r.Server.Entries()[0].Store
	read := func() web.AgentStats {
		var s web.Stats
		_, body := get(t, ts.URL+"/api/stats")
		if err := json.Unmarshal([]byte(body), &s); err != nil {
			t.Fatal(err)
		}
		return s.Total
	}
	ts0 := time.Now().UTC()
	_ = store.Append(feedback.Event{Doc: "spec.md", Type: "review_done", Author: "ana", Ts: ts0.Format(time.RFC3339)})
	if got := read(); got.Done != 0 || got.Rounds != 0 {
		t.Errorf("one of two Dones: %+v", got)
	}
	_ = store.Append(feedback.Event{Doc: "spec.md", Type: "review_done", Author: "bo", Ts: ts0.Add(time.Minute).Format(time.RFC3339)})
	if got := read(); got.Done != 1 || got.Rounds != 1 || got.TimeToDone.Count != 1 {
		t.Errorf("both Dones: %+v", got)
	}
}
