package exec_test

import (
	"errors"
	"testing"

	"github.com/stainless-code/verdict/internal/core"
	coreexec "github.com/stainless-code/verdict/internal/core/exec"
)

// TestPinnedGlabWriteRefusesAnotherAPIHost proves the runner checks the
// environment glab will inherit: a pinned review POST whose GITLAB_API_HOST
// names another server refuses host-mismatch naming both, before any spawn,
// because glab would send it (with the pinned host's token) to that server.
func TestPinnedGlabWriteRefusesAnotherAPIHost(t *testing.T) {
	t.Parallel()
	post := []string{
		"glab", "api", "projects/acme%2Fapp/merge_requests/7/notes",
		"--method", "POST", "--header", "Content-Type: application/json", "--input", "-",
	}
	ctx := core.WithHost(t.Context(), "gitlab.com")
	for _, tc := range []struct {
		name    string
		env     []string
		refuses bool
	}{
		{"another API host", []string{"GITLAB_API_HOST=gitlab.corp.example"}, true},
		{"CI auto-login server elsewhere", []string{
			"GLAB_ENABLE_CI_AUTOLOGIN=true", "GITLAB_CI=true", "CI_SERVER_FQDN=gitlab.corp.example",
		}, true},
		{"API host is the target", []string{"GITLAB_API_HOST=https://gitlab.com"}, false},
		{"no API host", nil, false},
	} {
		_, err := coreexec.Runner{}.Run(ctx, &core.Command{Argv: post, Env: append([]string{"PATH="}, tc.env...)})
		var ref *core.RefusalError
		refused := errors.As(err, &ref) && ref.Code == core.CodeHostMismatch
		if refused != tc.refuses {
			t.Errorf("%s: err = %v, want refusal %v", tc.name, err, tc.refuses)
		}
	}
}
