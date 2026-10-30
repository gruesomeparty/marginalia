// Package identity is who is asking. A hosted instance (PRD §5.9) serves two
// kinds of caller and lets each do different things: a human, authenticated
// at the edge and carrying its JWT, may append anything the review allows;
// an agent, holding a bearer token on /mcp, may answer and claim and never
// decide. Resolving which is which happens once per request, before any
// handler runs, and the result travels in the request context.
//
// Nothing here trusts a header on its own. The edge's JWT is verified
// against the issuer's published keys, with issuer, audience and validity
// checked: the container's port may be reachable around the edge, and a
// header anyone can type is not an identity.
package identity

import (
	"context"
	"errors"
)

// Kind is which side of the review a caller is on.
type Kind int

const (
	// None is the zero value: nobody has been resolved.
	None Kind = iota
	// Human is a reviewer, authenticated at the edge.
	Human
	// Agent is a requester, holding a credential for /mcp.
	Agent
)

func (k Kind) String() string {
	switch k {
	case Human:
		return "human"
	case Agent:
		return "agent"
	}
	return "none"
}

// Identity is a resolved caller. Name is what is written as an event's
// author: the human's email, or the agent's configured name. Provider and
// Subject say who vouched for the caller and under which stable id, so two
// logins can one day be told apart from two names: a name can change, a
// subject at its provider does not.
type Identity struct {
	Kind Kind
	Name string
	// Provider is what authenticated the caller: "edge" for the JWT the
	// edge put on a page request, "dev" for --insecure-dev-identity,
	// "static" for the /mcp token, "oauth" for an access token.
	Provider string
	// Subject is the caller's id at Provider (a token's sub); empty when
	// the provider has none, as for the static token.
	Subject string
}

// ErrUnauthenticated is returned when a request carries no credential the
// resolver recognises.
var ErrUnauthenticated = errors.New("identity: no credential")

type ctxKey struct{}

// WithIdentity records who is asking on a context.
func WithIdentity(ctx context.Context, id Identity) context.Context {
	return context.WithValue(ctx, ctxKey{}, id)
}

// FromContext returns the caller recorded on ctx, if any.
func FromContext(ctx context.Context) (Identity, bool) {
	id, ok := ctx.Value(ctxKey{}).(Identity)
	return id, ok && id.Kind != None
}
