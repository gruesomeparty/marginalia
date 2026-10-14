package mcpserver

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"os"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// TokenEnv names the environment variable carrying the bearer token that
// `mcp --http` requires on anything but a loopback address. An environment
// variable and never a flag: flags show up in process lists, shell histories
// and crash reports, and a token that leaks that way grants every tool here
// to whoever found it.
const TokenEnv = "MARGINALIA_MCP_TOKEN"

// Handler serves the same tools as stdio over Streamable HTTP at /mcp, and a
// health check at /healthz that needs no credential, so a container platform
// can ask after the process without holding a secret.
//
// A non-empty token gates /mcp (StaticTokenVerifier). An empty one leaves /mcp open,
// which is only acceptable on a loopback address — CheckBind is the gate for
// that, and the command runs it before listening.
//
// The transport is deliberately stateless. Marginalia never sends a
// server-initiated message — no notifications, no sampling, no elicitation —
// so a session id would buy nothing and cost two things: a standalone GET
// stream held open per client, which a graceful shutdown would have to wait
// out, and state that dies with the process, so a hosted instance restarting
// under a connector would answer 404 to every session the connector still
// holds. Stateless, a restart is invisible: each call carries everything it
// needs. Cancellation propagates from the HTTP request, so a client that
// disconnects mid-await stops the wait instead of leaving it polling for up
// to half an hour.
//
// The SDK's DNS-rebinding guard — refuse a request that arrived on a loopback
// address with a Host header that is not loopback — stays on only while /mcp
// is open. It exists so a web page cannot point a hostname at 127.0.0.1 and
// drive a local server from a browser, and with no token that is the one
// thing standing in the way. With a token the guard protects nothing (the
// page does not have the token) and breaks something real: `tailscale serve`
// and every same-host reverse proxy forward to 127.0.0.1 with the public
// hostname as Host, and would get 403 for it.
func (s *Server) Handler(token string) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/mcp", Transport(s.MCP(), token))
	mux.HandleFunc("GET /healthz", Healthz)
	return mux
}

// Transport is the Streamable HTTP transport for one MCP server, gated by
// the bearer verifier — the piece `mcp --http` and `host` share, each mounting it at
// its own /mcp. See Handler for why it is stateless and when the Host check
// is on.
func Transport(ms *mcp.Server, token string) http.Handler {
	var verifier auth.TokenVerifier
	if token != "" {
		verifier = StaticTokenVerifier(token, DefaultAgentName)
	}
	return TransportWith(ms, verifier, nil)
}

// DefaultAgentName is the identity an agent holding the static token writes
// under, when the operator named none.
const DefaultAgentName = "agent"

// TransportWith is Transport with any verifier: the static token, an OAuth
// access token, or both. A nil verifier leaves /mcp open, which is only
// acceptable on loopback (CheckBind). The verifier's TokenInfo reaches every
// tool handler as req.Extra.TokenInfo, which is how a tool learns which
// agent is asking without re-reading the header.
func TransportWith(ms *mcp.Server, verifier auth.TokenVerifier, opts *auth.RequireBearerTokenOptions) http.Handler {
	streamable := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return ms }, &mcp.StreamableHTTPOptions{
		Stateless:                    true,
		PropagateRequestCancellation: true,
		DisableLocalhostProtection:   verifier != nil,
	})
	if verifier == nil {
		return streamable
	}
	if opts == nil {
		opts = &auth.RequireBearerTokenOptions{}
	}
	// The static token carries no expiry of its own; its lifetime is the
	// operator's to end.
	opts.AllowMissingExpiration = true
	return challenge(auth.RequireBearerToken(verifier, opts)(streamable))
}

// StaticTokenVerifier accepts exactly one token, compared as fixed-length
// digests in constant time so a wrong token takes as long to refuse as a
// nearly right one, and names its bearer agentName.
func StaticTokenVerifier(token, agentName string) auth.TokenVerifier {
	want := sha256.Sum256([]byte(token))
	if agentName == "" {
		agentName = DefaultAgentName
	}
	return func(_ context.Context, got string, _ *http.Request) (*auth.TokenInfo, error) {
		sum := sha256.Sum256([]byte(strings.TrimSpace(got)))
		if subtle.ConstantTimeCompare(sum[:], want[:]) != 1 {
			return nil, fmt.Errorf("%w: /mcp needs `Authorization: Bearer <token>` — the value of %s on the server", auth.ErrInvalidToken, TokenEnv)
		}
		return &auth.TokenInfo{UserID: agentName}, nil
	}
}

// challenge makes sure a 401 carries a `WWW-Authenticate: Bearer` header,
// which is what tells an MCP client that it is a credential, not the URL,
// that is wrong. The SDK's middleware adds one only when it has parameters
// to put in it (a resource metadata URL, scopes); a plain challenge is still
// a challenge.
func challenge(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(&challenger{ResponseWriter: w}, r)
	})
}

type challenger struct {
	http.ResponseWriter
	wrote bool
}

func (c *challenger) WriteHeader(code int) {
	if !c.wrote {
		c.wrote = true
		if code == http.StatusUnauthorized && c.Header().Get("WWW-Authenticate") == "" {
			c.Header().Set("WWW-Authenticate", `Bearer realm="marginalia"`)
		}
	}
	c.ResponseWriter.WriteHeader(code)
}

func (c *challenger) Write(p []byte) (int, error) {
	if !c.wrote {
		c.WriteHeader(http.StatusOK)
	}
	return c.ResponseWriter.Write(p)
}

// Flush keeps streaming working through the wrapper: the SDK's SSE responses
// flush after every event.
func (c *challenger) Flush() {
	if f, ok := c.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// Healthz answers a platform's health check with 200 and "ok", with no
// credential, so a container can be probed without holding a secret.
func Healthz(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = io.WriteString(w, "ok\n")
}

// CheckBind refuses to put an unauthenticated MCP endpoint on an interface
// other people can reach. With a token any address is fine; without one, only
// loopback is. It runs before anything listens, so the refusal costs nothing
// and leaves nothing behind.
func CheckBind(addr, token string) error {
	if token != "" || Loopback(addr) {
		return nil
	}
	return fmt.Errorf("mcp --http %s needs %s set: an MCP endpoint off loopback with no token would let anyone who can reach it start reviews and read feedback — bind 127.0.0.1 for local use, or export %s", addr, TokenEnv, TokenEnv)
}

// Loopback reports whether a listen address names only this machine. An empty
// host (":8080") binds every interface and so is not loopback; "localhost"
// and any loopback IP are.
func Loopback(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		host = addr
	}
	host = strings.Trim(host, "[]")
	if host == "" {
		return false
	}
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip, err := netip.ParseAddr(host)
	return err == nil && ip.IsLoopback()
}

// ServeHTTP serves the tools over HTTP on ln until ctx is cancelled, then
// shuts down gracefully — and waits for the drain before returning, as
// `serve` does, so a reply being written as the process is told to stop still
// reaches disk and still gets its answer.
//
// The drain is made short on purpose: cancelling ctx first interrupts every
// in-flight await, which returns "cancelled" with whatever had arrived, so
// what is left to drain is responses being written, not a thirty-minute wait.
func (s *Server) ServeHTTP(ctx context.Context, ln net.Listener, token string) error {
	httpSrv := &http.Server{Handler: s.Handler(token), ReadHeaderTimeout: 5 * time.Second}
	stopped := make(chan struct{})
	defer close(stopped)
	drained := make(chan error, 1)
	go func() {
		select {
		case <-ctx.Done():
		case <-stopped:
			return // Serve failed on its own; there is nothing to drain
		}
		s.interrupt()
		shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		drained <- httpSrv.Shutdown(shutCtx)
	}()
	auth := "bearer token required"
	if token == "" {
		auth = "no token: loopback only"
	}
	s.logf("marginalia: MCP over Streamable HTTP at http://%s/mcp (%s)\n", ln.Addr(), auth)
	err := httpSrv.Serve(ln)
	if err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	// ErrServerClosed means Shutdown was called, which only happens above:
	// wait for the in-flight requests it is draining.
	return <-drained
}

// logf is the one way this package talks to the operator. Under stdio the
// command points Options.Log at stderr; when nothing is set it still goes to
// stderr, never stdout, because stdout may be the protocol.
func (s *Server) logf(format string, args ...any) {
	out := s.opts.Log
	if out == nil {
		out = os.Stderr
	}
	_, _ = fmt.Fprintf(out, format, args...)
}
