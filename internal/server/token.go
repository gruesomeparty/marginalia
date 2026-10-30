package server

import (
	"crypto/sha256"
	"crypto/subtle"
	"net/http"
	"net/url"
	"strings"
)

// tokenCookie carries the token after a browser has shown it once, so a
// phone opens the URL with ?token= a single time and then browses normally.
const tokenCookie = "marginalia_token"

// gate refuses every request that does not carry Options.Token (issue #54):
// as a bearer header, the cookie, or a one-time ?token= that sets the cookie
// and redirects to the same page without it, so the secret does not sit in
// the address bar or the history. With no token configured it is the mux
// itself — a loopback `serve` behaves exactly as it always has.
func (s *Server) gate(next http.Handler) http.Handler {
	if s.opts.Token == "" {
		return next
	}
	want := sha256.Sum256([]byte(s.opts.Token))
	ok := func(got string) bool {
		sum := sha256.Sum256([]byte(got))
		return got != "" && subtle.ConstantTimeCompare(sum[:], want[:]) == 1
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if t := r.URL.Query().Get("token"); t != "" && r.Method == http.MethodGet {
			if !ok(t) {
				refuse(w, r, "that token is not this review's")
				return
			}
			// Secure only over TLS: `serve` speaks plain HTTP, on loopback or
			// inside a tailnet that encrypts underneath it.
			http.SetCookie(w, &http.Cookie{ //nolint:gosec // see above; HttpOnly and SameSite are set
				Name: tokenCookie, Value: t, Path: "/",
				HttpOnly: true, SameSite: http.SameSiteStrictMode, Secure: r.TLS != nil,
			})
			http.Redirect(w, r, withoutToken(r.URL), http.StatusSeeOther) //nolint:gosec // withoutToken keeps only this server's path and query

			return
		}
		if b, found := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer "); found && ok(b) {
			next.ServeHTTP(w, r)
			return
		}
		if c, err := r.Cookie(tokenCookie); err == nil && ok(c.Value) {
			next.ServeHTTP(w, r)
			return
		}
		refuse(w, r, "this review needs its token")
	})
}

// refuse answers 401. A page request gets a one-field form that sends the
// token back as ?token=, so a reviewer handed a bare URL can still get in;
// an API call gets the reason as text.
func refuse(w http.ResponseWriter, r *http.Request, why string) {
	w.Header().Set("WWW-Authenticate", `Bearer realm="marginalia"`)
	w.Header().Set("Cache-Control", "no-store")
	if r.Method != http.MethodGet || strings.HasPrefix(r.URL.Path, "/api/") {
		http.Error(w, why, http.StatusUnauthorized)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusUnauthorized)
	_, _ = w.Write([]byte(`<!doctype html><html lang="en"><meta charset="utf-8">` +
		`<meta name="viewport" content="width=device-width,initial-scale=1">` +
		`<title>Marginalia: token needed</title>` +
		`<body style="font-family:system-ui,sans-serif;max-width:28rem;margin:4rem auto;padding:0 1rem">` +
		`<h1 style="font-size:1.25rem">This review needs its token</h1>` +
		`<p>Ask whoever sent you the link for it.</p>` +
		`<form method="get"><label>Token <input name="token" type="password" autocomplete="off" required autofocus></label> ` +
		`<button type="submit">Open</button></form></body></html>`))
}

// withoutToken is the same page with the token taken out of its query. Only
// the path and query are kept, and the path is given exactly one leading
// slash, so `//elsewhere.example/?token=…` cannot become a redirect off
// this server.
func withoutToken(u *url.URL) string {
	q := u.Query()
	q.Del("token")
	out := "/" + strings.TrimLeft(u.EscapedPath(), "/")
	if len(q) > 0 {
		out += "?" + q.Encode()
	}
	return out
}
