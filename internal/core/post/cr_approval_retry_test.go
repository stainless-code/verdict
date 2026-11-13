package postcore

import (
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/core"
)

type approvalRetryHost struct {
	summary          map[string]any
	base             failingDiscussionHost
	approvals, notes int
}

//nolint:revive // one fake exchange preserves the summary between the failed and successful approval
func (host *approvalRetryHost) Run(ctx context.Context, cmd *core.Command) (core.Result, error) {
	switch {
	case cmd.Argv[2] == "user":
		return core.Result{Stdout: `{"username":"reviewer"}`}, nil
	case strings.HasSuffix(cmd.Argv[2], "/discussions?per_page=100"):
		notes := []any{}
		if host.summary != nil {
			notes = append(notes, host.summary)
		}
		raw, err := json.Marshal([]any{map[string]any{"notes": notes}})
		if err != nil {
			return core.Result{}, err
		}
		return core.Result{Stdout: string(raw)}, nil
	case strings.HasSuffix(cmd.Argv[2], "/notes"):
		raw, err := io.ReadAll(cmd.Stdin)
		if err != nil {
			return core.Result{}, err
		}
		if err := json.Unmarshal(raw, &host.summary); err != nil {
			return core.Result{}, err
		}
		host.notes++
		return core.Result{Stdout: `{"id":99,"created_at":"2026-01-01T00:00:00Z"}`}, nil
	case strings.HasSuffix(cmd.Argv[2], "/approve"):
		host.approvals++
		if host.approvals == 1 {
			return core.Result{ExitCode: 1, Stderr: "approval failed"}, nil
		}
		return core.Result{Stdout: `{}`}, nil
	default:
		return host.base.Run(ctx, cmd)
	}
}

func TestCRApprovalRetriesAfterSummary(t *testing.T) {
	t.Chdir(t.TempDir())
	writeGitLabBundle(t, "gitlab", `{"schema_version":"1","body":"> ✅ Everything is ready to ship.","approved":true,"comments":[]}`)
	host := &approvalRetryHost{}
	var warnings, human []string
	req := postTestReq(host, core.PostFlags{Approve: "true", Bind: true, ReviewFile: ".verdict/state/7/review.json"}, &warnings, &human)
	if _, ref := Run(t.Context(), req); ref == nil || ref.Code != codePostFailed {
		t.Fatalf("first approval = %v", ref)
	}
	response, ref := Run(t.Context(), req)
	if ref != nil {
		t.Fatalf("approval retry swallowed: %v", ref)
	}
	if host.approvals != 2 || host.notes != 1 || response.Data.Review["state"] != "approved" {
		t.Fatalf("approval attempts %d, summary writes %d, state %v", host.approvals, host.notes, response.Data.Review["state"])
	}
}
