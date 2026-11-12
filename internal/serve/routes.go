package serve

import (
	"fmt"
	"net/http"
	"net/url"

	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/core/query"
)

// The two verbs a route renders.
const (
	verbServe = "serve"
	verbShow  = "show"
)

// contentTypeHTML is the index body's type.
const contentTypeHTML = "text/html; charset=utf-8"

// route is one table row. The table registers GET only; ServeMux adds HEAD
// and answers every other method with 405 and an Allow header.
type route struct { //nolint:govet // method/pattern/handler reads as the table row
	Method  string
	Pattern string
	Handler http.Handler
}

// routes is the only registration point in the package: a handler outside
// this table cannot exist.
func (s *Server) routes() []route {
	return []route{
		{Method: http.MethodGet, Pattern: "/{$}", Handler: http.HandlerFunc(s.handleRoot)},
		{Method: http.MethodGet, Pattern: "/v1/health", Handler: s.guard(s.handleHealth)},
		{Method: http.MethodGet, Pattern: "/v1/roots", Handler: s.guard(s.handleRoots)},
		{Method: http.MethodGet, Pattern: "/v1/bundles", Handler: s.guard(s.handleBundles)},
		{Method: http.MethodGet, Pattern: "/v1/cost", Handler: s.guard(s.handleCost)},
		{Method: http.MethodGet, Pattern: "/v1/precision", Handler: s.guard(s.handlePrecision)},
		{
			Method:  http.MethodGet,
			Pattern: "/v1/roots/{root}/bundles/{bundle}",
			Handler: s.guard(s.handleDetail),
		},
		{
			Method:  http.MethodGet,
			Pattern: "/v1/roots/{root}/bundles/{bundle}/findings/{sig}",
			Handler: s.guard(s.handleShow),
		},
	}
}

func (s *Server) handleCost(w http.ResponseWriter, r *http.Request) {
	by, ok := s.aggregateDimension(w, r, "cost")
	if !ok {
		return
	}
	view, err := s.layer.Cost(r.Context(), by)
	if err != nil {
		s.writeViewError(w, verbServe, "", err)
		return
	}
	s.writeView(w, verbServe, view)
}

func (s *Server) handlePrecision(w http.ResponseWriter, r *http.Request) {
	by, ok := s.aggregateDimension(w, r, "precision")
	if !ok {
		return
	}
	view, err := s.layer.Precision(r.Context(), by)
	if err != nil {
		s.writeViewError(w, verbServe, "", err)
		return
	}
	s.writeView(w, verbServe, view)
}

func (s *Server) aggregateDimension(w http.ResponseWriter, r *http.Request, view string) (string, bool) {
	values, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil || len(values) != 1 || len(values["by"]) != 1 {
		s.writeRefusal(w, verbServe, "", "", &core.RefusalError{
			Code: "bad-by", Message: fmt.Sprintf("serve %s accepts one by query parameter", view),
		})
		return "", false
	}
	return values.Get("by"), true
}

// Handler builds the mux and wraps it in the security middleware.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	for _, item := range s.routes() {
		mux.Handle(item.Method+" "+item.Pattern, item.Handler)
	}
	return s.middleware(mux)
}

// guard refuses a URL token and then requires the cookie or bearer token.
func (s *Server) guard(next http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if tokenInQuery(r) {
			s.problem(w, http.StatusBadRequest, "token-in-query",
				"the token is accepted only at the one exchange; send the session cookie or a bearer header")
			return
		}
		if !s.authorized(r) {
			s.problem(w, http.StatusUnauthorized, "unauthorized",
				"a valid verdict_session_<port> cookie or Authorization: Bearer token is required")
			return
		}
		next(w, r)
	})
}

// handleRoot runs the token exchange at GET /?token=, or serves the
// token-free index page.
func (s *Server) handleRoot(w http.ResponseWriter, r *http.Request) {
	if tokenInQuery(r) {
		if !s.tokenEqual(queryToken(r)) {
			s.problem(w, http.StatusUnauthorized, "bad-token",
				"the start token is not valid for this process")
			return
		}
		s.setSessionCookie(w)
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	w.Header().Set("Content-Type", contentTypeHTML)
	w.WriteHeader(http.StatusOK)
	fmt.Fprint(w, indexHTML)
}

// handleHealth publishes the process posture.
func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	s.writeView(w, verbServe, s.layer.Health(r.Context()))
}

// handleRoots publishes the served roots.
func (s *Server) handleRoots(w http.ResponseWriter, r *http.Request) {
	s.writeView(w, verbServe, s.layer.Roots(r.Context()))
}

// handleBundles publishes the inbox, with the optional portable path glob.
func (s *Server) handleBundles(w http.ResponseWriter, r *http.Request) {
	pattern := r.URL.Query().Get("path")
	if pattern != "" && !query.ValidBundleGlob(pattern) {
		s.problem(w, http.StatusBadRequest, "bad-glob",
			"path must be a portable repository-relative glob (no .., no absolute path, no backslash)")
		return
	}
	view, err := s.layer.Bundles(r.Context(), pattern)
	if err != nil {
		s.writeViewError(w, verbServe, "", err)
		return
	}
	s.writeView(w, verbServe, view)
}

// handleDetail publishes one bundle's report, gate, threads, and ledger.
func (s *Server) handleDetail(w http.ResponseWriter, r *http.Request) {
	bundle := r.PathValue("bundle")
	view, err := s.layer.Detail(r.Context(), r.PathValue("root"), bundle)
	if err != nil {
		s.writeViewError(w, verbServe, bundle, err)
		return
	}
	s.writeView(w, verbServe, view)
}

// handleShow publishes one finding through the show service.
func (s *Server) handleShow(w http.ResponseWriter, r *http.Request) {
	bundle := r.PathValue("bundle")
	sig := r.PathValue("sig")
	if !query.ValidSig(sig) {
		s.problem(w, http.StatusNotFound, "not-found", "no such bundle or sig")
		return
	}
	view, err := s.layer.Show(r.Context(), r.PathValue("root"), bundle, sig)
	if err != nil {
		s.writeViewError(w, verbShow, bundle, err)
		return
	}
	s.writeView(w, verbShow, view)
}

// indexHTML is the token-free landing page: no data, no inline script or
// style, no external asset.
const indexHTML = `<!doctype html>
<html lang="en">
<head><meta charset="utf-8"><title>verdict serve</title></head>
<body>
<main id="main">
<h1>verdict serve</h1>
<p>This is the read-only loopback surface. Open the start URL the process
printed in your terminal; it exchanges the start token for this browser's
session cookie.</p>
<p><a href="/v1/health">/v1/health</a></p>
</main>
</body>
</html>
`
