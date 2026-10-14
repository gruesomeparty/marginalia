package identity

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	jose "github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"
)

// Claims is what a verified token says about its bearer: the standard
// claims, plus the email an edge login carries.
type Claims struct {
	jwt.Claims
	Email string `json:"email,omitempty"`
}

// Name is the identity a token resolves to: the email when there is one,
// else the subject.
func (c Claims) Name() string {
	if c.Email != "" {
		return c.Email
	}
	return c.Subject
}

// Verifier checks JWTs against one issuer's published keys (a JWKS). It is
// the one place signatures are checked, and it does not check them itself:
// go-jose does, against a key the issuer published under the token's kid.
//
// Keys are cached. An unknown kid triggers a refresh, rate-limited so a
// flood of bad tokens cannot turn the verifier into a client that hammers
// the issuer; a stale set is refreshed in the background of a verify once
// it is older than Refresh.
type Verifier struct {
	// Issuer is what the token's iss must equal, exactly.
	Issuer string
	// Audience is what the token's aud must contain.
	Audience string
	// JWKSURL is where the issuer publishes its keys.
	JWKSURL string
	// Algorithms the verifier accepts. Nil means RS256, RS384, RS512,
	// ES256, ES384, ES512 and EdDSA — every asymmetric algorithm an edge
	// is known to sign with, and no HMAC: a shared secret is not a thing a
	// JWKS can publish.
	Algorithms []jose.SignatureAlgorithm
	// Client fetches the key set; nil means http.DefaultClient with a
	// timeout.
	Client *http.Client
	// Refresh is how old a cached key set may get before a verify refreshes
	// it in the background; MinRefresh is the least time between two
	// refreshes forced by an unknown kid. Zero means 1h and 1m.
	Refresh, MinRefresh time.Duration
	// Leeway is the clock skew tolerated on exp and nbf. Zero means 1m.
	Leeway time.Duration
	// Now is the clock, injectable for tests.
	Now func() time.Time

	mu      sync.Mutex
	keys    *jose.JSONWebKeySet
	fetched time.Time
}

var defaultAlgorithms = []jose.SignatureAlgorithm{jose.RS256, jose.RS384, jose.RS512, jose.ES256, jose.ES384, jose.ES512, jose.EdDSA}

// ErrInvalidToken wraps every refusal, so a caller can tell "this token is
// bad" from "the issuer could not be reached" without reading the message.
var ErrInvalidToken = errors.New("identity: invalid token")

func (v *Verifier) now() time.Time {
	if v.Now != nil {
		return v.Now()
	}
	return time.Now()
}

func (v *Verifier) client() *http.Client {
	if v.Client != nil {
		return v.Client
	}
	return &http.Client{Timeout: 10 * time.Second}
}

func (v *Verifier) refresh() time.Duration {
	if v.Refresh > 0 {
		return v.Refresh
	}
	return time.Hour
}

func (v *Verifier) minRefresh() time.Duration {
	if v.MinRefresh > 0 {
		return v.MinRefresh
	}
	return time.Minute
}

func (v *Verifier) leeway() time.Duration {
	if v.Leeway > 0 {
		return v.Leeway
	}
	return time.Minute
}

// Verify checks raw and returns its claims. Every failure is wrapped in
// ErrInvalidToken except a key set that could not be fetched, which is the
// issuer's problem rather than the token's and is returned as is.
func (v *Verifier) Verify(ctx context.Context, raw string) (Claims, error) {
	algs := v.Algorithms
	if algs == nil {
		algs = defaultAlgorithms
	}
	tok, err := jwt.ParseSigned(raw, algs)
	if err != nil {
		return Claims{}, fmt.Errorf("%w: %v", ErrInvalidToken, err)
	}
	if len(tok.Headers) != 1 {
		return Claims{}, fmt.Errorf("%w: %d signatures", ErrInvalidToken, len(tok.Headers))
	}
	kid := tok.Headers[0].KeyID
	key, err := v.key(ctx, kid)
	if err != nil {
		return Claims{}, err
	}
	var claims Claims
	if err := tok.Claims(key, &claims); err != nil {
		return Claims{}, fmt.Errorf("%w: %v", ErrInvalidToken, err)
	}
	expected := jwt.Expected{Issuer: v.Issuer, Time: v.now()}
	if v.Audience != "" {
		expected.AnyAudience = jwt.Audience{v.Audience}
	}
	if err := claims.ValidateWithLeeway(expected, v.leeway()); err != nil {
		return Claims{}, fmt.Errorf("%w: %v", ErrInvalidToken, err)
	}
	// A token with no expiry would be valid forever; an edge never issues
	// one, and accepting one would make a leaked token permanent.
	if claims.Expiry == nil {
		return Claims{}, fmt.Errorf("%w: no exp claim", ErrInvalidToken)
	}
	return claims, nil
}

// key returns the public key published under kid, fetching the set when it
// is not cached, too old, or does not hold the kid — the last rate-limited.
func (v *Verifier) key(ctx context.Context, kid string) (any, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	now := v.now()
	if v.keys == nil || now.Sub(v.fetched) > v.refresh() {
		if err := v.fetchLocked(ctx); err != nil {
			return nil, err
		}
	}
	if k, ok := v.lookup(kid); ok {
		return k, nil
	}
	// Unknown kid: the issuer may have rotated. Refresh, but not for every
	// bad token that comes in.
	if now.Sub(v.fetched) >= v.minRefresh() {
		if err := v.fetchLocked(ctx); err != nil {
			return nil, err
		}
		if k, ok := v.lookup(kid); ok {
			return k, nil
		}
	}
	return nil, fmt.Errorf("%w: no key %q at %s", ErrInvalidToken, kid, v.JWKSURL)
}

func (v *Verifier) lookup(kid string) (any, bool) {
	if v.keys == nil {
		return nil, false
	}
	for _, k := range v.keys.Keys {
		if k.KeyID == kid && (k.Use == "" || k.Use == "sig") && k.Valid() && k.IsPublic() {
			return k.Key, true
		}
	}
	return nil, false
}

// fetchLocked downloads the key set. Called with mu held.
func (v *Verifier) fetchLocked(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, v.JWKSURL, nil)
	if err != nil {
		return fmt.Errorf("identity: jwks: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	res, err := v.client().Do(req)
	if err != nil {
		return fmt.Errorf("identity: jwks %s: %w", v.JWKSURL, err)
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("identity: jwks %s: HTTP %d", v.JWKSURL, res.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if err != nil {
		return fmt.Errorf("identity: jwks %s: %w", v.JWKSURL, err)
	}
	var set jose.JSONWebKeySet
	if err := json.Unmarshal(body, &set); err != nil {
		return fmt.Errorf("identity: jwks %s: %w", v.JWKSURL, err)
	}
	v.keys = &set
	v.fetched = v.now()
	return nil
}

// JWKSURLFor is the key-set URL an issuer publishes by convention, for the
// case where the operator named the issuer and nothing else: Cloudflare
// Access serves its keys at /cdn-cgi/access/certs under the team domain;
// anything else is assumed to follow OpenID Connect discovery, whose
// jwks_uri this resolves by one fetch of the well-known document.
func JWKSURLFor(ctx context.Context, client *http.Client, issuer string) (string, error) {
	issuer = strings.TrimSuffix(issuer, "/")
	if strings.HasSuffix(issuer, ".cloudflareaccess.com") {
		return issuer + "/cdn-cgi/access/certs", nil
	}
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, issuer+"/.well-known/openid-configuration", nil)
	if err != nil {
		return "", err
	}
	res, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("identity: discovering %s: %w", issuer, err)
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		return "", fmt.Errorf("identity: discovering %s: HTTP %d — pass the key-set URL explicitly", issuer, res.StatusCode)
	}
	var doc struct {
		JWKSURI string `json:"jwks_uri"`
	}
	if err := json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(&doc); err != nil || doc.JWKSURI == "" {
		return "", fmt.Errorf("identity: discovering %s: no jwks_uri in its OpenID configuration — pass the key-set URL explicitly", issuer)
	}
	return doc.JWKSURI, nil
}
