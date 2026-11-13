package readycore

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/textproto"
	"strings"

	"github.com/stainless-code/verdict/internal/core"
	coreexec "github.com/stainless-code/verdict/internal/core/exec"
	"github.com/stainless-code/verdict/internal/jq"
)

const (
	waitReadCode = "pr-read-failed"
	waitMerged   = "MERGED"
)

// WaitSnapshot is one observation.
type WaitSnapshot struct {
	State       string `json:"state"`
	Head        string `json:"head"`
	Reviews     string `json:"reviews"`
	Threads     string `json:"threads"`
	Checks      string `json:"checks"`
	ChecksKnown bool   `json:"checks_known"`
	ChecksDone  bool   `json:"checks_done"`
	ChecksGreen bool   `json:"checks_green"`
}

// WaitProbe retains only an invocation-local conditional response cache.
type WaitProbe struct {
	cached map[string]any
	etag   string
}

// Read reads a complete GitHub observation without writing local files.
// Other providers refuse explicitly, before executing any host command.
func (p *WaitProbe) Read(ctx context.Context, req *core.ReadRequest, provider string) (*WaitSnapshot, *core.RefusalError) {
	if provider != providerGitHub {
		return nil, &core.RefusalError{Code: "provider-unimplemented", Message: "pr-wait on " + provider + " is unsupported; " +
			"supported hosts: github"}
	}
	live, ref := p.pull(ctx, req)
	if ref != nil {
		return nil, ref
	}
	raw, _, ok := coreexec.Gh(ctx, req.Commander, "pr", "view", req.Pr, "--repo", req.Repo, "--json",
		"headRefOid,statusCheckRollup,reviews")
	if !ok {
		return nil, waitReadFailed()
	}
	detail, ref := decodeWaitDetail(raw, live)
	if ref != nil {
		return nil, ref
	}

	threads, ref := waitThreads(ctx, req)
	if ref != nil {
		return nil, ref
	}

	return waitSnapshot(live, detail.Checks, detail.Reviews, threads)
}

func waitSnapshot(live map[string]any, checks []map[string]any, reviews, threads []any) (*WaitSnapshot, *core.RefusalError) {
	head, ok := live["head"].(map[string]any)
	if !ok {
		return nil, waitReadFailed()
	}
	if threads == nil {
		threads = []any{}
	}
	snapshot := &WaitSnapshot{
		State: strings.ToUpper(jq.StrOf(live["state"])), Head: jq.StrOf(head["sha"]),
		Reviews: jq.Compact(reviews), Threads: jq.Compact(threads), Checks: jq.Compact(checks),
	}
	if live["merged_at"] != nil {
		snapshot.State = waitMerged
	}
	snapshot.ChecksKnown, snapshot.ChecksDone, snapshot.ChecksGreen = waitChecks(checks)
	if !validWaitState(snapshot) {
		return nil, waitReadFailed()
	}
	return snapshot, nil
}

func validWaitState(snapshot *WaitSnapshot) bool {
	return snapshot.Head != "" && (snapshot.State == stateOpen || snapshot.State == stateClosed || snapshot.State == waitMerged)
}

func (p *WaitProbe) pull(ctx context.Context, req *core.ReadRequest) (map[string]any, *core.RefusalError) {
	argv := []string{"api", "repos/" + req.Repo + "/pulls/" + req.Pr, "--include"}
	if p.etag != "" {
		argv = append(argv, "-H", "If-None-Match: "+p.etag)
	}
	if req.Commander == nil {
		return nil, waitReadFailed()
	}
	result, err := req.Commander.Run(ctx, &core.Command{Argv: append([]string{"gh"}, argv...)})
	if err != nil || result.StdoutTruncated {
		return nil, waitReadFailed()
	}
	raw := result.Stdout
	if result.ExitCode != 0 && httpStatusCode(strings.SplitN(raw, "\n", 2)[0]) != "304" {
		return nil, waitReadFailed()
	}

	return p.parsePull(raw)
}

func httpStatusCode(statusLine string) string {
	fields := strings.Fields(statusLine)
	if len(fields) < 2 || !strings.HasPrefix(fields[0], "HTTP/") {
		return ""
	}
	return fields[1]
}

func (p *WaitProbe) parsePull(raw string) (map[string]any, *core.RefusalError) {
	reader := bufio.NewReader(strings.NewReader(raw))
	status, err := reader.ReadString('\n')
	if err != nil {
		return nil, waitReadFailed()
	}
	header, err := textproto.NewReader(reader).ReadMIMEHeader()
	if err != nil {
		return nil, waitReadFailed()
	}
	code := httpStatusCode(status)
	if code == "304" && p.cached != nil {
		return p.cached, nil
	}
	if code != "200" {
		return nil, waitReadFailed()
	}
	var doc map[string]any
	if json.NewDecoder(reader).Decode(&doc) != nil {
		return nil, waitReadFailed()
	}
	p.etag = http.Header(header).Get("ETag")
	p.cached = doc
	return doc, nil
}

func waitReadFailed() *core.RefusalError {
	return &core.RefusalError{Code: waitReadCode, Message: "could not read complete PR wait state"}
}

func waitChecks(checks []map[string]any) (bool, bool, bool) {
	if len(checks) == 0 {
		return false, false, false
	}
	done, green := true, true
	for _, check := range checks {
		terminal, passed, known := waitCheck(check)
		if !known {
			return false, false, false
		}
		done = done && terminal
		green = green && passed
	}
	return true, done, done && green
}

func waitCheck(check map[string]any) (bool, bool, bool) {
	switch jq.StrOf(check["__typename"]) {
	case "CheckRun":
		return waitCheckRun(check)
	case "StatusContext":
		return waitStatus(check)
	}
	return false, false, false
}

func waitCheckRun(check map[string]any) (bool, bool, bool) {
	switch jq.StrOf(check["status"]) {
	case "QUEUED", "IN_PROGRESS", "WAITING", "PENDING", "REQUESTED":
		return false, false, true
	case "COMPLETED":
		conclusion := jq.StrOf(check["conclusion"])
		if conclusion == "" {
			return false, false, false
		}
		return true, conclusion == "SUCCESS" || conclusion == "NEUTRAL" || conclusion == "SKIPPED", true
	}
	return false, false, false
}

func waitStatus(check map[string]any) (bool, bool, bool) {
	switch jq.StrOf(check["state"]) {
	case "PENDING", "EXPECTED":
		return false, false, true
	case "SUCCESS":
		return true, true, true
	case "ERROR", "FAILURE":
		return true, false, true
	}
	return false, false, false
}

type waitDetail struct {
	Head    string           `json:"headRefOid"`
	Checks  []map[string]any `json:"statusCheckRollup"`
	Reviews []any            `json:"reviews"`
}

func decodeWaitDetail(raw string, live map[string]any) (*waitDetail, *core.RefusalError) {
	detail := &waitDetail{}
	if json.Unmarshal([]byte(raw), detail) != nil {
		return nil, waitReadFailed()
	}
	if detail.Reviews == nil || len(detail.Reviews) >= waitPageLimit {
		return nil, waitReadFailed()
	}
	head, ok := live["head"].(map[string]any)
	if !ok || detail.Head != jq.StrOf(head["sha"]) {
		return nil, waitReadFailed()
	}
	return detail, nil
}
