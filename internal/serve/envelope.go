package serve

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/core/query"
	"github.com/stainless-code/verdict/internal/jq"
)

// at is the envelope clock.
func (s *Server) at() string { return core.NowUTC(s.now()) }

// writeView renders one composed view as its verb's envelope.
func (s *Server) writeView(w http.ResponseWriter, verb string, view *query.View) {
	env := core.RenderEnvelope(verb, view.Repo, view.Pr, view.At, view.Next,
		view.Warnings, view.Data, nil)
	s.writeEnvelope(w, env)
}

// writeEnvelope writes one 200 envelope body.
func (s *Server) writeEnvelope(w http.ResponseWriter, env *jq.Obj) {
	w.Header().Set("Content-Type", contentTypeJSON)
	w.WriteHeader(http.StatusOK)
	//nolint:gosec // G705: the body is the JSON envelope under an application/json content type, never HTML.
	fmt.Fprint(w, jq.PrettyOrdered(env))
}

// writeRefusal writes a product refusal as a 200 envelope: data null, the
// typed error object, and the hint the CLI's own refusal carries.
func (s *Server) writeRefusal(w http.ResponseWriter, verb, repo, pr string, ref *core.RefusalError) {
	message := ref.Message + "\n" + core.HintFor(ref.Code)
	errObj := jq.NewObj("code", ref.Code, "message", message)
	env := core.RenderEnvelope(verb, repo, pr, s.at(), "", nil, nil, errObj)
	s.writeEnvelope(w, env)
}

// writeViewError maps a layer error: a typed refusal stays a 200 envelope, a
// missing root or bundle is 404, and anything else is a 500 problem.
func (s *Server) writeViewError(w http.ResponseWriter, verb, pr string, err error) {
	if ref, ok := errors.AsType[*core.RefusalError](err); ok {
		s.writeRefusal(w, verb, "", pr, ref)
		return
	}
	if errors.Is(err, query.ErrNotFound) {
		s.problem(w, http.StatusNotFound, "not-found", "no such root or bundle")
		return
	}
	s.problem(w, http.StatusInternalServerError, "internal", err.Error())
}
