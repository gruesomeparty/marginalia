package mcpserver

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const testToken = "s3cret-token"

// bearer is an http.RoundTripper that presents one token, the way a client's
// configured `headers` would.
type bearer struct {
	token string
	next  http.RoundTripper
}

func (b bearer) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	if b.token != "" {
		r.Header.Set("Authorization", "Bearer "+b.token)
	}
	return b.next.RoundTrip(r)
}

// connectHTTP opens an MCP session over Streamable HTTP against base, as a
// remote client would.
func connectHTTP(t *testing.T, base, token string) (*mcp.ClientSession, error) {
	t.Helper()
	transport := &mcp.StreamableClientTransport{
		Endpoint:   base + "/mcp",
		HTTPClient: &http.Client{Transport: bearer{token: token, next: http.DefaultTransport}},
		MaxRetries: -1,
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "remote-test", Version: "1"}, nil).Connect(ctx, transport, nil)
	if err != nil {
		return nil, err
	}
	t.Cleanup(func() { _ = cs.Close() })
	return cs, nil
}

func serveHTTP(t *testing.T, root, token string) (*httptest.Server, *Server) {
	t.Helper()
	srv, err := New(Options{Root: root, Log: testWriter{t}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(srv.Close)
	ts := httptest.NewServer(srv.Handler(token))
	t.Cleanup(ts.Close)
	return ts, srv
}

func toolNames(t *testing.T, cs *mcp.ClientSession) map[string]bool {
	t.Helper()
	res, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, tool := range res.Tools {
		got[tool.Name] = true
	}
	return got
}

// The acceptance criterion of #93, end to end: the HTTP transport serves the
// same tools stdio does, and a real call works with the token.
func TestHTTPServesTheSameToolsAsStdio(t *testing.T) {
	root := t.TempDir()
	writeDoc(t, root, "spec.md", specDoc)
	ts, _ := serveHTTP(t, root, testToken)

	cs, err := connectHTTP(t, ts.URL, testToken)
	if err != nil {
		t.Fatalf("connect with the right token: %v", err)
	}
	overHTTP := toolNames(t, cs)

	local, _ := serveMCP(t, root)
	overStdio := toolNames(t, local)
	if len(overHTTP) != len(overStdio) {
		t.Fatalf("tool sets differ: http=%v stdio=%v", overHTTP, overStdio)
	}
	for name := range overStdio {
		if !overHTTP[name] {
			t.Errorf("tool %q is missing over HTTP", name)
		}
	}

	var status statusOut
	if err := call(t, cs, "review_status", map[string]any{"paths": []string{"spec.md"}}, &status); err != nil {
		t.Fatalf("review_status over HTTP: %v", err)
	}
	if len(status.Docs) != 1 || status.Docs[0].Blocks == 0 {
		t.Errorf("review_status over HTTP returned nothing useful: %+v", status)
	}
}

func TestHTTPRefusesAMissingOrWrongToken(t *testing.T) {
	root := t.TempDir()
	ts, _ := serveHTTP(t, root, testToken)

	// At the HTTP level: a 401 with a Bearer challenge, so a client knows it
	// is the credential that is wrong and not the URL.
	for _, tc := range []struct{ name, token string }{{"missing", ""}, {"wrong", "nope"}, {"nearly", testToken + "x"}} {
		t.Run(tc.name, func(t *testing.T) {
			req, err := http.NewRequest(http.MethodPost, ts.URL+"/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
			if err != nil {
				t.Fatal(err)
			}
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Accept", "application/json, text/event-stream")
			if tc.token != "" {
				req.Header.Set("Authorization", "Bearer "+tc.token)
			}
			res, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = res.Body.Close() }()
			if res.StatusCode != http.StatusUnauthorized {
				t.Fatalf("status %d, want 401", res.StatusCode)
			}
			if got := res.Header.Get("WWW-Authenticate"); !strings.HasPrefix(got, "Bearer") {
				t.Errorf("WWW-Authenticate = %q, want a Bearer challenge", got)
			}
			body, _ := io.ReadAll(res.Body)
			if !strings.Contains(string(body), TokenEnv) {
				t.Errorf("the refusal does not say where the token comes from: %s", body)
			}
		})
	}

	// And at the MCP level: a client with the wrong token never gets a
	// session at all.
	if _, err := connectHTTP(t, ts.URL, "nope"); err == nil {
		t.Error("a client with the wrong token got a session")
	}
	if _, err := connectHTTP(t, ts.URL, ""); err == nil {
		t.Error("a client with no token got a session")
	}
}

func TestTokenMatchingIsCaseInsensitiveOnTheSchemeOnly(t *testing.T) {
	root := t.TempDir()
	ts, _ := serveHTTP(t, root, testToken)
	for _, tc := range []struct {
		header string
		want   int
	}{
		{"bearer " + testToken, http.StatusOK},
		{"BEARER " + testToken, http.StatusOK},
		{"Bearer " + strings.ToUpper(testToken), http.StatusUnauthorized},
		{"Basic " + testToken, http.StatusUnauthorized},
		{"Bearer", http.StatusUnauthorized},
	} {
		req, err := http.NewRequest(http.MethodGet, ts.URL+"/mcp", nil)
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", tc.header)
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = res.Body.Close()
		// A GET that passes auth reaches the stateless transport, which
		// answers 405; anything that is not 401 means the token was taken.
		got := res.StatusCode
		if tc.want == http.StatusOK && got == http.StatusUnauthorized {
			t.Errorf("%q: refused, want accepted", tc.header)
		}
		if tc.want == http.StatusUnauthorized && got != http.StatusUnauthorized {
			t.Errorf("%q: status %d, want 401", tc.header, got)
		}
	}
}

// A token-protected endpoint behind a same-host reverse proxy (`tailscale
// serve`, say) sees loopback as its local address and the public hostname as
// Host. The token is the auth boundary there, so that must work; without a
// token the Host check is what keeps a web page from driving a loopback
// server, so it must not.
func TestHostHeaderIsOnlyCheckedWhenNothingElseGuardsTheEndpoint(t *testing.T) {
	root := t.TempDir()
	proxied := func(t *testing.T, base, token string) int {
		t.Helper()
		req, err := http.NewRequest(http.MethodPost, base+"/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
		if err != nil {
			t.Fatal(err)
		}
		req.Host = "review.example"
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = res.Body.Close()
		return res.StatusCode
	}
	withToken, _ := serveHTTP(t, root, testToken)
	if got := proxied(t, withToken.URL, testToken); got != http.StatusOK {
		t.Errorf("with a token, a proxied Host header got %d, want 200", got)
	}
	open, _ := serveHTTP(t, root, "")
	if got := proxied(t, open.URL, ""); got != http.StatusForbidden {
		t.Errorf("with no token, a foreign Host header on a loopback server got %d, want 403 (DNS rebinding guard)", got)
	}
}

func TestHealthzNeedsNoToken(t *testing.T) {
	ts, _ := serveHTTP(t, t.TempDir(), testToken)
	res, err := http.Get(ts.URL + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	body, _ := io.ReadAll(res.Body)
	if res.StatusCode != http.StatusOK || !strings.HasPrefix(string(body), "ok") {
		t.Errorf("GET /healthz = %d %q, want 200 ok", res.StatusCode, body)
	}
	// Health is the only thing the token does not gate.
	res, err = http.Get(ts.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode != http.StatusNotFound {
		t.Errorf("GET / = %d, want 404: the HTTP transport serves tools and health, nothing else", res.StatusCode)
	}
}

func TestCheckBindRefusesAnOpenEndpointOffLoopback(t *testing.T) {
	for _, tc := range []struct {
		addr, token string
		ok          bool
	}{
		{"127.0.0.1:8080", "", true},
		{"localhost:0", "", true},
		{"[::1]:8080", "", true},
		{"127.0.0.1", "", true},
		{":8080", "", false},
		{"0.0.0.0:0", "", false},
		{"[::]:8080", "", false},
		{"100.64.0.1:8080", "", false},
		{"example.com:8080", "", false},
		{"0.0.0.0:0", testToken, true},
		{":8080", testToken, true},
	} {
		err := CheckBind(tc.addr, tc.token)
		if tc.ok && err != nil {
			t.Errorf("CheckBind(%q, %q) = %v, want ok", tc.addr, tc.token, err)
		}
		if !tc.ok {
			if err == nil {
				t.Errorf("CheckBind(%q, %q) allowed an unauthenticated endpoint off loopback", tc.addr, tc.token)
			} else if !strings.Contains(err.Error(), TokenEnv) {
				t.Errorf("CheckBind(%q, %q) refused without naming %s: %v", tc.addr, tc.token, TokenEnv, err)
			}
		}
	}
}

// A graceful shutdown must not wait out a thirty-minute await: the wait
// returns "cancelled" with what it has, and the server returns once that
// response is written.
func TestHTTPShutdownReturnsInFlightWaits(t *testing.T) {
	root := t.TempDir()
	writeDoc(t, root, "spec.md", specDoc)
	srv, err := New(Options{Root: root, Log: testWriter{t}})
	if err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	served := make(chan error, 1)
	go func() { served <- srv.ServeHTTP(ctx, ln, testToken) }()

	cs, err := connectHTTP(t, "http://"+ln.Addr().String(), testToken)
	if err != nil {
		t.Fatal(err)
	}
	type outcome struct {
		res *mcp.CallToolResult
		err error
	}
	waited := make(chan outcome, 1)
	go func() {
		res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
			Name:      "await_review_done",
			Arguments: map[string]any{"paths": []string{"spec.md"}, "timeout_seconds": 120},
		})
		waited <- outcome{res, err}
	}()
	// Let the call reach the server before pulling the plug.
	time.Sleep(500 * time.Millisecond)
	start := time.Now()
	cancel()

	select {
	case o := <-waited:
		if o.err != nil {
			t.Fatalf("the in-flight wait did not get an answer: %v", o.err)
		}
		if o.res.IsError {
			t.Fatalf("the in-flight wait failed: %v", errText(o.res))
		}
		reason, _ := o.res.StructuredContent.(map[string]any)["reason"].(string)
		if reason != "cancelled" {
			t.Errorf("reason = %q, want cancelled", reason)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("the in-flight wait was not returned on shutdown")
	}
	select {
	case err := <-served:
		if err != nil {
			t.Errorf("ServeHTTP returned %v", err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("ServeHTTP did not return after shutdown")
	}
	if elapsed := time.Since(start); elapsed > 10*time.Second {
		t.Errorf("shutdown took %s; the drain should be short once waits are interrupted", elapsed)
	}
}
