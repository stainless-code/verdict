package serve

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// contentTypeJSON is the one body shape a data or problem response carries.
const contentTypeJSON = "application/json; charset=utf-8"

// cspPolicy is the exact Content-Security-Policy every response carries: no
// inline script or style, no external origin, no framing, no form action.
const cspPolicy = "default-src 'self'; script-src 'self'; style-src 'self'; " +
	"img-src 'self' data:; connect-src 'self'; base-uri 'none'; " +
	"form-action 'none'; frame-ancestors 'none'"

// securityHeader is one frozen response header.
type securityHeader struct {
	name  string
	value string
}

// securityHeaders are applied before a route can write its own status.
var securityHeaders = []securityHeader{
	{name: "Content-Security-Policy", value: cspPolicy},
	{name: "Referrer-Policy", value: "no-referrer"},
	{name: "X-Content-Type-Options", value: "nosniff"},
	{name: "Cross-Origin-Resource-Policy", value: "same-origin"},
	{name: "Cross-Origin-Opener-Policy", value: "same-origin"},
	{name: "Permissions-Policy", value: "camera=(), microphone=(), geolocation=()"},
	{name: "Cache-Control", value: "no-store"},
}

// problemBody is the transport-only error shape: a reason, not the refusal
// registry's error.code.
type problemBody struct { //nolint:govet // three-field transport body; grouping beats padding
	Status  int    `json:"status"`
	Reason  string `json:"reason"`
	Message string `json:"message"`
}

// middleware applies the security headers and the Host/Origin gates to every
// response, including the mux's own 404 and 405.
func (s *Server) middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		setSecurityHeaders(w)
		if s.rejected(w, r) {
			return
		}
		next.ServeHTTP(w, r)
	})
}

// setSecurityHeaders freezes the header set on every response.
func setSecurityHeaders(w http.ResponseWriter) {
	for _, header := range securityHeaders {
		w.Header().Set(header.name, header.value)
	}
}

// rejected writes the first request-gate failure, reporting whether it did.
func (s *Server) rejected(w http.ResponseWriter, r *http.Request) bool {
	if badPath(r.URL.Path) {
		s.problem(w, http.StatusNotFound, "not-found", "no such route")
		return true
	}
	if !s.hostAllowed(r.Host) {
		s.problem(w, http.StatusForbidden, "bad-host", "host "+r.Host+" is not the listen address")
		return true
	}
	if !s.originAllowed(r.Header.Get("Origin")) {
		s.problem(w, http.StatusForbidden, "bad-origin", "cross-origin requests are refused")
		return true
	}
	return false
}

// badPath refuses a traversal segment or a backslash before routing: the URL
// never becomes a filesystem path.
func badPath(p string) bool {
	if strings.Contains(p, `\`) {
		return true
	}
	for segment := range strings.SplitSeq(p, "/") {
		if segment == ".." {
			return true
		}
	}
	return false
}

// problem writes one transport error: status, reason, message.
func (s *Server) problem(w http.ResponseWriter, status int, reason, message string) {
	body, err := json.Marshal(problemBody{Status: status, Reason: reason, Message: message})
	if err != nil {
		http.Error(w, message, status)
		return
	}
	w.Header().Set("Content-Type", contentTypeJSON)
	w.WriteHeader(status)
	fmt.Fprint(w, string(body))
}
