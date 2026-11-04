package cli_test

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/cli"
	"github.com/stainless-code/verdict/internal/core"
)

// TestProgressRoutesLate pins the late binding the adapters depend on: a
// Progress value built before RouteHuman still writes to the stream the
// runner carries at call time, so JSON mode keeps stdout for one envelope.
func TestProgressRoutesLate(t *testing.T) {
	for _, tc := range []struct {
		name     string
		wantOut  string
		wantDiag string
		json     bool
	}{
		{name: "text mode", json: false, wantOut: "human line\n"},
		{name: "json mode", json: true, wantDiag: "human line\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			checkProgressRouting(t, tc.json, tc.wantOut, tc.wantDiag)
		})
	}
}

func checkProgressRouting(t *testing.T, json bool, wantOut, wantDiag string) {
	t.Helper()
	out, diag := &bytes.Buffer{}, &bytes.Buffer{}
	r := &cli.Runner{Out: out, Diag: diag, Human: out, JSONMode: json}
	p := cli.Progress{R: r}
	r.RouteHuman()
	p.Human("human line")
	if got := out.String(); got != wantOut {
		t.Errorf("stdout = %q; want %q", got, wantOut)
	}
	if got := diag.String(); got != wantDiag {
		t.Errorf("stderr = %q; want %q", got, wantDiag)
	}
}

type refusalCase struct {
	name     string
	ref      *core.RefusalError
	want     []string
	wantCode int
}

// TestRefuseTypedAdoptsWarnings pins the adapter-side refusal contract: the
// refusal's warnings replace the runner's when it carries them, a nil list
// keeps the accumulated ones, and a cancel exits 3 rather than 1.
func TestRefuseTypedAdoptsWarnings(t *testing.T) {
	for _, tc := range []refusalCase{
		{
			name:     "adopts the refusal warnings",
			ref:      &core.RefusalError{Code: "bad-bundle", Message: "boom", Warnings: []string{"from the service"}},
			want:     []string{"from the service"},
			wantCode: cli.ExitRefused,
		},
		{
			name:     "nil warnings keep the runner's",
			ref:      &core.RefusalError{Code: "bad-bundle", Message: "boom"},
			want:     []string{"from the adapter"},
			wantCode: cli.ExitRefused,
		},
		{
			name:     "a cancel exits 3",
			ref:      &core.RefusalError{Code: cli.CodeCanceled, Message: "canceled before record completed"},
			want:     []string{"from the adapter"},
			wantCode: cli.ExitBroke,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			checkRefuseTyped(t, tc)
		})
	}
}

func checkRefuseTyped(t *testing.T, tc refusalCase) {
	t.Helper()
	var out, diag bytes.Buffer
	r := &cli.Runner{Out: &out, Human: &out, Diag: &diag, Warnings: []string{"from the adapter"}}
	if code := r.RefuseTyped(tc.ref); code != tc.wantCode {
		t.Errorf("exit = %d; want %d", code, tc.wantCode)
	}
	if len(r.Warnings) != len(tc.want) || r.Warnings[0] != tc.want[0] {
		t.Errorf("warnings = %v; want %v", r.Warnings, tc.want)
	}
	if out.Len() != 0 {
		t.Errorf("stdout = %q; a refusal stays off stdout", out.String())
	}
	if diag.Len() == 0 {
		t.Error("no human refusal on stderr")
	}
}

// TestCanceled pins the shared cancel guard: a live context continues, a
// done one renders the frozen message and exits 3.
func TestCanceled(t *testing.T) {
	diag := &bytes.Buffer{}
	r := &cli.Runner{Out: &bytes.Buffer{}, Human: &bytes.Buffer{}, Diag: diag}
	if code, done := r.Canceled(t.Context(), "fetch"); done || code != 0 {
		t.Fatalf("live context = %d/%v; want 0/false", code, done)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	code, done := r.Canceled(ctx, "fetch")
	if !done || code != cli.ExitBroke {
		t.Fatalf("done context = %d/%v; want %d/true", code, done, cli.ExitBroke)
	}
	if !strings.HasPrefix(diag.String(), "error: canceled before fetch completed\n") {
		t.Errorf("stderr = %q; want the frozen cancel line", diag.String())
	}
}

type refuseRenderCase struct {
	render func(*cli.Runner, *core.RefusalError) int
	ref    *core.RefusalError
	name   string
	want   int
}

// TestRefuseMapsCancelToBroke pins GO-CLI-05 at the one renderer every
// adapter shares: a `canceled` refusal exits 3 however it is rendered and
// whichever code the caller picked, and a plain refusal keeps its code.
func TestRefuseMapsCancelToBroke(t *testing.T) {
	for _, tc := range []refuseRenderCase{
		{
			name:   "Refuse on a cancel exits 3",
			ref:    &core.RefusalError{Code: cli.CodeCanceled, Message: "canceled before ready completed"},
			render: func(r *cli.Runner, ref *core.RefusalError) int { return r.Refuse(ref) },
			want:   cli.ExitBroke,
		},
		{
			name:   "Refuse keeps 1 for a plain refusal",
			ref:    &core.RefusalError{Code: "bad-bundle", Message: "boom"},
			render: func(r *cli.Runner, ref *core.RefusalError) int { return r.Refuse(ref) },
			want:   cli.ExitRefused,
		},
		{
			name: "RefuseExit keeps the caller's code for a plain refusal",
			ref:  &core.RefusalError{Code: "bad-bundle", Message: "boom"},
			render: func(r *cli.Runner, ref *core.RefusalError) int {
				return r.RefuseExit(ref, cli.ExitUsage)
			},
			want: cli.ExitUsage,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			checkRefuseCode(t, tc)
		})
	}
}

func checkRefuseCode(t *testing.T, tc refuseRenderCase) {
	t.Helper()
	var out, diag bytes.Buffer
	r := &cli.Runner{Out: &out, Human: &out, Diag: &diag}
	if code := tc.render(r, tc.ref); code != tc.want {
		t.Errorf("exit = %d; want %d", code, tc.want)
	}
	if out.Len() != 0 {
		t.Errorf("stdout = %q; a refusal stays off stdout", out.String())
	}
}
