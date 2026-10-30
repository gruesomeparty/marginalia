package hosted

import (
	"errors"
	"net/http"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/gruesomeparty/marginalia/internal/identity"
	"github.com/gruesomeparty/marginalia/internal/mcpserver"
)

// Cloudflare Access puts the user's JWT on every request it forwards, in a
// header, and on the browser as a cookie. Both are verified the same way;
// neither is trusted on its own. They are the defaults, not the only names:
// an OIDC proxy in front of a Kubernetes ingress forwards the same kind of
// token under a header of its own choosing (Options.HumanHeader).
const (
	DefaultHumanHeader = "Cf-Access-Jwt-Assertion"
	DefaultHumanCookie = "CF_Authorization"
)

// checkHumanHeader refuses a header the human token cannot ride in.
// Authorization is the agent's: a bearer on a page route is refused before
// any token is looked at, so a proxy configured to forward the reviewer's
// token there would lock every reviewer out — and telling the two apart by
// trying the token would let an agent's credential be read as a human's.
func checkHumanHeader(name string) error {
	if strings.EqualFold(strings.TrimSpace(name), "Authorization") {
		return errors.New("hosted: the human token cannot ride in Authorization — that header is the agent's credential and page routes refuse it; have the proxy forward the token in a header of its own (e.g. X-Auth-Request-Id-Token)")
	}
	return nil
}

// human resolves the reviewer behind a page request, or says why it cannot.
// The order matters: an agent credential on a page route is refused before
// anything else is looked at, because an agent that could open the page
// could append a verdict through it — the exact thing the tool surface is
// built to make impossible.
func (h *Host) human(r *http.Request) (identity.Identity, int, error) {
	if strings.HasPrefix(strings.ToLower(r.Header.Get("Authorization")), "bearer ") {
		return identity.Identity{}, http.StatusForbidden, errors.New("an agent credential cannot open review pages — agents use /mcp")
	}
	if h.opts.DevIdentity != "" {
		return h.admit(identity.Identity{Kind: identity.Human, Name: h.opts.DevIdentity, Provider: "dev"})
	}
	if h.opts.HumanAuth == nil {
		return identity.Identity{}, http.StatusServiceUnavailable, errors.New("no human identity is configured on this host")
	}
	raw := r.Header.Get(h.humanHeader())
	if raw == "" && h.humanCookie() != "" {
		if c, err := r.Cookie(h.humanCookie()); err == nil {
			raw = c.Value
		}
	}
	if raw == "" {
		return identity.Identity{}, http.StatusUnauthorized, errors.New("sign in at the edge: no access token on this request")
	}
	claims, err := h.opts.HumanAuth.Verify(r.Context(), raw)
	if err != nil {
		if errors.Is(err, identity.ErrInvalidToken) {
			return identity.Identity{}, http.StatusUnauthorized, errors.New("the access token was refused: " + err.Error())
		}
		// The issuer's keys could not be fetched: not the reviewer's fault,
		// and not a reason to let anyone in.
		h.logf("marginalia: cannot verify access tokens: %v\n", err)
		return identity.Identity{}, http.StatusServiceUnavailable, errors.New("the access token could not be verified right now")
	}
	name := claims.Name()
	if name == "" {
		return identity.Identity{}, http.StatusUnauthorized, errors.New("the access token names nobody: no email or sub claim")
	}
	return h.admit(identity.Identity{Kind: identity.Human, Name: name, Provider: "edge", Subject: claims.Subject})
}

// admit lets a verified human in when the host has no reviewer list, or
// when they are on it. The edge deciding who may log in is not the same as
// this host deciding who may review: one identity provider often fronts
// many applications, and the list is where more than one reviewer starts.
// Names compare without case, since an email's or a login's case is not
// part of who it names.
func (h *Host) admit(id identity.Identity) (identity.Identity, int, error) {
	if len(h.opts.Reviewers) == 0 {
		return id, 0, nil
	}
	for _, r := range h.opts.Reviewers {
		if strings.EqualFold(strings.TrimSpace(r), id.Name) {
			return id, 0, nil
		}
	}
	return identity.Identity{}, http.StatusForbidden, errors.New(id.Name + " is signed in but is not a reviewer on this host")
}

func (h *Host) humanHeader() string { return firstNonEmpty(h.opts.HumanHeader, DefaultHumanHeader) }

// humanCookie is the cookie a browser carries the token in; "-" turns the
// fallback off, for a proxy whose session cookie is not a JWT at all.
func (h *Host) humanCookie() string {
	if h.opts.HumanCookie == "-" {
		return ""
	}
	return firstNonEmpty(h.opts.HumanCookie, DefaultHumanCookie)
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// requireHuman gates a page route: the request proceeds with the reviewer's
// identity on its context, or is refused with the reason.
func (h *Host) requireHuman(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, code, err := h.human(r)
		if err != nil {
			http.Error(w, err.Error(), code)
			return
		}
		next.ServeHTTP(w, r.WithContext(identity.WithIdentity(r.Context(), id)))
	})
}

// authorOf is what the per-review server writes as an event's author: the
// identity requireHuman resolved, whatever the body claimed. A page route
// never runs without one, so an empty name here is a programming error
// rather than a request to accommodate.
func authorOf(r *http.Request) string {
	id, _ := identity.FromContext(r.Context())
	return id.Name
}

// agentName is the configured name agents write under.
func (h *Host) agentName() string {
	if h.opts.AgentName != "" {
		return h.opts.AgentName
	}
	return mcpserver.DefaultAgentName
}

// agentOf is the identity written on an agent's events: what the bearer
// verifier recorded for this request, which for the static token is the
// configured agent name. The request's own say in the matter is nil.
func (h *Host) agentOf(req *mcp.CallToolRequest) string {
	if req != nil && req.Extra != nil && req.Extra.TokenInfo != nil && req.Extra.TokenInfo.UserID != "" {
		return req.Extra.TokenInfo.UserID
	}
	return h.agentName()
}
