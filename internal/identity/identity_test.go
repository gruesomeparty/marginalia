package identity

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	jose "github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"
)

// issuer is a fake edge: a key pair, a JWKS endpoint serving the public
// half, and a way to mint tokens with any claims a test wants.
type issuer struct {
	t     *testing.T
	name  string
	keys  []jose.JSONWebKey // private, with kids
	srv   *httptest.Server
	hits  atomic.Int64
	serve []jose.JSONWebKey // what the endpoint publishes
}

func newIssuer(t *testing.T) *issuer {
	t.Helper()
	is := &issuer{t: t}
	is.addRSA("k1")
	is.serve = is.keys
	is.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		is.hits.Add(1)
		set := jose.JSONWebKeySet{}
		for _, k := range is.serve {
			set.Keys = append(set.Keys, k.Public())
		}
		_ = json.NewEncoder(w).Encode(set)
	}))
	t.Cleanup(is.srv.Close)
	is.name = "https://team.example"
	return is
}

func (is *issuer) addRSA(kid string) jose.JSONWebKey {
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		is.t.Fatal(err)
	}
	k := jose.JSONWebKey{Key: priv, KeyID: kid, Algorithm: string(jose.RS256), Use: "sig"}
	is.keys = append(is.keys, k)
	return k
}

func (is *issuer) addEC(kid string) jose.JSONWebKey {
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		is.t.Fatal(err)
	}
	k := jose.JSONWebKey{Key: priv, KeyID: kid, Algorithm: string(jose.ES256), Use: "sig"}
	is.keys = append(is.keys, k)
	return k
}

// mint signs claims with the named key.
func (is *issuer) mint(key jose.JSONWebKey, claims any) string {
	is.t.Helper()
	signer, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.SignatureAlgorithm(key.Algorithm), Key: key}, (&jose.SignerOptions{}).WithType("JWT"))
	if err != nil {
		is.t.Fatal(err)
	}
	raw, err := jwt.Signed(signer).Claims(claims).Serialize()
	if err != nil {
		is.t.Fatal(err)
	}
	return raw
}

func (is *issuer) verifier() *Verifier {
	return &Verifier{Issuer: is.name, Audience: "aud-1", JWKSURL: is.srv.URL, MinRefresh: time.Minute}
}

func good(is *issuer) Claims {
	return Claims{Claims: jwt.Claims{
		Issuer: is.name, Subject: "user-1", Audience: jwt.Audience{"aud-1"},
		Expiry: jwt.NewNumericDate(time.Now().Add(time.Hour)), IssuedAt: jwt.NewNumericDate(time.Now()),
	}, Email: "berkay@example.com"}
}

func TestVerifyAcceptsAValidToken(t *testing.T) {
	is := newIssuer(t)
	ec := is.addEC("k2")
	is.serve = is.keys
	v := is.verifier()
	claims, err := v.Verify(context.Background(), is.mint(is.keys[0], good(is)))
	if err != nil {
		t.Fatal(err)
	}
	if claims.Name() != "berkay@example.com" || claims.Subject != "user-1" {
		t.Errorf("claims = %+v", claims)
	}
	// An EC key works the same way.
	claims, err = v.Verify(context.Background(), is.mint(ec, good(is)))
	if err != nil {
		t.Fatal(err)
	}
	if claims.Email != "berkay@example.com" {
		t.Errorf("EC claims = %+v", claims)
	}
}

func TestVerifyRefusesWhatItMust(t *testing.T) {
	is := newIssuer(t)
	other := newIssuer(t) // a different key pair entirely
	v := is.verifier()
	cases := map[string]string{}
	c := good(is)
	c.Expiry = jwt.NewNumericDate(time.Now().Add(-2 * time.Hour))
	cases["expired"] = is.mint(is.keys[0], c)
	c = good(is)
	c.Audience = jwt.Audience{"someone-else"}
	cases["wrong audience"] = is.mint(is.keys[0], c)
	c = good(is)
	c.Issuer = "https://evil.example"
	cases["wrong issuer"] = is.mint(is.keys[0], c)
	c = good(is)
	c.NotBefore = jwt.NewNumericDate(time.Now().Add(time.Hour))
	cases["not yet valid"] = is.mint(is.keys[0], c)
	c = good(is)
	c.Expiry = nil
	cases["no expiry"] = is.mint(is.keys[0], c)
	k := other.keys[0]
	k.KeyID = "k1" // same kid as ours, different key
	cases["signed by another key"] = other.mint(k, good(is))
	cases["garbage"] = "not.a.jwt"
	cases["empty"] = ""
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := v.Verify(context.Background(), raw)
			if !errors.Is(err, ErrInvalidToken) {
				t.Fatalf("got %v, want ErrInvalidToken", err)
			}
		})
	}
	// None of those is a key-set problem.
	if got := is.hits.Load(); got > 2 {
		t.Errorf("the JWKS was fetched %d times for bad tokens; refreshes on unknown kids are rate-limited", got)
	}
}

func TestVerifyRefusesHMAC(t *testing.T) {
	// A token whose header says HS256 must not be accepted even when the
	// key set could be made to "verify" it: the algorithm list is the gate.
	is := newIssuer(t)
	v := is.verifier()
	signer, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.HS256, Key: []byte("a-shared-secret-of-thirty-two-by")}, nil)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := jwt.Signed(signer).Claims(good(is)).Serialize()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := v.Verify(context.Background(), raw); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("HS256 was accepted: %v", err)
	}
}

// Keys rotate. A token under a kid the cache does not hold refreshes the set
// once; the same unknown kid again within the window does not.
func TestVerifyRefreshesOnUnknownKid(t *testing.T) {
	is := newIssuer(t)
	v := is.verifier()
	if _, err := v.Verify(context.Background(), is.mint(is.keys[0], good(is))); err != nil {
		t.Fatal(err)
	}
	before := is.hits.Load()
	rotated := is.addRSA("k2")
	is.serve = is.keys
	// First sight of k2 refreshes the set once the rate limit allows; the
	// first fetch was just now, so let it.
	v.MinRefresh = time.Nanosecond
	if _, err := v.Verify(context.Background(), is.mint(rotated, good(is))); err != nil {
		t.Fatalf("a rotated key was not picked up: %v", err)
	}
	if is.hits.Load() != before+1 {
		t.Errorf("fetches went %d → %d, want exactly one refresh", before, is.hits.Load())
	}
	// And a kid nobody publishes is refused without a fetch per attempt.
	v.MinRefresh = time.Hour
	ghost := jose.JSONWebKey{Key: is.keys[0].Key, KeyID: "ghost", Algorithm: string(jose.RS256)}
	after := is.hits.Load()
	for i := 0; i < 5; i++ {
		if _, err := v.Verify(context.Background(), is.mint(ghost, good(is))); !errors.Is(err, ErrInvalidToken) {
			t.Fatalf("unknown kid accepted: %v", err)
		}
	}
	if is.hits.Load() != after {
		t.Errorf("unknown kids caused %d extra fetches", is.hits.Load()-after)
	}
}

func TestVerifyReportsAnUnreachableIssuerAsSuch(t *testing.T) {
	is := newIssuer(t)
	v := is.verifier()
	is.srv.Close()
	_, err := v.Verify(context.Background(), is.mint(is.keys[0], good(is)))
	if err == nil || errors.Is(err, ErrInvalidToken) {
		t.Fatalf("an unreachable JWKS must not read as a bad token: %v", err)
	}
}

func TestJWKSURLFor(t *testing.T) {
	got, err := JWKSURLFor(context.Background(), nil, "https://team.cloudflareaccess.com/")
	if err != nil || got != "https://team.cloudflareaccess.com/cdn-cgi/access/certs" {
		t.Errorf("Access: %q, %v", got, err)
	}
	oidc := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/.well-known/openid-configuration" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(`{"issuer":"x","jwks_uri":"https://keys.example/jwks.json"}`))
	}))
	defer oidc.Close()
	got, err = JWKSURLFor(context.Background(), oidc.Client(), oidc.URL)
	if err != nil || got != "https://keys.example/jwks.json" {
		t.Errorf("OIDC: %q, %v", got, err)
	}
	broken := httptest.NewServer(http.NotFoundHandler())
	defer broken.Close()
	if _, err := JWKSURLFor(context.Background(), broken.Client(), broken.URL); err == nil || !strings.Contains(err.Error(), "explicitly") {
		t.Errorf("no discovery: %v", err)
	}
}

func TestContextRoundTrip(t *testing.T) {
	ctx := WithIdentity(context.Background(), Identity{Kind: Human, Name: "a@b"})
	id, ok := FromContext(ctx)
	if !ok || id.Kind != Human || id.Name != "a@b" || id.Kind.String() != "human" {
		t.Errorf("got %+v %v", id, ok)
	}
	if _, ok := FromContext(context.Background()); ok {
		t.Error("an empty context has an identity")
	}
}
