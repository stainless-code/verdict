package index

import (
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/cli"
)

// TestParseCallersLimitRefuses pins the CLI's --limit usage refusal: one
// message and exit code for every out-of-range shape.
func TestParseCallersLimitRefuses(t *testing.T) {
	for _, value := range []string{"0", "201", "x", "-1"} {
		var diag strings.Builder
		r := &cli.Runner{Diag: &diag}
		if _, code, done := parse(r, []string{"callers", "a.Target", "--limit", value}); !done || code != cli.ExitUsage {
			t.Errorf("--limit %s = code %d, done %t; want usage refusal", value, code, done)
		}
		if want := "--limit wants an integer from 1 to 200\n"; diag.String() != want {
			t.Errorf("--limit %s stderr = %q; want %q", value, diag.String(), want)
		}
	}
}

// TestParseCallersLimitAcceptsBounds proves both published bounds parse.
func TestParseCallersLimitAcceptsBounds(t *testing.T) {
	for _, want := range []int{1, 200} {
		r := &cli.Runner{Diag: &strings.Builder{}}
		inv, code, done := parse(r, []string{"callers", "a.Target", "--limit", strconv.Itoa(want)})
		if done || code != 0 || inv.limit != want {
			t.Errorf("--limit %d = limit %d, code %d, done %t; want accepted", want, inv.limit, code, done)
		}
	}
}

// TestParseGraphSubcommands pins the new subcommands' argv contract:
// defaults and flag mapping.
func TestParseGraphSubcommands(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want invocation
	}{
		{
			name: "co-changed defaults", args: []string{"co-changed", "a/a.go"},
			want: invocation{
				query: "co-changed", path: "a/a.go", limit: 20, commits: 2000, minCount: 2,
			},
		},
		{
			name: "co-changed flags",
			args: []string{"co-changed", "a/a.go", "--commits", "50", "--since", "90d", "--min-count", "3", "--limit", "7"},
			want: invocation{
				query: "co-changed", path: "a/a.go", since: "90d", limit: 7, commits: 50, minCount: 3,
			},
		},
		{
			name: "tests-for path", args: []string{"tests-for", "a/a.go", "--at", "HEAD~1", "--depth", "2", "--limit", "2"},
			want: invocation{query: "tests-for", path: "a/a.go", at: "HEAD~1", limit: 2, depth: 2},
		},
		{
			name: "tests-for symbol", args: []string{"tests-for", "--symbol", "a.Target"},
			want: invocation{query: "tests-for", symbol: "a.Target", limit: 50},
		},
		{
			name: "owners", args: []string{"owners", "a/a.go"},
			want: invocation{query: "owners", path: "a/a.go"},
		},
		{
			name: "owners at a tree", args: []string{"owners", "a/a.go", "--at", "HEAD~1"},
			want: invocation{query: "owners", path: "a/a.go", at: "HEAD~1"},
		},
		{
			name: "blast-radius paths", args: []string{"blast-radius", "--paths", "a/a.go", "--paths", "b/b.go", "--limit", "7"},
			want: invocation{
				query: "blast-radius", paths: []string{"a/a.go", "b/b.go"},
				limit: 7, commits: 2000, minCount: 2,
			},
		},
		{
			name: "blast-radius pr", args: []string{"blast-radius", "--pr", "12", "--at", "HEAD~1", "--min-count", "3"},
			want: invocation{
				query: "blast-radius", pr: "12", at: "HEAD~1",
				limit: 50, commits: 2000, minCount: 3,
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			inv := parseAccepted(t, tc.args)
			if !sameInvocation(&inv, &tc.want) {
				t.Errorf("invocation = %+v; want %+v", inv, tc.want)
			}
		})
	}
}

// sameInvocation compares two parsed invocations, slices included.
func sameInvocation(a, b *invocation) bool {
	return a.query == b.query && a.from == b.from && a.at == b.at &&
		a.symbol == b.symbol && a.path == b.path && a.since == b.since &&
		a.pr == b.pr && a.commits == b.commits && a.minCount == b.minCount &&
		a.limit == b.limit && a.depth == b.depth && slices.Equal(a.paths, b.paths)
}

// parseAccepted parses one accepted argv and fails on any refusal.
func parseAccepted(t *testing.T, args []string) invocation {
	t.Helper()
	var diag strings.Builder
	inv, code, done := parse(&cli.Runner{Diag: &diag}, args)
	if done || code != 0 {
		t.Fatalf("parse refused: %q", diag.String())
	}
	return inv
}

// TestParseGraphRefusals pins one usage refusal per bad new-subcommand shape.
func TestParseGraphRefusals(t *testing.T) {
	cases := []struct {
		name string
		diag string
		args []string
		code int
	}{
		{
			name: "co-changed commits bound", args: []string{"co-changed", "a/a.go", "--commits", "0"},
			code: cli.ExitUsage, diag: "index co-changed wants --commits from 1 to 100000\n",
		},
		{
			name: "co-changed min count bound", args: []string{"co-changed", "a/a.go", "--min-count", "x"},
			code: cli.ExitUsage, diag: "index co-changed wants --min-count from 1 to 100000\n",
		},
		{
			name: "co-changed limit bound", args: []string{"co-changed", "a/a.go", "--limit", "201"},
			code: cli.ExitUsage, diag: "index co-changed wants --limit from 1 to 200\n",
		},
		{
			name: "co-changed missing path", args: []string{"co-changed"},
			code: cli.ExitUsage,
			diag: "usage: verdict index co-changed <path> [--commits <n>] [--since <dur|ref>] " +
				"[--min-count <n>] [--limit <n>]\n",
		},
		{
			name: "tests-for neither", args: []string{"tests-for"},
			code: cli.ExitUsage,
			diag: "usage: verdict index tests-for <path>|--symbol <pkg.Name> [--at <tree>] [--depth <n>] [--limit <n>]\n",
		},
		{
			name: "tests-for both", args: []string{"tests-for", "a/a.go", "--symbol", "a.Target"},
			code: cli.ExitUsage,
			diag: "usage: verdict index tests-for <path>|--symbol <pkg.Name> [--at <tree>] [--depth <n>] [--limit <n>]\n",
		},
		{
			name: "tests-for limit bound", args: []string{"tests-for", "a/a.go", "--limit", "0"},
			code: cli.ExitUsage, diag: "index tests-for wants --limit from 1 to 200\n",
		},
		{
			name: "tests-for depth bound", args: []string{"tests-for", "a/a.go", "--depth", "9"},
			code: cli.ExitUsage, diag: "index tests-for wants --depth from 1 to 8\n",
		},
		{
			name: "tests-for depth is not a number", args: []string{"tests-for", "a/a.go", "--depth", "deep"},
			code: cli.ExitUsage, diag: "index tests-for wants --depth from 1 to 8\n",
		},
		{
			name: "owners missing path", args: []string{"owners"},
			code: cli.ExitUsage, diag: "usage: verdict index owners <path> [--at <tree>]\n",
		},
		{
			name: "owners unknown flag", args: []string{"owners", "a/a.go", "--limit", "2"},
			code: cli.ExitUsage, diag: "unknown flag '--limit' for index owners (want --at <tree>)\n",
		},
		{
			name: "blast-radius neither input", args: []string{"blast-radius"},
			code: cli.ExitUsage, diag: blastUsage + "\n",
		},
		{
			name: "blast-radius both inputs", args: []string{"blast-radius", "--pr", "7", "--paths", "a/a.go"},
			code: cli.ExitUsage, diag: blastUsage + "\n",
		},
		{
			name: "blast-radius positional", args: []string{"blast-radius", "a/a.go"},
			code: cli.ExitUsage,
			diag: "index blast-radius takes --pr <n> or --paths <path>, not the positional 'a/a.go'\n",
		},
		{
			name: "blast-radius limit bound", args: []string{"blast-radius", "--paths", "a/a.go", "--limit", "0"},
			code: cli.ExitUsage, diag: "index blast-radius wants --limit from 1 to 200\n",
		},
		{
			name: "blast-radius commits bound", args: []string{"blast-radius", "--paths", "a/a.go", "--commits", "0"},
			code: cli.ExitUsage, diag: "index blast-radius wants --commits from 1 to 100000\n",
		},
		{
			name: "unknown subcommand", args: []string{"calers", "app.Target"},
			code: cli.ExitUsage,
			diag: "unknown index subcommand 'calers' (want build, status, prune, callers, tests-for, co-changed, owners, blast-radius)\n",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assertUsageRefusal(t, tc.args, tc.code, tc.diag)
		})
	}
}

// assertUsageRefusal parses one argv that must refuse with the exact stderr.
func assertUsageRefusal(t *testing.T, args []string, code int, diag string) {
	t.Helper()
	var stderr strings.Builder
	got, gotCode, done := parse(&cli.Runner{Diag: &stderr}, args)
	if !done || gotCode != code {
		t.Fatalf("parse = invocation %+v, code %d, done %t; want usage refusal", got, gotCode, done)
	}
	if stderr.String() != diag {
		t.Errorf("stderr = %q; want %q", stderr.String(), diag)
	}
}
