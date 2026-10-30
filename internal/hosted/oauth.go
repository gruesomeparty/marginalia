package hosted

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/oauthex"

	"github.com/gruesomeparty/marginalia/internal/identity"
	"github.com/gruesomeparty/marginalia/internal/mcpserver"
)

// Marginalia is an OAuth resource server and nothing more: it never issues a
// token, stores a user or shows a login page. An authorization server the
// deploy picks — Cloudflare Access, or any OpenID provider — does that, and
// a client such as claude.ai finds it the way RFC 9728 says: an
// unauthenticated request to /mcp gets a 401 naming the protected-resource
// metadata, the metadata names the authorization server, and the access
// token that comes back is a JWT this host verifies against the server's
// keys, exactly as it verifies the edge's token for a human.
//
// What the token buys is still an agent's seat. Its subject becomes the
// agent's name, prefixed so a log reads who was asking, and nothing it
// claims — a scope, a role, an email — promotes it to the reviewer's side.

// metadataPath is the well-known path of RFC 9728. The resource being /mcp,
// the path-suffixed form is the one a client derives; the bare form is
// served too, for clients that ask at the root.
const (
	metadataPath = "/.well-known/oauth-protected-resource"
	mcpPath      = "/mcp"
)

// resourceURL is the identifier of the protected resource: the public /mcp.
func (h *Host) resourceURL() string {
	if h.opts.OAuthAudience != "" {
		return h.opts.OAuthAudience
	}
	return strings.TrimSuffix(h.opts.PublicURL, "/") + mcpPath
}

// metadataURL is where a client is sent to learn the authorization server.
func (h *Host) metadataURL() string {
	return strings.TrimSuffix(h.opts.PublicURL, "/") + metadataPath + mcpPath
}

// metadata is the RFC 9728 document: this resource, its authorization
// server, and that the token travels in the header.
func (h *Host) metadata() *oauthex.ProtectedResourceMetadata {
	return &oauthex.ProtectedResourceMetadata{
		Resource:               h.resourceURL(),
		AuthorizationServers:   []string{h.opts.OAuthIssuer},
		BearerMethodsSupported: []string{"header"},
		ResourceName:           "Marginalia document review",
	}
}

// oauthEnabled reports whether an authorization server is configured.
func (h *Host) oauthEnabled() bool { return h.opts.OAuth != nil && h.opts.OAuthIssuer != "" }

// agentVerifier is what /mcp checks a bearer against: the static token when
// one is configured, an access token from the authorization server when one
// is, either when both are. Nil when neither is, which the command allows on
// loopback only.
func (h *Host) agentVerifier() auth.TokenVerifier {
	var static auth.TokenVerifier
	if h.opts.Token != "" {
		static = mcpserver.StaticTokenVerifier(h.opts.Token, h.agentName())
	}
	if !h.oauthEnabled() {
		return static
	}
	oauth := h.opts.OAuth
	return func(ctx context.Context, raw string, r *http.Request) (*auth.TokenInfo, error) {
		// The static token first: a constant-time compare is cheap, and a
		// token that is not a JWT has nowhere else to go.
		if static != nil {
			if info, err := static(ctx, raw, r); err == nil {
				return info, nil
			}
		}
		claims, err := oauth.Verify(ctx, raw)
		if err != nil {
			if errors.Is(err, identity.ErrInvalidToken) {
				return nil, fmt.Errorf("%w: %v", auth.ErrInvalidToken, err)
			}
			// The authorization server's keys could not be fetched: the
			// SDK answers 500 for an error that is not the token's, which
			// is the honest status.
			return nil, err
		}
		name := claims.Name()
		if name == "" {
			return nil, fmt.Errorf("%w: the access token names nobody", auth.ErrInvalidToken)
		}
		info := &auth.TokenInfo{UserID: "agent:" + name}
		if claims.Expiry != nil {
			info.Expiration = claims.Expiry.Time()
		}
		return info, nil
	}
}

// transport is /mcp: the Streamable HTTP transport behind whatever verifies
// agents. With an authorization server configured, a refusal carries the
// metadata URL, which is how a client discovers where to get a token.
func (h *Host) transport() http.Handler {
	verifier := h.agentVerifier()
	var opts *auth.RequireBearerTokenOptions
	if h.oauthEnabled() {
		opts = &auth.RequireBearerTokenOptions{ResourceMetadataURL: h.metadataURL()}
	}
	return mcpserver.TransportWith(h.mcp, verifier, opts)
}

// agentWaitPrefix is where an agent long-polls a review over plain HTTP. It is
// a route of its own, beside /mcp and not under /r/, so the page routes stay
// the human's by construction: nothing here serves a document, a note or a
// write, and requireHuman has no exception to forget.
const agentWaitPrefix = "/agent/r/"

// agentWait is GET /agent/r/{id}/wait?since=N (agentwait.go), behind the
// same bearer check as /mcp. It reads the in-memory logs, so a wait costs
// the store nothing.
func (h *Host) agentWait() http.Handler {
	wait := http.HandlerFunc(h.handleAgentWait)
	verifier := h.agentVerifier()
	if verifier == nil {
		return wait
	}
	opts := &auth.RequireBearerTokenOptions{AllowMissingExpiration: true}
	if h.oauthEnabled() {
		opts.ResourceMetadataURL = h.metadataURL()
	}
	return auth.RequireBearerToken(verifier, opts)(wait)
}
