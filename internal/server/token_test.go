package server

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gruesomeparty/marginalia/internal/document"
	"github.com/gruesomeparty/marginalia/internal/feedback"
)

func newTokenServer(t *testing.T) (*Server, *feedback.Store) {
	t.Helper()
	docPath := filepath.Join(t.TempDir(), "d.md")
	if err := os.WriteFile(docPath, []byte("# Title\n\nHello.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	doc, err := document.Parse(docPath)
	if err != nil {
		t.Fatal(err)
	}
	store := feedback.NewStore(docPath)
	return New(Options{Doc: doc, Store: store, Author: "berkay", Token: "s3cret"}), store
}

func hit(h http.Handler, method, target, body string, set func(*http.Request)) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, target, strings.NewReader(body))
	if set != nil {
		set(r)
	}
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, r)
	return rr
}

// With a token set, nothing is served without it — the page, the API, a
// write — and everything is with it, by header or by the cookie a one-time
// ?token= leaves behind (issue #54).
func TestTokenGatesPageAndAPI(t *testing.T) {
	s, store := newTokenServer(t)
	h := s.Handler()
	note := `{"block":"1/2","type":"comment","text":"x"}`
	for _, c := range []struct{ method, target, body string }{
		{"GET", "/", ""},
		{"GET", "/api/doc", ""},
		{"GET", "/api/feedback", ""},
		{"POST", "/api/feedback", note},
		{"GET", "/?token=wrong", ""},
	} {
		if rr := hit(h, c.method, c.target, c.body, nil); rr.Code != http.StatusUnauthorized {
			t.Errorf("%s %s without the token: %d", c.method, c.target, rr.Code)
		}
	}
	if rr := hit(h, "GET", "/", "", nil); !strings.Contains(rr.Body.String(), `name="token"`) {
		t.Error("a refused page should offer a way to give the token")
	}
	if rr := hit(h, "GET", "/api/doc", "", func(r *http.Request) { r.Header.Set("Authorization", "Bearer wrong") }); rr.Code != http.StatusUnauthorized {
		t.Errorf("a wrong bearer: %d", rr.Code)
	}
	if events, _ := store.Load(); len(events) != 0 {
		t.Fatalf("a refused write reached the log: %+v", events)
	}

	bearer := func(r *http.Request) { r.Header.Set("Authorization", "Bearer s3cret") }
	if rr := hit(h, "GET", "/api/doc", "", bearer); rr.Code != 200 {
		t.Errorf("GET /api/doc with the bearer: %d", rr.Code)
	}

	// The one-time link: a cookie, and a redirect that drops the secret.
	rr := hit(h, "GET", "/d/x?token=s3cret&k=v", "", nil)
	if rr.Code != http.StatusSeeOther || rr.Header().Get("Location") != "/d/x?k=v" {
		t.Fatalf("?token= gave %d → %q", rr.Code, rr.Header().Get("Location"))
	}
	if rr := hit(h, "GET", "//evil.example/?token=s3cret", "", nil); rr.Header().Get("Location") != "/evil.example/" {
		t.Errorf("a protocol-relative path redirected to %q", rr.Header().Get("Location"))
	}
	if rr := hit(h, "GET", "/%5Cevil.example/?token=s3cret", "", nil); strings.Contains(rr.Header().Get("Location"), `\`) {
		t.Errorf("a backslash path redirected to %q", rr.Header().Get("Location"))
	}
	cookies := rr.Result().Cookies()
	if len(cookies) != 1 || !cookies[0].HttpOnly || cookies[0].SameSite != http.SameSiteLaxMode {
		t.Fatalf("cookie %+v", cookies)
	}
	if strings.Contains(cookies[0].Value, "s3cret") {
		t.Error("the cookie carries the token itself")
	}
	// The cookie is not a bearer, and the token is not a cookie.
	if rr := hit(h, "GET", "/api/doc", "", func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+cookies[0].Value) }); rr.Code != http.StatusUnauthorized {
		t.Errorf("the cookie's value as a bearer: %d", rr.Code)
	}
	if rr := hit(h, "GET", "/", "", func(r *http.Request) { r.Header.Set("Cookie", cookies[0].Name+"=s3cret") }); rr.Code != http.StatusUnauthorized {
		t.Errorf("the raw token as the cookie: %d", rr.Code)
	}
	withCookie := func(r *http.Request) { r.AddCookie(cookies[0]) }
	if rr := hit(h, "GET", "/", "", withCookie); rr.Code != 200 {
		t.Errorf("GET / with the cookie: %d", rr.Code)
	}
	if rr := hit(h, "POST", "/api/feedback", note, withCookie); rr.Code != http.StatusCreated {
		t.Errorf("POST with the cookie: %d %s", rr.Code, rr.Body)
	}
}

// Past the token the server says who wrote a note, not the body.
func TestTokenPinsTheAuthor(t *testing.T) {
	s, store := newTokenServer(t)
	body := `{"block":"1/2","type":"comment","text":"x","author":"someone else"}`
	rr := hit(s.Handler(), "POST", "/api/feedback", body, func(r *http.Request) { r.Header.Set("Authorization", "Bearer s3cret") })
	if rr.Code != http.StatusCreated {
		t.Fatalf("%d %s", rr.Code, rr.Body)
	}
	events, _ := store.Load()
	if len(events) != 1 || events[0].Author != "berkay" {
		t.Fatalf("logged %+v, want author berkay", events)
	}
}

// No token, no change: the body's author stands and nothing asks for one.
func TestNoTokenIsUnchanged(t *testing.T) {
	s, store, _ := newTestServer(t)
	rr := hit(s.Handler(), "POST", "/api/feedback", `{"block":"1/2","type":"comment","text":"x","author":"me"}`, nil)
	if rr.Code != http.StatusCreated {
		t.Fatalf("%d", rr.Code)
	}
	if events, _ := store.Load(); len(events) != 1 || events[0].Author != "me" {
		t.Fatalf("logged %+v", events)
	}
}

// The cookie's name carries the port and nothing that would make it
// invalid: reached over IPv6, the host is full of colons and brackets.
func TestTokenCookieWorksOverIPv6(t *testing.T) {
	s, _ := newTokenServer(t)
	h := s.Handler()
	onHost := func(host string) func(*http.Request) { return func(r *http.Request) { r.Host = host } }
	for host, want := range map[string]string{"[::1]:8787": "marginalia_token_8787", "[fd7a:115c::1]:9000": "marginalia_token_9000", "example": "marginalia_token_80"} {
		rr := hit(h, "GET", "/?token=s3cret", "", onHost(host))
		cookies := rr.Result().Cookies()
		if len(cookies) != 1 || cookies[0].Name != want {
			t.Fatalf("%s: cookies %+v, want one named %s", host, cookies, want)
		}
		withCookie := func(r *http.Request) { r.Host = host; r.AddCookie(cookies[0]) }
		if rr := hit(h, "GET", "/", "", withCookie); rr.Code != http.StatusOK {
			t.Errorf("%s: GET / with the cookie: %d", host, rr.Code)
		}
	}
}
