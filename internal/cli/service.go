package cli

import (
	"fmt"
	"maps"

	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/jq"
)

// Emit writes the verb's JSON envelope when machine output is enabled.
func (r *Runner) Emit(verb, repo, pr, at, next string, warnings []string, data any) {
	if r.JSONMode {
		env := core.RenderEnvelope(verb, repo, pr, at, next, warnings, data, nil)
		fmt.Fprint(r.Out, jq.PrettyOrdered(env))
	}
}

// Read injects the runner's runtime dependencies into an independent request.
func (r *Runner) Read(req core.ReadRequest, cmdr core.Commander) core.ReadRequest {
	rt := &core.Runtime{}
	if req.Runtime != nil {
		*rt = *req.Runtime
		rt.Env = maps.Clone(req.Env)
	}
	rt.Now = r.Now
	rt.Progress = Progress{R: r}
	rt.Commander = cmdr
	req.Runtime = rt
	return req
}
