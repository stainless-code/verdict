package serve

import (
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

// route is one table row. The table registers GET only under --read-only; a
// writable server adds POST rows. ServeMux adds HEAD and answers
// every other method with 405 and an Allow header.
type route struct { //nolint:govet // method/pattern/handler reads as the table row
	Method  string
	Pattern string
	Handler http.Handler
}

// routes is the only registration point in the package: a handler outside
// this table cannot exist.
func (s *Server) routes() []route {
	routes := []route{
		{Method: http.MethodGet, Pattern: "/{$}", Handler: http.HandlerFunc(s.handleRoot)},
		{Method: http.MethodGet, Pattern: "/app.js", Handler: s.assetHandler("app.js")},
		{Method: http.MethodGet, Pattern: "/app.css", Handler: s.assetHandler("app.css")},
		{Method: http.MethodGet, Pattern: "/assets/{name}", Handler: http.HandlerFunc(s.handleAsset)},
		{
			Method: http.MethodGet, Pattern: "/v1/roots/{root}/bundles/{bundle}/findings/{sig}/reproduction",
			Handler: s.guard(s.handleReproduction),
		},
		{
			Method: http.MethodGet, Pattern: "/v1/roots/{root}/bundles/{bundle}/compare",
			Handler: s.guard(s.handleCompare),
		},
		{
			Method: http.MethodGet, Pattern: "/v1/roots/{root}/bundles/{bundle}/findings/{sig}/copy",
			Handler: s.guard(s.handleCopy),
		},
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
			Pattern: "/v1/roots/{root}/bundles/{bundle}/diff",
			Handler: s.guard(s.handleDiff),
		},
		{
			Method:  http.MethodGet,
			Pattern: "/v1/roots/{root}/bundles/{bundle}/findings/{sig}",
			Handler: s.guard(s.handleShow),
		},
	}
	if !s.cfg.NoWatch {
		routes = append(routes, route{
			Method: http.MethodGet, Pattern: "/v1/events", Handler: s.guard(s.handleEvents),
		})
	}
	// The write and job paths exist as GET placeholders so the mux
	// answers a non-GET method with 405 and an Allow header instead of
	// a port-scan 404: under --read-only the writes are refused,
	// never silently absent. A writable server registers POST only, so the
	// mux answers every other method with 405 and Allow: POST. The
	// job status read is GET on a writable server under the session
	// guard; under --read-only it too fails closed.
	if !s.rule.LocalWrites() {
		routes = append(routes,
			route{Method: http.MethodGet, Pattern: "/v1/triage", Handler: http.HandlerFunc(s.handleWriteGone)},
			route{Method: http.MethodGet, Pattern: "/v1/kb/promote", Handler: http.HandlerFunc(s.handleWriteGone)},
			route{Method: http.MethodGet, Pattern: postSubmitPath, Handler: http.HandlerFunc(s.handleWriteGone)},
			route{Method: http.MethodGet, Pattern: jobPathVerify, Handler: http.HandlerFunc(s.handleWriteGone)},
			route{Method: http.MethodGet, Pattern: jobPathPostPreview, Handler: http.HandlerFunc(s.handleWriteGone)},
			route{Method: http.MethodGet, Pattern: "/v1/jobs/{id}/cancel", Handler: http.HandlerFunc(s.handleWriteGone)},
			route{Method: http.MethodGet, Pattern: "/v1/jobs/{id}", Handler: http.HandlerFunc(s.handleWriteGone)},
		)
	}
	if s.rule.LocalWrites() {
		routes = append(routes,
			route{Method: http.MethodPost, Pattern: "/v1/triage", Handler: s.guardWrite(s.handleTriage)},
			route{Method: http.MethodPost, Pattern: "/v1/kb/promote", Handler: s.guardWrite(s.handleKBPromote)},
			route{Method: http.MethodPost, Pattern: postSubmitPath, Handler: s.guardWrite(s.handlePostSubmit)},
			route{Method: http.MethodPost, Pattern: jobPathVerify, Handler: s.guardWrite(s.handleVerifyJob)},
			route{Method: http.MethodPost, Pattern: jobPathPostPreview, Handler: s.guardWrite(s.handlePostPreviewJob)},
			route{Method: http.MethodPost, Pattern: "/v1/jobs/{id}/cancel", Handler: s.guardWrite(s.handleCancelJob)},
			route{Method: http.MethodGet, Pattern: "/v1/jobs/{id}", Handler: s.guard(s.handleGetJob)},
		)
	}
	return routes
}

// handleWriteGone is the placeholder behind the write paths on a
// --read-only server: the writes stay refused with the default 405 and an
// Allow header, and a GET there gets the same refusal. The copy names the
// flag to drop; it never promises that dropping it enables host posting,
// which also needs --allow-post.
func (s *Server) handleWriteGone(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Allow", "GET, HEAD")
	s.problem(w, http.StatusMethodNotAllowed, "method-not-allowed",
		"this server is read-only (--read-only); restart without it for local writes "+
			"(host posting also needs --allow-post)")
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
		s.writeRefusal(w, verbServe, "", &core.RefusalError{
			Code: "bad-by", Message: core.Msgf("serve %s accepts one by query parameter", view),
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
	return s.middleware(s.jobPostOnly(mux))
}

// jobPostOnly keeps the two POST job literals POST-only despite the GET
// job-status wildcard that shares their prefix: without it
// GET /v1/jobs/verify would read as a status lookup for id "verify"
// and PUT would advertise GET beside POST in Allow.
func (s *Server) jobPostOnly(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.rule.LocalWrites() && r.Method != http.MethodPost &&
			(r.URL.Path == jobPathVerify || r.URL.Path == jobPathPostPreview) {
			w.Header().Set("Allow", http.MethodPost)
			s.problem(w, http.StatusMethodNotAllowed, "method-not-allowed",
				"this action accepts POST only")
			return
		}
		next.ServeHTTP(w, r)
	})
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
	s.serveIndex(w)
}

// handleHealth publishes the process posture. On a writable server it also
// advertises the mode, the per-session CSRF token the UI writes with,
// and the Tier B job actions the running server can start. The token
// never rides a URL or the static data.json export, which reads no
// health view.
func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	view := s.layer.Health(r.Context())
	view.Data.Set("read_only", !s.rule.LocalWrites())
	if s.rule.LocalWrites() {
		view.Data.Set("interactive", true)
		view.Data.Set("allow_post", s.rule.HostPost())
		view.Data.Set("csrf", s.csrf)
		view.Data.Set("jobs", true)
		view.Data.Set("actions", []any{jobKindVerify, jobKindPostPreview})
	}
	s.writeView(w, verbServe, view)
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

// handleCompare serves one bundle's comparison against a baseline bundle: the
// report service's data under the compare flags, at
// GET /v1/roots/{root}/bundles/{bundle}/compare?baseline=<bundle>. Both
// bundles are resolved inside the route's own served root.
func (s *Server) handleCompare(w http.ResponseWriter, r *http.Request) {
	baseline, ok := s.baselineParam(w, r)
	if !ok {
		return
	}
	bundle := r.PathValue(bundlePathKey)
	view, err := s.layer.Compare(r.Context(), r.PathValue(rootPathKey), bundle, baseline)
	if err != nil {
		s.writeViewError(w, verbServe, bundle, err)
		return
	}
	s.writeView(w, verbServe, view)
}

// baselineParam reads the compare endpoint's one query parameter. The route
// accepts exactly one, `baseline`, and nothing beside it: an unrelated second
// parameter refuses like a second baseline, so one comparison has one
// spelling. The query layer resolves the value and confines it to a bundle in
// the same served root; this function checks the parameter's shape only.
func (s *Server) baselineParam(
	w http.ResponseWriter, r *http.Request,
) (string, bool) {
	values, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil || len(values) != 1 || len(values["baseline"]) != 1 {
		s.writeRefusal(w, verbServe, "", &core.RefusalError{
			Code:    "bad-baseline",
			Message: "serve compare accepts exactly one baseline query parameter naming a bundle in the same root",
		})
		return "", false
	}
	return values.Get("baseline"), true
}

// handleCopy publishes one finding's copy payloads: the deterministic
// `Prompt for AI agent` block and the ready-to-run `verdict triage` command.
// A finding the store does not hold, and a policy-withheld one, have no copy.
func (s *Server) handleCopy(w http.ResponseWriter, r *http.Request) {
	bundle := r.PathValue(bundlePathKey)
	sig := r.PathValue("sig")
	if !query.ValidSig(sig) {
		s.problem(w, http.StatusNotFound, "not-found", "no such bundle or sig")
		return
	}
	view, err := s.layer.Copy(r.Context(), r.PathValue(rootPathKey), bundle, sig)
	if err != nil {
		s.writeViewError(w, verbServe, bundle, err)
		return
	}
	s.writeView(w, verbServe, view)
}

// handleDetail publishes one bundle's report, gate, threads, and ledger.
func (s *Server) handleDetail(w http.ResponseWriter, r *http.Request) {
	bundle := r.PathValue(bundlePathKey)
	view, err := s.layer.Detail(r.Context(), r.PathValue(rootPathKey), bundle)
	if err != nil {
		s.writeViewError(w, verbServe, bundle, err)
		return
	}
	s.writeView(w, verbServe, view)
}

// handleDiff publishes one bundle's review diff: parsed hunks with
// word-level spans, collapsed gaps, anchored findings, and lanes grouped
// in risk order. Read-only GET, available under --read-only.
func (s *Server) handleDiff(w http.ResponseWriter, r *http.Request) {
	bundle := r.PathValue(bundlePathKey)
	view, err := s.layer.Diff(r.Context(), r.PathValue(rootPathKey), bundle)
	if err != nil {
		s.writeViewError(w, verbServe, bundle, err)
		return
	}
	s.writeView(w, verbServe, view)
}

// handleShow publishes one finding through the show service.
func (s *Server) handleShow(w http.ResponseWriter, r *http.Request) {
	bundle := r.PathValue(bundlePathKey)
	sig := r.PathValue("sig")
	if !query.ValidSig(sig) {
		s.problem(w, http.StatusNotFound, "not-found", "no such bundle or sig")
		return
	}
	view, err := s.layer.Show(r.Context(), r.PathValue(rootPathKey), bundle, sig)
	if err != nil {
		s.writeViewError(w, verbShow, bundle, err)
		return
	}
	s.writeView(w, verbShow, view)
}

func (s *Server) handleReproduction(w http.ResponseWriter, r *http.Request) {
	raw, err := s.layer.Reproduction(r.Context(), r.PathValue(rootPathKey), r.PathValue(bundlePathKey), r.PathValue("sig"))
	if err != nil {
		s.writeViewError(w, verbShow, r.PathValue(bundlePathKey), err)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="reproduction.txt"`)
	if _, err := w.Write(raw); err != nil {
		return
	}
}

const (
	rootPathKey   = "root"
	bundlePathKey = "bundle"
)
