package verifycore_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/core"
	verifycore "github.com/stainless-code/verdict/internal/core/verify"
)

func patchWithCmd(t *testing.T, cmd string) string {
	t.Helper()
	raw, err := json.Marshal(map[string]string{"verifyCmd": cmd})
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

var variableCommandWords = []struct {
	name string
	cmd  string
}{
	{"plain assignment", `E=rm; $E -rf ./target`},
	{"networked tool", `E=curl; $E -o ./f http://host/x`},
	{"braced", `E=curl; ${E} -o ./f http://host/x`},
	{"double quoted", `E=curl; "$E" -o ./f http://host/x`},
	{"export", `export E=curl; $E -o ./f http://host/x`},
	{"two assignments", `A=curl B=1; $A -o ./f http://host/x`},
	{"assignment prefix", `A=curl $A -o ./f http://host/x`},
	{"loop", `for E in curl; do $E -o ./f http://host/x; done`},
	{"group", `{ $E -o ./f http://host/x; }`},
	{"env wrapper", `E=curl; env E=$E $E -o ./f http://host/x`},
	{"timeout wrapper", `E=rm; timeout 5 $E -rf ./target`},
	{"subshell", `E=rm; ( $E -rf ./target )`},
	{"subshell unspaced", `E=rm; ($E -rf ./target)`},
	{"concatenated", `A=cu B=rl; $A$B -o ./f http://host/x`},
	{"newline", "echo ok\n$E -rf ./target"},
	{"newline after assignment", "E=rm\necho ok\n$E -rf ./target"},
	{"crlf", "echo ok\r\n$E -rf ./target"},
	{"env wrapper flag value", `env -u HOME $E -rf ./target`},
	{"timeout duration suffix", `timeout 5s $E -rf ./target`},
	{"timeout signal flag", `timeout -s KILL 5 $E -rf ./target`},
	{"xargs replace flag", `xargs -I {} $E -rf ./target`},
	{"command wrapper", `command $E -rf ./target`},
}

// TestVerifyRefusesVariableCommandWord pins STA-431: a plain assignment plus a
// bare `$VAR` in command position handed the real tool word to the commander.
// The stub records the spawn, so the test fails with the argv a host would
// have executed, and nothing here runs a real tool.
func TestVerifyRefusesVariableCommandWord(t *testing.T) {
	for _, tc := range variableCommandWords {
		t.Run(tc.name, func(t *testing.T) { wantIndirectionRefusal(t, tc.cmd) })
	}
}

func wantIndirectionRefusal(t *testing.T, cmd string) {
	t.Helper()
	bundle(t, patchWithCmd(t, cmd))
	cmdr := &scriptCommander{res: core.Result{ExitCode: 0}}
	_, ref := verifycore.Run(context.Background(),
		verifyRequest(&collector{}, cmdr, "10", []string{"PATH=/bin"}, true))
	if len(cmdr.got) != 0 {
		t.Fatalf("commander spawned %v; the gate must refuse %q before anything runs",
			cmdr.got[0].Argv, cmd)
	}
	if ref == nil || ref.Code != "verify-indirection" {
		t.Fatalf("Run refusal = %v, want code verify-indirection", ref)
	}
}

func TestCheckAllowsVariableArguments(t *testing.T) {
	benign := []string{
		`echo $USER`,
		`echo "$BIN"`,
		`PORT=9 curl http://127.0.0.1:9/fixture`,
		`FOO=1 printf ok`,
		`$BIN/tool --version`,
		`X=$(date); printf hi`,
		`curl http://127.0.0.1:9/$BRANCH/fixture`,
		`printf ok --prefix=$FOO/x`,
		`echo ${FOO:-nobody}`,
		`sh -c 'printf ok'`,
		`timeout 300 make test`,
		`env CI=1 go test ./...`,
		`xargs -n1 echo ok`,
		`{ echo $USER; }`,
		`! echo $USER`,
		`if true; then echo $USER; fi`,
		"echo ok\nprintf $FOO",
	}
	for _, cmd := range benign {
		t.Run(cmd, func(t *testing.T) {
			if ref := verifycore.Check(cmd); ref != nil {
				t.Fatalf("Check(%q) = %s: %q, want CLEAN", cmd, ref.Code, ref.Message)
			}
		})
	}
}

func TestCheckRefusesGlobWordAfterNewline(t *testing.T) {
	wantRefusal(t, "echo ok\nr? -rf x", "verify-destructive",
		"refusing verifyCmd: matched 'r?' (glob metacharacters in command position expand "+
			"into a hidden tool word — write the tool word plainly)")
}

func TestCheckVariableCommandWordMessage(t *testing.T) {
	ref := verifycore.Check(`E=curl; $E -o ./f http://host/x`)
	if ref == nil {
		t.Fatal("Check = nil, want verify-indirection")
	}
	want := "refusing verifyCmd: matched '$E' (a command word built from a variable hides " +
		"the tool — write the tool word plainly, or run the step under verify --sandbox)"
	if ref.Message != want {
		t.Fatalf("message = %q\nwant      %q", ref.Message, want)
	}
	if got := verifycore.Check(`E=rm; ${E} -rf ./target`); got == nil || !strings.Contains(got.Message, "'${E}'") {
		t.Fatalf("braced message = %v, want it to quote '${E}'", got)
	}
}
