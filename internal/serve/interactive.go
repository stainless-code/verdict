package serve

import (
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"

	"github.com/stainless-code/verdict/internal/core"
	learncore "github.com/stainless-code/verdict/internal/core/learn"
	"github.com/stainless-code/verdict/internal/core/query"
	triagecore "github.com/stainless-code/verdict/internal/core/triage"
)

// Interactive write endpoints: local-only triage and KB promotion over the
// verb services the CLI calls, gated by the interactive flag, a per-session
// CSRF token, and the existing Host/Origin allowlists. Registered only when
// the server runs --interactive; without it the mux answers 405 and nothing
// is written.
const (
	csrfHeader  = "X-Verdict-CSRF"
	csrfBodyCap = 64 * 1024
	// kbDefaultRigor mirrors the learn CLI's --rigor default.
	kbDefaultRigor = "pointed"
)

// triageBody is the POST /v1/triage wire shape: the same status vocabulary
// `verdict triage --status` accepts, plus the rationale or force the CLI
// takes. Root and bundle must name a served root and one of its bundles.
type triageBody struct {
	Root      string `json:"root"`
	Bundle    string `json:"bundle"`
	Sig       string `json:"sig"`
	Status    string `json:"status"`
	Rationale string `json:"rationale,omitempty"`
	Force     bool   `json:"force,omitempty"`
}

// kbPromoteBody is the POST /v1/kb/promote wire shape: one learn append.
// Rigor defaults to pointed, the CLI default; provenance and id fall back
// to the service defaults when empty.
type kbPromoteBody struct {
	Root       string `json:"root"`
	Rule       string `json:"rule"`
	Scope      string `json:"scope,omitempty"`
	Rigor      string `json:"rigor,omitempty"`
	Provenance string `json:"provenance,omitempty"`
	ID         string `json:"id,omitempty"`
}

// guardWrite wraps one interactive write handler: the session cookie or
// bearer token (guard), then the per-session CSRF token, a present
// allowlisted Origin, and a strict JSON content type. Every refusal is a
// transport problem and happens before any store is read or written.
func (s *Server) guardWrite(next http.HandlerFunc) http.Handler {
	return s.guard(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.csrfAllowed(r) {
			s.problem(w, http.StatusForbidden, "bad-csrf",
				"writes require the per-session X-Verdict-CSRF token")
			return
		}
		if origin := r.Header.Get("Origin"); origin == "" || !s.originAllowed(origin) {
			s.problem(w, http.StatusForbidden, "bad-origin",
				"cross-origin requests are refused")
			return
		}
		if !hasJSONContentType(r) {
			s.problem(w, http.StatusBadRequest, "bad-content-type",
				"writes accept application/json only")
			return
		}
		next(w, r)
	}))
}

// csrfAllowed reports whether the request carries this session's CSRF token.
func (s *Server) csrfAllowed(r *http.Request) bool {
	candidate := r.Header.Get(csrfHeader)
	return candidate != "" && s.csrf != "" && equalSecret(candidate, s.csrf)
}

// hasJSONContentType requires the application/json media type; a charset or
// other parameter is fine.
func hasJSONContentType(r *http.Request) bool {
	media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	return err == nil && media == "application/json"
}

// decodeJSON reads one bounded JSON object body with no unknown fields and
// no trailing value; a malformed body refuses before any store is touched.
func decodeJSON[T any](w http.ResponseWriter, r *http.Request, s *Server, out *T) bool {
	r.Body = http.MaxBytesReader(w, r.Body, csrfBodyCap)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(out); err != nil {
		s.problem(w, http.StatusBadRequest, "bad-body",
			"writes accept one bounded JSON object")
		return false
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		s.problem(w, http.StatusBadRequest, "bad-body",
			"writes accept one bounded JSON object")
		return false
	}
	return true
}

// handleTriage flips one finding's status through the triage service the
// CLI calls, confined to a served root and bundle.
func (s *Server) handleTriage(w http.ResponseWriter, r *http.Request) {
	var body triageBody
	if !decodeJSON(w, r, s, &body) {
		return
	}
	rootPath, ok := s.resolveWrite(w, body.Root, body.Bundle)
	if !ok {
		return
	}
	resp, ref := triagecore.Run(r.Context(), &triagecore.Request{
		Runtime:   s.writeRuntime(),
		Pr:        body.Bundle,
		RepoRoot:  rootPath,
		Sig:       body.Sig,
		Status:    body.Status,
		Rationale: body.Rationale,
		Force:     body.Force,
	})
	if ref != nil {
		s.writeRefusal(w, verbServe, body.Bundle, ref)
		return
	}
	s.layer.Invalidate()
	s.writeEnvelope(w, core.RenderEnvelope(verbServe, "", body.Bundle, resp.At, resp.Next,
		resp.Warnings, resp.Data.Wire(), nil))
}

// handleKBPromote appends one rule to the knowledge base through the learn
// service's append mode, in the root the request names.
func (s *Server) handleKBPromote(w http.ResponseWriter, r *http.Request) {
	var body kbPromoteBody
	if !decodeJSON(w, r, s, &body) {
		return
	}
	rootPath, ok := s.resolveRootOnly(w, body.Root)
	if !ok {
		return
	}
	if body.Rigor == "" {
		body.Rigor = kbDefaultRigor
	}
	resp, ref := learncore.Run(r.Context(), &learncore.Request{
		Runtime:    s.writeRuntime(),
		RepoRoot:   rootPath,
		Rule:       body.Rule,
		Scope:      body.Scope,
		Rigor:      body.Rigor,
		Provenance: body.Provenance,
		ID:         body.ID,
	})
	if ref != nil {
		s.writeRefusal(w, verbServe, "", ref)
		return
	}
	s.layer.Invalidate()
	s.writeEnvelope(w, core.RenderEnvelope(verbServe, "", "", resp.At, resp.Next,
		resp.Warnings, resp.Data.Wire(), nil))
}

// writeRuntime is the clock the interactive calls stamp with. Progress
// stays nil: the transport returns warnings in the envelope, not on a
// stream.
func (s *Server) writeRuntime() *core.Runtime {
	return &core.Runtime{Now: s.now}
}

// resolveWrite confines a triage write to a served root and bundle, with
// the same answer a read gives for a name outside the served set. The
// problem is already written when ok is false.
func (s *Server) resolveWrite(w http.ResponseWriter, rootID, bundle string) (string, bool) {
	rootPath, _, err := s.layer.ResolveBundle(rootID, bundle)
	if errors.Is(err, query.ErrNotFound) {
		s.problem(w, http.StatusNotFound, "not-found", "no such root or bundle")
		return "", false
	}
	if ref, ok := errors.AsType[*core.RefusalError](err); ok {
		s.writeRefusal(w, verbServe, bundle, ref)
		return "", false
	}
	return rootPath, true
}

// resolveRootOnly confines a KB promotion to a served root; the store it
// writes is the root's own, so no bundle is involved. The problem is
// already written when ok is false.
func (s *Server) resolveRootOnly(w http.ResponseWriter, rootID string) (string, bool) {
	rootPath, ok := s.layer.RootByID(rootID)
	if !ok {
		s.problem(w, http.StatusNotFound, "not-found", "no such root")
		return "", false
	}
	return rootPath, true
}
