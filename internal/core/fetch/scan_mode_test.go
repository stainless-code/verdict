package fetchcore

import (
	"context"
	"strings"
	"testing"
)

// TestResolveScanModeRequiresAnHonestUnbornHead proves a scan skips the trusted
// base only when git positively confirms HEAD is a symbolic branch with no
// commit: a transient read, a detached HEAD, and an operational failure no
// unborn marker backs all refuse instead.
func TestResolveScanModeRequiresAnHonestUnbornHead(t *testing.T) {
	const head = "cccccccccccccccccccccccccccccccccccccccc"
	revParse := "git\x00rev-parse\x00--verify\x00HEAD"
	symbolic := "git\x00symbolic-ref\x00-q\x00HEAD"
	branch := "git\x00rev-parse\x00--verify\x00-q\x00refs/heads/main"
	birth := "git\x00status\x00--porcelain=v2\x00--branch"
	cases := []struct {
		name string
		out  map[string]scriptResult
		base string
		want string
	}{
		{
			name: "committed head",
			out:  map[string]scriptResult{revParse: {stdout: head + "\n"}},
			base: head,
		},
		{
			name: "unborn branch",
			out: map[string]scriptResult{
				revParse: {fail: true, stderr: "fatal: Needed a single revision\n"},
				symbolic: {stdout: "refs/heads/main\n"},
				branch:   {fail: true},
				birth:    {stdout: "# branch.oid (initial)\n# branch.head main\n"},
			},
		},
		{
			name: "operational failure without the unborn marker",
			out: map[string]scriptResult{
				revParse: {fail: true, stderr: "fatal: unable to read tree\n"},
				symbolic: {stdout: "refs/heads/main\n"},
				branch:   {fail: true},
				birth:    {stdout: "# branch.oid " + head + "\n# branch.head main\n"},
			},
			want: "could not confirm HEAD is unborn",
		},
		{
			name: "status read failed",
			out: map[string]scriptResult{
				revParse: {fail: true},
				symbolic: {stdout: "refs/heads/main\n"},
				branch:   {fail: true},
				birth:    {fail: true},
			},
			want: "could not confirm HEAD is unborn",
		},
		{
			name: "transient first read",
			out: map[string]scriptResult{
				revParse: {fail: true},
				symbolic: {stdout: "refs/heads/main\n"},
				branch:   {stdout: head + "\n"},
			},
			want: "second read",
		},
		{
			name: "detached head",
			out: map[string]scriptResult{
				revParse: {fail: true},
				symbolic: {fail: true},
			},
			want: "could not resolve HEAD",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cmdr := &scriptCommander{t: t, out: c.out}
			s := &session{commander: cmdr, warnings: new([]string)}
			mode, err := resolveScanMode(context.Background(), s)
			assertScanMode(t, c.want, c.base, &mode, err)
		})
	}
}

// assertScanMode pins one mode resolution: a refusal carrying want, or the
// scan mode with base and the dirty marker.
func assertScanMode(t *testing.T, want, base string, mode *localMode, err error) {
	t.Helper()
	if want != "" {
		ref := requireRefusal(t, err, CodeFetchFailed)
		if !strings.Contains(ref.Message, want) {
			t.Errorf("refusal = %q; want it to carry %q", ref.Message, want)
		}
		return
	}
	if err != nil {
		t.Fatalf("resolveScanMode: %v", err)
	}
	if mode.kind != modeScan || mode.baseSHA != base || !mode.dirty {
		t.Errorf("mode = %+v; want kind %q, base %q, dirty", mode, modeScan, base)
	}
}
