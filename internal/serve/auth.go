package serve

import (
	"crypto/rand"
	"crypto/subtle"
	"net"
	"net/http"
	"strconv"
	"strings"
)

// Loopback spellings and the cookie/bearer vocabulary the security matrix
// pins.
const (
	loopbackV4   = "127.0.0.1"
	loopbackName = "localhost"
	loopbackV6   = "::1"
	cookiePrefix = "verdict_session_"
	bearerPrefix = "Bearer "
	originScheme = "http://"
)

// newToken mints a 128-bit capability from the OS CSPRNG.
func newToken() string { return rand.Text() }

// tokenEqual compares a candidate against the session token in constant time,
// rejecting a length mismatch first.
func (s *Server) tokenEqual(candidate string) bool {
	if len(candidate) != len(s.token) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(candidate), []byte(s.token)) == 1
}

// cookieName is the per-port session cookie name.
func (s *Server) cookieName() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return cookiePrefix + strconv.Itoa(s.port)
}

// cookieValue reads the session cookie's value, empty when absent.
func (s *Server) cookieValue(r *http.Request) string {
	cookie, err := r.Cookie(s.cookieName())
	if err != nil {
		return ""
	}
	return cookie.Value
}

// authorized accepts the session cookie or one Authorization bearer header.
func (s *Server) authorized(r *http.Request) bool {
	if value := s.cookieValue(r); value != "" && s.tokenEqual(value) {
		return true
	}
	header := r.Header.Get("Authorization")
	value, ok := strings.CutPrefix(header, bearerPrefix)
	return ok && s.tokenEqual(value)
}

// tokenInQuery reports the forbidden capability spelling: a token in a URL
// instead of the exchange's one bootstrap.
func tokenInQuery(r *http.Request) bool {
	_, ok := r.URL.Query()["token"]
	return ok
}

// queryToken reads the exchange's token value, empty when absent.
func queryToken(r *http.Request) string {
	values := r.URL.Query()["token"]
	if len(values) == 0 {
		return ""
	}
	return values[0]
}

// setSessionCookie writes the exchange cookie: HttpOnly, SameSite=Strict,
// Path=/v1, no Max-Age (it dies with the browser), no Secure (plain HTTP on
// loopback).
func (s *Server) setSessionCookie(w http.ResponseWriter) {
	//nolint:gosec // loopback plain HTTP is the documented transport; the manual states the missing Secure attribute.
	http.SetCookie(w, &http.Cookie{
		Name:     s.cookieName(),
		Value:    s.token,
		Path:     "/v1",
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
	})
}

// isLoopback reports whether a bind host stays on the local machine.
func isLoopback(host string) bool {
	switch host {
	case loopbackV4, loopbackName, loopbackV6:
		return true
	default:
		return false
	}
}

// hostAllowed accepts only Host spellings that name the listen address. A
// DNS-rebinding page arrives with its own name and is refused here.
func (s *Server) hostAllowed(host string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.hosts[host]
}

// originAllowed accepts an absent Origin or one of the loopback origins. No
// CORS headers are ever emitted, so a cross-origin page cannot read a body.
func (s *Server) originAllowed(origin string) bool {
	if origin == "" {
		return true
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.origins[origin]
}

// refreshAllowlists rebuilds the Host and Origin sets from the resolved
// listen port.
func (s *Server) refreshAllowlists() {
	port := strconv.Itoa(s.port)
	hosts := map[string]bool{
		net.JoinHostPort(loopbackV4, port):   true,
		net.JoinHostPort(loopbackName, port): true,
		net.JoinHostPort(loopbackV6, port):   true,
	}
	origins := map[string]bool{
		originScheme + net.JoinHostPort(loopbackV4, port):   true,
		originScheme + net.JoinHostPort(loopbackName, port): true,
		originScheme + net.JoinHostPort(loopbackV6, port):   true,
	}
	if !isLoopback(s.cfg.Host) {
		host := net.JoinHostPort(s.cfg.Host, port)
		hosts[host] = true
		origins[originScheme+host] = true
	}
	s.mu.Lock()
	s.hosts = hosts
	s.origins = origins
	s.mu.Unlock()
}
