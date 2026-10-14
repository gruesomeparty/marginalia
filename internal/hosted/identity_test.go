package hosted

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	jose "github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"

	"github.com/gruesomeparty/marginalia/internal/feedback"
	"github.com/gruesomeparty/marginalia/internal/identity"
	"github.com/gruesomeparty/marginalia/internal/storage"
)

// edge is a fake Cloudflare Access: a signing key, a JWKS endpoint, and a
// way to mint the token it would put on a request.
type edge struct {
	t      *testing.T
	key    jose.JSONWebKey
	jwks   *httptest.Server
	issuer string
	aud    string
}

func newEdge(t *testing.T) *edge {
	t.Helper()
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	e := &edge{t: t, key: jose.JSONWebKey{Key: priv, KeyID: "edge-1", Algorithm: string(jose.RS256), Use: "sig"}, issuer: "https://team.cloudflareaccess.com", aud: "app-aud-tag"}
	e.jwks = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(jose.JSONWebKeySet{Keys: []jose.JSONWebKey{e.key.Public()}})
	}))
	t.Cleanup(e.jwks.Close)
	return e
}

func (e *edge) verifier() *identity.Verifier {
	return &identity.Verifier{Issuer: e.issuer, Audience: e.aud, JWKSURL: e.jwks.URL}
}

type claims struct {
	jwt.Claims
	Email string `json:"email,omitempty"`
}

func (e *edge) claims(email string) claims {
	return claims{Claims: jwt.Claims{
		Issuer: e.issuer, Subject: "sub-1", Audience: jwt.Audience{e.aud},
		Expiry: jwt.NewNumericDate(time.Now().Add(time.Hour)), IssuedAt: jwt.NewNumericDate(time.Now()),
	}, Email: email}
}

func (e *edge) mint(key jose.JSONWebKey, c claims) string {
	e.t.Helper()
	signer, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.RS256, Key: key}, (&jose.SignerOptions{}).WithType("JWT"))
	if err != nil {
		e.t.Fatal(err)
	}
	raw, err := jwt.Signed(signer).Claims(c).Serialize()
	if err != nil {
		e.t.Fatal(err)
	}
	return raw
}

// openWithEdge is open() with the edge's verifier instead of a dev identity.
func openWithEdge(t *testing.T, e *edge) (*Host, *httptest.Server) {
	t.Helper()
	h, err := Open(context.Background(), Options{Store: storage.NewMemory(), PublicURL: "https://review.example", Token: "tok", HumanAuth: e.verifier(), Log: testWriter{t}})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(h.Handler())
	t.Cleanup(ts.Close)
	return h, ts
}

// do sends a request with the given headers and returns the status and body.
func do(t *testing.T, method, url string, body []byte, headers map[string]string) (int, string) {
	t.Helper()
	req, err := http.NewRequest(method, url, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	var b bytes.Buffer
	_, _ = b.ReadFrom(res.Body)
	return res.StatusCode, b.String()
}

func TestHumanWithAValidTokenIsTheAuthorWhateverTheBodySays(t *testing.T) {
	e := newEdge(t)
	h, ts := openWithEdge(t, e)
	r := publish(t, h, "spec", Document{Name: "spec.md", Content: []byte(specDoc)})
	page := ts.URL + "/r/" + r.Meta.ID + "/"
	token := e.mint(e.key, e.claims("berkay@example.com"))

	if code, _ := do(t, "GET", page, nil, map[string]string{accessHeader: token}); code != 200 {
		t.Fatalf("page with a valid token: %d", code)
	}
	// The cookie form works too.
	if code, _ := do(t, "GET", page, nil, map[string]string{"Cookie": accessCookie + "=" + token}); code != 200 {
		t.Fatalf("page with the cookie: %d", code)
	}
	body, _ := json.Marshal(feedback.Event{Doc: "spec.md", Block: "1/2", Type: "comment", Text: "why?", Author: "mallory"})
	code, out := do(t, "POST", page+"api/feedback", body, map[string]string{accessHeader: token})
	if code != http.StatusCreated {
		t.Fatalf("POST with a valid token: %d %s", code, out)
	}
	var saved feedback.Event
	if err := json.Unmarshal([]byte(out), &saved); err != nil {
		t.Fatal(err)
	}
	if saved.Author != "berkay@example.com" {
		t.Errorf("author = %q: the body's claim won over the token", saved.Author)
	}
	events, _ := r.Server.Entries()[0].Store.Load()
	if len(events) != 1 || events[0].Author != "berkay@example.com" {
		t.Errorf("the log says %+v", events)
	}
	// review_done through the page is the human's to write.
	body, _ = json.Marshal(feedback.Event{Doc: "spec.md", Type: feedback.TypeReviewDone})
	if code, out := do(t, "POST", page+"api/feedback", body, map[string]string{accessHeader: token}); code != http.StatusCreated {
		t.Errorf("a human's review_done: %d %s", code, out)
	}
	// session_done too, under the same name.
	if code, out := do(t, "POST", page+"api/session_done", []byte("{}"), map[string]string{accessHeader: token}); code != http.StatusCreated {
		t.Errorf("a human's session_done: %d %s", code, out)
	}
	events, _ = r.Server.Entries()[0].Store.Load()
	for _, ev := range events {
		if ev.Author != "berkay@example.com" {
			t.Errorf("event %s has author %q", ev.Type, ev.Author)
		}
	}
}

func TestPageRoutesRefuseBadOrMissingTokens(t *testing.T) {
	e := newEdge(t)
	h, ts := openWithEdge(t, e)
	r := publish(t, h, "spec", Document{Name: "spec.md", Content: []byte(specDoc)})
	page := ts.URL + "/r/" + r.Meta.ID + "/"
	other := newEdge(t)
	otherKey := other.key
	otherKey.KeyID = e.key.KeyID
	expired := e.claims("x@example.com")
	expired.Expiry = jwt.NewNumericDate(time.Now().Add(-time.Hour))
	wrongAud := e.claims("x@example.com")
	wrongAud.Audience = jwt.Audience{"another-app"}
	wrongIss := e.claims("x@example.com")
	wrongIss.Issuer = "https://other.cloudflareaccess.com"
	cases := map[string]map[string]string{
		"missing":          {},
		"expired":          {accessHeader: e.mint(e.key, expired)},
		"wrong audience":   {accessHeader: e.mint(e.key, wrongAud)},
		"wrong issuer":     {accessHeader: e.mint(e.key, wrongIss)},
		"another key":      {accessHeader: e.mint(otherKey, e.claims("x@example.com"))},
		"garbage":          {accessHeader: "not.a.token"},
		"agent credential": {"Authorization": "Bearer tok"},
	}
	comment, _ := json.Marshal(feedback.Event{Doc: "spec.md", Block: "1/2", Type: "comment", Text: "x", Author: "x"})
	for name, headers := range cases {
		t.Run(name, func(t *testing.T) {
			for _, path := range []string{"", "api/doc"} {
				code, _ := do(t, "GET", page+path, nil, headers)
				if code != http.StatusUnauthorized && code != http.StatusForbidden {
					t.Errorf("GET %s = %d, want 401/403", path, code)
				}
			}
			if code, _ := do(t, "GET", ts.URL+"/", nil, headers); code != http.StatusUnauthorized && code != http.StatusForbidden {
				t.Errorf("GET / = %d, want 401/403", code)
			}
			code, _ := do(t, "POST", page+"api/feedback", comment, headers)
			if code != http.StatusUnauthorized && code != http.StatusForbidden {
				t.Errorf("POST = %d, want 401/403", code)
			}
		})
	}
	if events, _ := r.Server.Entries()[0].Store.Load(); len(events) != 0 {
		t.Errorf("a refused request appended: %+v", events)
	}
	// Health needs nothing, as the platform's probe cannot sign in.
	if code, _ := do(t, "GET", ts.URL+"/healthz", nil, nil); code != 200 {
		t.Errorf("/healthz = %d", code)
	}
}

// The MCP token opens /mcp and nothing else: presented to a page route with
// a review_done, it is refused and the log is untouched. The tool surface
// has no way to write one either, so there is no road from an agent
// credential to the reviewer's verdict.
func TestAgentCredentialCannotWriteTheVerdict(t *testing.T) {
	e := newEdge(t)
	h, ts := openWithEdge(t, e)
	r := publish(t, h, "spec", Document{Name: "spec.md", Content: []byte(specDoc)})
	page := ts.URL + "/r/" + r.Meta.ID + "/"
	for _, typ := range []string{feedback.TypeReviewDone, feedback.TypeConfirm, feedback.TypeReopen, "comment"} {
		body, _ := json.Marshal(feedback.Event{Doc: "spec.md", Block: "1/2", Type: typ, Text: "x", Author: "agent"})
		code, _ := do(t, "POST", page+"api/feedback", body, map[string]string{"Authorization": "Bearer tok"})
		if code != http.StatusForbidden {
			t.Errorf("agent credential posting %s: %d, want 403", typ, code)
		}
	}
	if code, _ := do(t, "POST", page+"api/session_done", []byte("{}"), map[string]string{"Authorization": "Bearer tok"}); code != http.StatusForbidden {
		t.Errorf("agent credential on session_done: %d, want 403", code)
	}
	if events, _ := r.Server.Entries()[0].Store.Load(); len(events) != 0 {
		t.Fatalf("something was appended: %+v", events)
	}
	// And over MCP, what the agent writes is written under the agent's
	// name, whatever it would like to be called.
	cs := connect(t, ts)
	token := e.mint(e.key, e.claims("berkay@example.com"))
	body, _ := json.Marshal(feedback.Event{Doc: "spec.md", Block: "1/2", Type: "question", Text: "why?"})
	if code, _ := do(t, "POST", page+"api/feedback", body, map[string]string{accessHeader: token}); code != http.StatusCreated {
		t.Fatal(code)
	}
	var seen feedbackOut
	if err := call(t, cs, "feedback_since", map[string]any{"review_id": r.Meta.ID}, &seen); err != nil {
		t.Fatal(err)
	}
	var out mcpserverReply
	if err := call(t, cs, "reply_to_note", map[string]any{"review_id": r.Meta.ID, "name": "spec.md", "note_id": seen.Docs[0].Events[0].ID, "text": "because"}, &out); err != nil {
		t.Fatal(err)
	}
	events, _ := r.Server.Entries()[0].Store.Load()
	if len(events) != 2 || events[1].Author != "agent" || events[1].Type != feedback.TypeReply {
		t.Errorf("the agent's reply: %+v", events[len(events)-1])
	}
}

func TestDevIdentityStandsInForTheEdge(t *testing.T) {
	h, ts := open(t, storage.NewMemory()) // open() sets DevIdentity
	r := publish(t, h, "spec", Document{Name: "spec.md", Content: []byte(specDoc)})
	page := ts.URL + "/r/" + r.Meta.ID + "/"
	body, _ := json.Marshal(feedback.Event{Doc: "spec.md", Block: "1/2", Type: "comment", Text: "x", Author: "someone-else"})
	code, out := do(t, "POST", page+"api/feedback", body, nil)
	if code != http.StatusCreated || !strings.Contains(out, `"author":"dev@example.com"`) {
		t.Errorf("dev identity: %d %s", code, out)
	}
	// Even then, an agent credential on a page route is refused.
	if code, _ := do(t, "GET", page, nil, map[string]string{"Authorization": "Bearer tok"}); code != http.StatusForbidden {
		t.Errorf("agent credential with a dev identity: %d", code)
	}
}

func TestOpenNeedsAHumanIdentity(t *testing.T) {
	_, err := Open(context.Background(), Options{Store: storage.NewMemory()})
	if err == nil || !strings.Contains(err.Error(), "human identity") {
		t.Fatalf("got %v", err)
	}
}
