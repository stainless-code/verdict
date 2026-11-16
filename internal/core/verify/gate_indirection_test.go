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
	{"builtin wrapper", `E=echo; builtin $E pwned`},
	{"redirect in", `E=echo; </dev/null $E pwned`},
	{"redirect out", `E=echo; >/dev/null $E pwned`},
	{"redirect fd dup", `E=echo; 2>&1 $E pwned`},
	{"redirect here string", `E=echo; <<<x $E pwned`},
	{"redirect separate target", `E=echo; >> fz-notes.txt $E pwned`},
	{"command substitution", `E=echo; echo $($E pwned)`},
	{"command substitution nested", `E=echo; echo $(echo $($E pwned))`},
	{"backtick substitution", "E=echo; echo `$E pwned`"},
	{"process substitution", `E=echo; cat <($E pwned)`},
	{"shell inline command", `E=echo; sh -c "$E pwned"`},
	{"shell inline command clustered", `E=echo; bash -ec "$E pwned"`},
}

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
		`cat </dev/null`,
		`printf ok < fz-notes.txt`,
		`echo $(date)`,
		"echo `date`",
		`diff <(sort a) <(sort b)`,
		`builtin echo ok`,
		`bash ./fz-missing.sh $TARGET`,
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

func TestCheckRefusesEvalAndSudoBeforeTheWrapperRule(t *testing.T) {
	wantRefusal(t, `E=echo; eval $E pwned`, "verify-destructive",
		"refusing verifyCmd: matched ' eval ' (eval with non-literal args refuses — fail closed)")
	wantRefusal(t, `E=echo; sudo $E pwned`, "verify-destructive",
		"refusing verifyCmd: matched ' sudo ' (privilege change refuses regardless of payload)")
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
