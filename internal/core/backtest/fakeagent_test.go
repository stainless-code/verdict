package backtest

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// The replay tests spawn a real child through the production launcher. No real
// agent CLI and no model is ever invoked: this test binary is symlinked into a
// PATH directory under an agent's name and switched into fake mode by an
// environment variable, so the child observes the exact argv, cwd, and
// directive a real agent would.
const (
	fakeAgentEnv     = "VERDICT_TEST_FAKE_AGENT"
	fakeAgentLogEnv  = "VERDICT_TEST_FAKE_AGENT_LOG"
	fakeAgentCostEnv = "VERDICT_TEST_FAKE_AGENT_COST"
)

// fakeReviewBody is a body the record path accepts unchanged: its Basis line
// carries the counts the record path computes for one walked comment, so the
// recorded review warns about nothing.
func fakeReviewBody(walked int) string {
	return fmt.Sprintf("> Basis: 0 0 %d 0 0 0 0\n\n### One note\n\n"+
		"This line is worth a second look in the historical diff.", walked)
}

// fakeComment is one inline comment, anchored on the bundle's own diff.
type fakeComment struct {
	Path     string `json:"path"`
	Side     string `json:"side"`
	Body     string `json:"body"`
	Severity string `json:"severity"`
	Rigor    string `json:"rigor"`
	Line     int    `json:"line"`
}

// fakeReviewDoc is the review the fake agent writes.
type fakeReviewDoc struct {
	SchemaVersion string        `json:"schema_version"`
	Body          string        `json:"body"`
	Comments      []fakeComment `json:"comments"`
	Approved      bool          `json:"approved"`
}

// fakeAgentMain is the child's side of the contract: it appends one line naming
// its cwd, the artifact the directive asked for, and its own argv, then behaves
// as the planted mode asked.
func fakeAgentMain(mode string) int {
	directive := os.Args[len(os.Args)-1]
	artifact := fakeArtifact(directive)
	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintln(os.Stderr, "fake agent: getwd:", err)
		return 90
	}
	if code := fakeAppendLog(cwd + "\t" + artifact + "\t" + fakeArgvTail() + "\n"); code != 0 {
		return code
	}
	switch mode {
	case "write":
		return fakeWrite(artifact)
	case "exit":
		fmt.Fprintln(os.Stderr, "fake agent: the model refused the request")
		return 7
	default: // "silent": exit 0 and write nothing
		return 0
	}
}

// fakeArgvTail renders the launch's own arguments without the directive, whose
// newlines would split one launch across log lines.
func fakeArgvTail() string {
	if len(os.Args) < 2 {
		return ""
	}
	return strings.Join(os.Args[:len(os.Args)-1], " ")
}

// fakeAppendLog appends one line to the planted log, if any.
func fakeAppendLog(line string) int {
	path := os.Getenv(fakeAgentLogEnv)
	if path == "" {
		return 0
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		fmt.Fprintln(os.Stderr, "fake agent: open log:", err)
		return 91
	}
	if _, err := f.WriteString(line); err != nil {
		fmt.Fprintln(os.Stderr, "fake agent: write log:", err)
		return 92
	}
	if err := f.Close(); err != nil {
		fmt.Fprintln(os.Stderr, "fake agent: close log:", err)
		return 93
	}
	return 0
}

// fakeArtifact extracts the review path from the launcher's directive: the
// launcher always names it between "Write " and " with {".
func fakeArtifact(directive string) string {
	const openMark, closeMark = "Write ", " with {"
	_, rest, ok := strings.Cut(directive, openMark)
	if !ok {
		return ""
	}
	artifact, _, ok := strings.Cut(rest, closeMark)
	if !ok {
		return ""
	}
	return artifact
}

// fakeComments anchors one inline comment on the first added file of the
// bundle's own diff, so the review proves which historical diff it read.
func fakeComments(dir string) []fakeComment {
	raw, err := os.ReadFile(filepath.Join(dir, "full.diff"))
	if err != nil {
		return nil
	}
	for line := range strings.SplitSeq(string(raw), "\n") {
		path, ok := strings.CutPrefix(line, "+++ b/")
		if !ok {
			continue
		}
		return []fakeComment{{
			Path: path, Line: 1, Side: "RIGHT", Body: "Nit: rename this line.",
			Severity: "informational", Rigor: "walked",
		}}
	}
	return nil
}

// fakeWrite writes the review into the bundle the directive named and, when a
// price is planted, the agent-reported cost beside it.
func fakeWrite(artifact string) int {
	if artifact == "" {
		fmt.Fprintln(os.Stderr, "fake agent: the directive names no artifact")
		return 94
	}
	if err := os.MkdirAll(filepath.Dir(artifact), 0o755); err != nil {
		fmt.Fprintln(os.Stderr, "fake agent: create bundle:", err)
		return 95
	}
	comments := fakeComments(filepath.Dir(artifact))
	review, err := json.Marshal(fakeReviewDoc{
		SchemaVersion: "1",
		Body:          fakeReviewBody(len(comments)),
		Comments:      comments,
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "fake agent: render review:", err)
		return 96
	}
	if err := os.WriteFile(artifact, review, 0o644); err != nil {
		fmt.Fprintln(os.Stderr, "fake agent: write review:", err)
		return 96
	}
	price := os.Getenv(fakeAgentCostEnv)
	if price == "" {
		return 0
	}
	body := `{"schema_version":"2","est_usd":` + price + `,"models":[{"name":"fake","input_tokens":10,"output_tokens":5}]}`
	if err := os.WriteFile(filepath.Join(filepath.Dir(artifact), "cost.json"), []byte(body), 0o644); err != nil {
		fmt.Fprintln(os.Stderr, "fake agent: write cost:", err)
		return 97
	}
	return 0
}
