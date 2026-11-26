package postcore

import (
	"bytes"
	"context"
	"os"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/cli"
	"github.com/stainless-code/verdict/internal/config"
	"github.com/stainless-code/verdict/internal/core"
)

// forged is a stranger's value that tries to end verdict's `error:` line and
// start one of its own. forgedJSON is the same bytes as a JSON string body.
const (
	forged     = "x\nerror: forged"
	forgedJSON = `x\nerror: forged`
	forgedHead = "\nerror: forged"
)

// errorLines renders one refusal through the CLI writer and returns every
// stderr line that reads as verdict's own error.
func errorLines(t *testing.T, ref *core.RefusalError) []string {
	t.Helper()
	if ref == nil {
		t.Fatal("no refusal")
	}
	var out, diag bytes.Buffer
	r := cli.NewRunner("post", "acme/app", "7", &out, &diag, func(string) string { return "" })
	r.Refuse(ref)
	if strings.Contains(diag.String(), "\r") {
		t.Errorf("stderr carries a carriage return: %q", diag.String())
	}
	var lines []string
	for line := range strings.SplitSeq(diag.String(), "\n") {
		if strings.HasPrefix(line, "error:") {
			lines = append(lines, line)
		}
	}
	return lines
}

// assertOneErrorLine pins exactly one `error:` line that still names the
// value.
func assertOneErrorLine(t *testing.T, ref *core.RefusalError, named string) {
	t.Helper()
	lines := errorLines(t, ref)
	if len(lines) != 1 || !strings.Contains(lines[0], named) {
		t.Errorf("error lines = %q, want exactly one naming %q", lines, named)
	}
}

// TestSchemaVersionCannotForgeErrorLine drives a whole post: review.json's
// schema_version is the agent's text, and the refusal that names it must stay
// one line whatever line breaks it carries.
func TestSchemaVersionCannotForgeErrorLine(t *testing.T) {
	for _, tc := range []struct{ name, version, want string }{
		{
			name: "lf", version: `"2\nerror: forged"`,
			want: "error: review.json schema_version '2 error: forged' unknown (script knows 1)",
		},
		{
			name: "cr", version: `"2\rerror: forged"`,
			want: "error: review.json schema_version '2 error: forged' unknown (script knows 1)",
		},
		{
			name: "crlf", version: `"2\r\nerror: forged"`,
			want: "error: review.json schema_version '2  error: forged' unknown (script knows 1)",
		},
		{
			name: "array", version: `["error: forged"]`,
			want: `error: review.json schema_version '[   "error: forged" ]' unknown (script knows 1)`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Chdir(t.TempDir())
			// A findings-shaped document: the fixture writer rewrites the legacy
			// shape, and would replace the version under test.
			postBundle(t, `{"schema_version":`+tc.version+`,"summary":"A clean review.","findings":[]}`)
			var warnings, human []string
			req := postTestReq(postCommander(t),
				Flags{Approve: "false", ReviewFile: ".verdict/state/7/review.json", DryRun: true}, &warnings, &human)
			_, ref := Run(context.Background(), req)
			if lines := errorLines(t, ref); len(lines) != 1 || lines[0] != tc.want {
				t.Errorf("error lines = %q, want exactly %q", lines, tc.want)
			}
		})
	}
}

// forgedBundleState writes one PR record and the files the bundle check wants,
// and returns a state over them.
func forgedBundleState(t *testing.T, record string) *state {
	t.Helper()
	t.Chdir(t.TempDir())
	for name, body := range map[string]string{"PR.json": record, "full.diff": "", "review.json": "{}"} {
		if err := writeTestFile(".verdict/state/7/"+name, body); err != nil {
			t.Fatal(err)
		}
	}
	var warnings []string
	return postTestState(".verdict/state/7", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		Flags{Approve: "false", ReviewFile: ".verdict/state/7/review.json"}, &warnings)
}

func TestBundleValuesCannotForgeErrorLine(t *testing.T) {
	t.Run("provider", func(t *testing.T) {
		st := forgedBundleState(t, `{"provider":"`+forgedJSON+`"}`)
		assertOneErrorLine(t, st.checkBundle(), "post to x error: forged is not implemented")
	})
	t.Run("pr state", func(t *testing.T) {
		st := forgedBundleState(t, `{"state":"`+forgedJSON+`"}`)
		assertOneErrorLine(t, st.checkBundle(), "PR is x error: forged")
	})
	t.Run("post receipt", func(t *testing.T) {
		st := forgedBundleState(t, `{}`)
		st.commitID = forgedHead
		if err := os.WriteFile(st.dir+"/post.json",
			[]byte(`{"head":"\nerror: forged","posted_at":"`+forgedJSON+`"}`), 0o644); err != nil {
			t.Fatal(err)
		}
		ref, _ := st.sameSHARefusal()
		assertOneErrorLine(t, ref, "refusing second post on reviewed SHA  error:")
		assertOneErrorLine(t, ref, "already posted at x error: forged")
	})
}

func TestHostValuesCannotForgeErrorLine(t *testing.T) {
	t.Run("live review", func(t *testing.T) {
		var warnings []string
		st := postTestState(".", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Flags{}, &warnings)
		st.liveLoaded = true
		st.live = []liveReview{{
			body:     "<!-- verdict-review payload=" + strings.Repeat("c", 40) + " head=" + st.commitID + " -->",
			commitID: st.commitID, submittedAt: forged,
		}}
		assertOneErrorLine(t, st.liveAlreadyPosted(), "already posted at x error: forged")
	})
	t.Run("follow-up review id", func(t *testing.T) {
		var warnings []string
		st := postTestState(".", "", Flags{}, &warnings)
		assertOneErrorLine(t, st.followRefusal(2, 3, `{"id":"`+forgedJSON+`"}`, ""),
			"review x error: forged landed part 1")
	})
	t.Run("forgejo state", func(t *testing.T) {
		cmdr := forgejoCommander(t, forgejoAnswer("200 OK", forgejoReviewBody(forgedJSON)))
		attempt := forgejoPost(t, cmdr, forgejoPostDir(t), forgejoPayload(eventComment, ""))
		assertOneErrorLine(t, &core.RefusalError{Code: codePostFailed, Message: attempt.message},
			"answered state x error: forged")
	})
	t.Run("bitbucket head", func(t *testing.T) {
		cmdr := bitbucketCommander(t, bitbucketAnswer("201 Created", bitbucketCreated(501)))
		cmdr.out[bitbucketPRRoute] = scriptResult{stdout: bitbucketPRJSON(`\nerror: forged`)}
		attempt := bitbucketPost(t, cmdr, bitbucketPostDir(t), bitbucketPayload(eventApprove, ""))
		assertOneErrorLine(t, &core.RefusalError{Code: codePostFailed, Message: attempt.message},
			"live HEAD is  error:)")
	})
	t.Run("azure head", func(t *testing.T) {
		cmdr := azureCommander(t)
		cmdr.out[azurePullRoute] = scriptResult{stdout: azurePRJSON(`\nerror: forged`)}
		attempt := azurePost(t, cmdr, azurePostDir(t),
			azurePayload(eventComment, `{"path":"notes.txt","body":"x","line":2,"side":"RIGHT"}`))
		assertOneErrorLine(t, &core.RefusalError{Code: codePostFailed, Message: attempt.message},
			"live HEAD is  error:)")
	})
}

// failingCommander answers every call with one nonzero exit and no stderr.
type failingCommander struct{ err error }

func (c failingCommander) Run(context.Context, *core.Command) (core.Result, error) {
	return core.Result{ExitCode: 1}, c.err
}

func TestThreadValuesCannotForgeErrorLine(t *testing.T) {
	ctx := context.Background()
	refused := failingCommander{}
	broken := failingCommander{err: errPatchGuardNoGit}
	t.Run("unimplemented provider", func(t *testing.T) {
		_, ref := ReplyThread(ctx, refused, forged, nil, "acme/app", "7", "1", "t", "body")
		assertOneErrorLine(t, ref, "thread replies on x error: forged")
		assertOneErrorLine(t, ResolveThread(ctx, refused, forged, nil, "acme/app", "7", "t"),
			"thread resolution on x error: forged")
	})
	for _, provider := range []string{providerGitHub, providerGitLab} {
		t.Run(provider+" refused", func(t *testing.T) {
			_, ref := ReplyThread(ctx, refused, provider, nil, "acme/app", "7", forged, forged, "body")
			assertOneErrorLine(t, ref, "x error: forged:")
			assertOneErrorLine(t, ResolveThread(ctx, refused, provider, nil, "acme/app", "7", forged),
				"x error: forged:")
		})
		t.Run(provider+" unconfirmed", func(t *testing.T) {
			silent := &silentCommander{}
			assertOneErrorLine(t, ResolveThread(ctx, silent, provider, nil, "acme/app", "7", forged),
				"x error: forged resolved")
		})
		t.Run(provider+" unreachable", func(t *testing.T) {
			_, ref := ReplyThread(ctx, broken, provider, nil, "acme/app", "7", "1", forged, "body")
			assertOneErrorLine(t, ref, "reply to thread x error: forged")
			assertOneErrorLine(t, ResolveThread(ctx, broken, provider, nil, "acme/app", "7", forged),
				"resolve thread x error: forged")
		})
	}
}

// silentCommander succeeds with an empty document, which confirms nothing.
type silentCommander struct{}

func (*silentCommander) Run(context.Context, *core.Command) (core.Result, error) {
	return core.Result{Stdout: "{}"}, nil
}

func TestPolicyValuesCannotForgeErrorLine(t *testing.T) {
	t.Run("blocked finding path", func(t *testing.T) {
		rs := policyRunState()
		rs.Resolved.Files[0].Path = "pay/" + forged
		st := policyTestState([]any{policyEntry("pay/"+forged, "security", "critical", "open")}, rs)
		st.flags.Approve, st.flags.Bind = approveTrue, true
		assertOneErrorLine(t, st.complianceGate(), "pay/x error: forged [security/critical]")
	})
	t.Run("withheld path", func(t *testing.T) {
		rs := privacyRunState(t)
		rs.Resolved.Files[0].Path = "secret/" + forged
		st := policyTestState(nil, rs)
		st.flags.Approve, st.flags.Bind = approveTrue, true
		assertOneErrorLine(t, st.privacyConflictGate(), "required review for secret/x error: forged")
	})
	t.Run("approval reason", func(t *testing.T) {
		st := policyTestState(nil, nil)
		st.merged.Approval = &core.ApprovalReceipt{
			Insufficient: true, Reason: core.CodeReviewInsufficient + ": finding " + forged,
		}
		assertOneErrorLine(t, st.evidenceGate(), "finding x error: forged")
	})
	t.Run("recorded check detail", func(t *testing.T) {
		rs := policyRunState()
		rs.Resolved.Plan.Groups[0].Checks = []string{config.CheckHeadCurrent}
		st := policyTestState(nil, rs)
		st.commitID = forgedHead
		rs.Outcomes = []config.CheckObservation{{
			Check: config.CheckHeadCurrent, Group: "payments-group", Head: forgedHead, Detail: forged,
		}}
		ref := st.headGate()
		assertOneErrorLine(t, ref, "(head  error:): x error: forged")
	})
}
