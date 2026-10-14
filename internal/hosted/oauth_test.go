package hosted

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-jose/go-jose/v4/jwt"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/gruesomeparty/marginalia/internal/feedback"
	"github.com/gruesomeparty/marginalia/internal/identity"
	"github.com/gruesomeparty/marginalia/internal/storage"
)

// openWithOAuth is a host with an authorization server (the fake edge, reused
// as an OAuth issuer) and, optionally, the static token too.
func openWithOAuth(t *testing.T, as *edge, static string) (*Host, *httptest.Server) {
	t.Helper()
	as.issuer = "https://auth.example"
	as.aud = "https://review.example/mcp"
	h, err := Open(context.Background(), Options{
		Store: storage.NewMemory(), PublicURL: "https://review.example", Token: static, DevIdentity: "dev@example.com",
		OAuth: &identity.Verifier{Issuer: as.issuer, Audience: as.aud, JWKSURL: as.jwks.URL}, OAuthIssuer: as.issuer,
		Log: testWriter{t},
	})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(h.Handler())
	t.Cleanup(ts.Close)
	return h, ts
}

// connectWith opens an MCP session presenting any bearer.
func connectWith(t *testing.T, ts *httptest.Server, token string) (*mcp.ClientSession, error) {
	t.Helper()
	transport := &mcp.StreamableClientTransport{
		Endpoint:   ts.URL + "/mcp",
		HTTPClient: &http.Client{Transport: bearer{token: token, next: http.DefaultTransport}},
		MaxRetries: -1,
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "claude-ai", Version: "1"}, nil).Connect(ctx, transport, nil)
	if err != nil {
		return nil, err
	}
	t.Cleanup(func() { _ = cs.Close() })
	return cs, nil
}

func TestProtectedResourceMetadataNamesTheResourceAndItsServer(t *testing.T) {
	as := newEdge(t)
	_, ts := openWithOAuth(t, as, "")
	for _, path := range []string{"/.well-known/oauth-protected-resource", "/.well-known/oauth-protected-resource/mcp"} {
		code, body := get(t, ts.URL+path)
		if code != 200 {
			t.Fatalf("GET %s = %d", path, code)
		}
		var meta struct {
			Resource string   `json:"resource"`
			Servers  []string `json:"authorization_servers"`
			Methods  []string `json:"bearer_methods_supported"`
		}
		if err := json.Unmarshal([]byte(body), &meta); err != nil {
			t.Fatalf("%s is not JSON: %v", path, err)
		}
		if meta.Resource != "https://review.example/mcp" || len(meta.Servers) != 1 || meta.Servers[0] != "https://auth.example" {
			t.Errorf("%s = %+v", path, meta)
		}
		if len(meta.Methods) != 1 || meta.Methods[0] != "header" {
			t.Errorf("%s bearer methods = %v", path, meta.Methods)
		}
	}
	// The metadata needs no credential: it is how a client finds one.
	code, _ := do(t, "GET", ts.URL+"/.well-known/oauth-protected-resource/mcp", nil, map[string]string{"Authorization": "Bearer nope"})
	if code != 200 {
		t.Errorf("metadata with a bad bearer = %d", code)
	}
}

func TestUnauthenticatedMCPIsChallengedWithTheMetadataURL(t *testing.T) {
	as := newEdge(t)
	_, ts := openWithOAuth(t, as, "")
	req, err := http.NewRequest(http.MethodPost, ts.URL+"/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status %d, want 401", res.StatusCode)
	}
	challenge := res.Header.Get("WWW-Authenticate")
	const want = `resource_metadata="https://review.example/.well-known/oauth-protected-resource/mcp"`
	if !strings.HasPrefix(challenge, "Bearer") || !strings.Contains(challenge, want) {
		t.Fatalf("WWW-Authenticate = %q, want a Bearer challenge carrying %s", challenge, want)
	}
	// And that URL resolves to the metadata on this host.
	url := strings.TrimPrefix(strings.TrimSuffix(strings.SplitN(challenge, `resource_metadata="`, 2)[1], `"`), "https://review.example")
	if code, body := get(t, ts.URL+url); code != 200 || !strings.Contains(body, `"authorization_servers"`) {
		t.Errorf("the challenged URL %s = %d", url, code)
	}
}

func TestAccessTokensFromTheServerOpenMCP(t *testing.T) {
	as := newEdge(t)
	h, ts := openWithOAuth(t, as, "")
	r := publish(t, h, "spec", Document{Name: "spec.md", Content: []byte(specDoc)})
	good := as.claims("")
	good.Subject = "client-7"
	good.Email = ""
	cs, err := connectWith(t, ts, as.mint(as.key, good))
	if err != nil {
		t.Fatalf("a valid access token was refused: %v", err)
	}
	tools, err := cs.ListTools(context.Background(), nil)
	if err != nil || len(tools.Tools) != 7 {
		t.Fatalf("tools over OAuth: %d, %v", len(tools.Tools), err)
	}
	// What the token's bearer writes is written as that agent, by subject.
	page := ts.URL + "/r/" + r.Meta.ID + "/"
	body, _ := json.Marshal(feedback.Event{Doc: "spec.md", Block: "1/2", Type: "question", Text: "why?"})
	if code, _ := do(t, "POST", page+"api/feedback", body, nil); code != http.StatusCreated {
		t.Fatal(code)
	}
	var seen feedbackOut
	if err := call(t, cs, "feedback_since", map[string]any{"review_id": r.Meta.ID}, &seen); err != nil {
		t.Fatal(err)
	}
	if err := call(t, cs, "reply_to_note", map[string]any{"review_id": r.Meta.ID, "name": "spec.md", "note_id": seen.Docs[0].Events[0].ID, "text": "because"}, nil); err != nil {
		t.Fatal(err)
	}
	events, _ := r.Server.Entries()[0].Store.Load()
	if len(events) != 2 || events[1].Author != "agent:client-7" {
		t.Errorf("the OAuth agent's reply: %+v", events[len(events)-1])
	}
	// An email in the token names the agent by it.
	withEmail := as.claims("bot@example.com")
	cs2, err := connectWith(t, ts, as.mint(as.key, withEmail))
	if err != nil {
		t.Fatal(err)
	}
	if err := call(t, cs2, "mark_addressed", map[string]any{"review_id": r.Meta.ID, "name": "spec.md", "note_id": seen.Docs[0].Events[0].ID, "text": "rewrote it"}, nil); err != nil {
		t.Fatal(err)
	}
	events, _ = r.Server.Entries()[0].Store.Load()
	if events[2].Author != "agent:bot@example.com" {
		t.Errorf("author = %q", events[2].Author)
	}
}

func TestBadAccessTokensAreRefused(t *testing.T) {
	as := newEdge(t)
	_, ts := openWithOAuth(t, as, "")
	expired := as.claims("x")
	expired.Expiry = jwt.NewNumericDate(time.Now().Add(-time.Hour))
	wrongAud := as.claims("x")
	wrongAud.Audience = jwt.Audience{"https://other.example/mcp"}
	wrongIss := as.claims("x")
	wrongIss.Issuer = "https://evil.example"
	for name, raw := range map[string]string{
		"expired":        as.mint(as.key, expired),
		"wrong audience": as.mint(as.key, wrongAud),
		"wrong issuer":   as.mint(as.key, wrongIss),
		"not a token":    "nope",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := connectWith(t, ts, raw); err == nil {
				t.Fatal("got a session")
			}
			req, _ := http.NewRequest(http.MethodPost, ts.URL+"/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Accept", "application/json, text/event-stream")
			req.Header.Set("Authorization", "Bearer "+raw)
			res, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			_ = res.Body.Close()
			if res.StatusCode != http.StatusUnauthorized {
				t.Errorf("status %d, want 401", res.StatusCode)
			}
		})
	}
}

// A valid access token is an agent's seat and no more: it cannot open a page
// route, and the tool surface it opens has nothing that writes a verdict.
func TestAnAgentAccessTokenCannotAppendReviewDone(t *testing.T) {
	as := newEdge(t)
	h, ts := openWithOAuth(t, as, "")
	r := publish(t, h, "spec", Document{Name: "spec.md", Content: []byte(specDoc)})
	token := as.mint(as.key, as.claims("bot@example.com"))
	page := ts.URL + "/r/" + r.Meta.ID + "/"
	body, _ := json.Marshal(feedback.Event{Doc: "spec.md", Type: feedback.TypeReviewDone})
	if code, _ := do(t, "POST", page+"api/feedback", body, map[string]string{"Authorization": "Bearer " + token}); code != http.StatusForbidden {
		t.Errorf("review_done with an access token on the page API: %d, want 403", code)
	}
	if code, _ := do(t, "GET", page, nil, map[string]string{"Authorization": "Bearer " + token}); code != http.StatusForbidden {
		t.Errorf("the page with an access token: %d, want 403", code)
	}
	cs, err := connectWith(t, ts, token)
	if err != nil {
		t.Fatal(err)
	}
	tools, _ := cs.ListTools(context.Background(), nil)
	for _, tool := range tools.Tools {
		for _, forbidden := range []string{"session_done", "confirm", "reopen", "comment", "post", "append", "apply", "settle"} {
			if strings.Contains(tool.Name, forbidden) {
				t.Errorf("tool %q looks like it writes the reviewer's verdict", tool.Name)
			}
		}
	}
	if events, _ := r.Server.Entries()[0].Store.Load(); len(events) != 0 {
		t.Errorf("something was appended: %+v", events)
	}
}

func TestTheStaticTokenStillWorksBesideOAuth(t *testing.T) {
	as := newEdge(t)
	_, ts := openWithOAuth(t, as, "tok")
	cs, err := connectWith(t, ts, "tok")
	if err != nil {
		t.Fatalf("the static token was refused once OAuth was on: %v", err)
	}
	if tools, err := cs.ListTools(context.Background(), nil); err != nil || len(tools.Tools) != 7 {
		t.Errorf("tools with the static token: %v", err)
	}
	if _, err := connectWith(t, ts, "wrong"); err == nil {
		t.Error("a wrong static token got a session")
	}
	if _, err := connectWith(t, ts, as.mint(as.key, as.claims("bot@example.com"))); err != nil {
		t.Errorf("an access token was refused beside the static token: %v", err)
	}
}

func TestOAuthNeedsAPublicURL(t *testing.T) {
	as := newEdge(t)
	_, err := Open(context.Background(), Options{
		Store: storage.NewMemory(), DevIdentity: "dev@example.com",
		OAuth: &identity.Verifier{Issuer: "https://auth.example", Audience: "x", JWKSURL: as.jwks.URL}, OAuthIssuer: "https://auth.example",
	})
	if err == nil || !strings.Contains(err.Error(), "public-url") {
		t.Fatalf("got %v", err)
	}
}

// Without an authorization server, nothing OAuth-shaped is served: no
// metadata, and the challenge carries no resource_metadata.
func TestNoOAuthMeansNoMetadata(t *testing.T) {
	_, ts := open(t, storage.NewMemory())
	if code, _ := get(t, ts.URL+"/.well-known/oauth-protected-resource/mcp"); code != http.StatusNotFound {
		t.Errorf("metadata served with no authorization server: %d", code)
	}
}
