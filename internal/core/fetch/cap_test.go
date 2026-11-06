package fetchcore

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/core"
)

// capTestDiff is a one-file, one-hunk staged diff. Its byte length is the
// cap boundary every case below reasons about.
const capTestDiff = "diff --git a/a.txt b/a.txt\n--- a/a.txt\n+++ b/a.txt\n@@ -1 +1 @@\n-a\n+b\n"

// runLocalStagedDiff runs one hermetic fetch-local --staged over diff, with
// the given env knobs and repo files planted first. The typed repository
// configuration (if any) is scripted at the diff base — the base commit
// owns the policy — while the remaining entries plant worktree files for
// the legacy controls the base snapshot still carries.
func runLocalStagedDiff(
	t *testing.T, diff string, env map[string]string, controls map[string]string, chunk bool,
) (*core.FetchResponse, *bytes.Buffer, error) {
	t.Helper()
	cmdr := stagedLocalCommander(t, diff)
	for name, body := range controls {
		if name == ".verdict/config.json" {
			scriptBasePolicy(cmdr, localTestHead, body)
			continue
		}
		if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(name, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	var human, diag bytes.Buffer
	var streamed []string
	resp, err := RunLocal(context.Background(),
		localScriptReq(cmdr, &human, &diag, &streamed, env, core.FetchLocalFlags{Staged: true, ChunkFlag: chunk}))
	return resp, &human, err
}

// capLine runs one staged fetch and returns its human lines.
func capLine(t *testing.T, env, controls map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	t.Chdir(dir)
	_, human, err := runLocalStagedDiff(t, capTestDiff, env, controls, false)
	if err != nil {
		t.Fatalf("RunLocal: %v", err)
	}
	return human.String()
}

// typedConfig renders one repository configuration with the given
// defaults body.
func typedConfig(defaults string) string {
	return `{"schema_version":"1","defaults":{` + defaults + `}}`
}

// TestRunLocalCapSourcePrecedence proves the manual's resolution order —
// env, then the repository configuration, then the silent 512000
// built-in default.
func TestRunLocalCapSourcePrecedence(t *testing.T) {
	size := strconv.Itoa(len(capTestDiff))
	cases := []struct {
		name     string
		env      map[string]string
		controls map[string]string
		want     string
	}{
		{
			// Env wins: the repository configuration says 10, which
			// would refuse.
			name:     "env beats repo config",
			env:      map[string]string{"REVIEW_MAX_BYTES": size},
			controls: map[string]string{".verdict/config.json": typedConfig(`"max_bytes":10`)},
			want:     "» cap: " + size + " bytes (from REVIEW_MAX_BYTES, default 512000)\n",
		},
		{
			// The repository configuration is read when env is absent.
			name:     "repo config when env absent",
			controls: map[string]string{".verdict/config.json": typedConfig(`"max_bytes":` + size)},
			want:     "» cap: " + size + " bytes (from .verdict/config.json, default 512000)\n",
		},
		{
			// Only a deliberate override is announced: the 512000 default
			// prints nothing.
			name: "default is silent",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			assertCapLine(t, capLine(t, c.env, c.controls), c.want)
		})
	}
}

// assertCapLine proves the human lines carry the announced override, or no
// cap line at all when want is empty.
func assertCapLine(t *testing.T, got, want string) {
	t.Helper()
	present := strings.Contains(got, "» cap:")
	if want == "" && present {
		t.Errorf("default run prints a cap line:\n%s", got)
	}
	if want != "" && !strings.Contains(got, want) {
		t.Errorf("human lines miss %q:\n%s", want, got)
	}
}

// TestRunLocalCapGarbageRefuses proves a non-numeric override is a typed
// bad-max-bytes refusal naming the value and its source — never a silent
// fall back to the default.
func TestRunLocalCapGarbageRefuses(t *testing.T) {
	cases := []struct {
		name     string
		env      map[string]string
		controls map[string]string
		source   string
	}{
		{"env", map[string]string{"REVIEW_MAX_BYTES": "nope"}, nil, "REVIEW_MAX_BYTES"},
		{"env zero", map[string]string{"REVIEW_MAX_BYTES": "0"}, nil, "REVIEW_MAX_BYTES"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			t.Chdir(dir)
			_, _, err := runLocalStagedDiff(t, capTestDiff, c.env, c.controls, false)
			ref := requireRefusal(t, err, "bad-max-bytes")
			if !strings.Contains(ref.Message, c.source) {
				t.Errorf("refusal does not name the source %q: %q", c.source, ref.Message)
			}
		})
	}
}

// TestRunLocalCapRepoGarbageRefuses proves a malformed repository
// configuration refuses at parse time: the typed parser owns every
// structural rule, so the fetch never sees the bad value.
func TestRunLocalCapRepoGarbageRefuses(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	_, _, err := runLocalStagedDiff(t, capTestDiff, nil,
		map[string]string{".verdict/config.json": typedConfig(`"max_bytes":"lots"`)}, false)
	ref := requireRefusal(t, err, "bad-config")
	if !strings.Contains(ref.Message, "max_bytes") {
		t.Errorf("refusal does not name the field: %q", ref.Message)
	}
}

// TestRunLocalChunkDefaultOffUnderCap proves an under-cap run with no
// chunk opt-in writes no chunks: the resolved default reads off.
func TestRunLocalChunkDefaultOffUnderCap(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	resp, _, err := runLocalStagedDiff(t, capTestDiff, nil,
		map[string]string{".verdict/config.json": typedConfig(`"chunk":false`)}, false)
	if err != nil {
		t.Fatalf("under-cap run with chunk false: %v", err)
	}
	if resp.Data.Chunk != nil {
		t.Errorf("under-cap run produced chunks: %v", resp.Data.Chunk)
	}
}

// TestRunLocalChunkGarbageRefuses proves a malformed chunk default
// refuses at parse time: the typed parser owns the vocabulary, so the
// fetch never sees the bad value.
func TestRunLocalChunkGarbageRefuses(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	_, _, err := runLocalStagedDiff(t, capTestDiff, nil,
		map[string]string{".verdict/config.json": typedConfig(`"chunk":"maybe"`)}, false)
	ref := requireRefusal(t, err, "bad-config")
	if !strings.Contains(ref.Message, "chunk") {
		t.Errorf("refusal does not name the field: %q", ref.Message)
	}
}

// TestRunLocalChunkRepoOptInArmsOverCap proves the repository chunk
// default arms slicing on the over-cap path: a two-file diff over the
// default cap writes one chunk per file and the envelope carries the
// chunk summary.
func TestRunLocalChunkRepoOptInArmsOverCap(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	resp, human, err := runLocalStagedDiff(t, overCapTwoFiles(), nil,
		map[string]string{".verdict/config.json": typedConfig(`"chunk":true`)}, false)
	if err != nil {
		t.Fatalf("chunked run: %v", err)
	}
	if resp.Data.Chunk == nil {
		t.Fatal("over-cap run with chunk:true wrote no chunks")
	}
	_ = human
	entries, err := os.ReadDir(filepath.Join(".verdict", "state", "local-mine", "chunks"))
	if err != nil || len(entries) == 0 {
		t.Fatalf("chunk dir missing or empty: %v %v", entries, err)
	}
}

// TestRunLocalChunkOffOverCapRefuses proves chunk:false over the cap
// still gives today's advisory diff-too-large refusal.
func TestRunLocalChunkOffOverCapRefuses(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	_, _, err := runLocalStagedDiff(t, overDefaultCapDiff(), nil,
		map[string]string{".verdict/config.json": typedConfig(`"chunk":false`)}, false)
	ref := requireRefusal(t, err, "diff-too-large")
	if !strings.Contains(ref.Message, "512000") {
		t.Errorf("refusal misses the default cap: %q", ref.Message)
	}
}

// TestRunLocalChunkOverrideDisablesChunking proves the manual's rule that
// either cap override disables chunking outright: an over-cap run with
// --chunk still refuses diff-too-large and writes no chunks — including the
// two-file shape that would otherwise slice cleanly.
func TestRunLocalChunkOverrideDisablesChunking(t *testing.T) {
	twoFiles := capTestDiff + strings.ReplaceAll(capTestDiff, "a.txt", "b.txt")
	cases := []struct {
		name    string
		diff    string
		capSize int
	}{
		{"single file over cap", capTestDiff, len(capTestDiff) - 1},
		{"every file fits the cap", twoFiles, len(capTestDiff) + 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			assertOverrideRefuses(t, c.diff, c.capSize)
		})
	}
}

// assertOverrideRefuses proves one opt-in run under a cap override refuses
// diff-too-large and leaves no chunk map behind.
func assertOverrideRefuses(t *testing.T, diff string, capSize int) {
	t.Helper()
	dir := t.TempDir()
	t.Chdir(dir)
	env := map[string]string{"REVIEW_MAX_BYTES": strconv.Itoa(capSize)}
	_, _, err := runLocalStagedDiff(t, diff, env, nil, true)
	ref := requireRefusal(t, err, "diff-too-large")
	if !strings.Contains(ref.Message, strconv.Itoa(capSize)) {
		t.Errorf("refusal misses the override cap: %q", ref.Message)
	}
	if _, serr := os.Stat(filepath.Join(".verdict", "state", "local-mine", "chunks")); !os.IsNotExist(serr) {
		t.Errorf("over-cap refusal wrote a chunks dir: %v", serr)
	}
}

// overCapTwoFiles builds a two-file diff over the 512000-byte default
// cap with each file under it, the only shape that slices cleanly.
func overCapTwoFiles() string {
	line := "+" + strings.Repeat("x", 998) + "\n"
	var sb strings.Builder
	for _, name := range []string{"a.txt", "b.txt"} {
		sb.WriteString("diff --git a/" + name + " b/" + name + "\n--- a/" + name + "\n+++ b/" + name +
			"\n@@ -1 +1 @@\n")
		for range 260 {
			sb.WriteString(line)
		}
	}
	return sb.String()
}

// overDefaultCapDiff builds a one-file diff larger than the 512000-byte
// default cap, the only shape that consults the chunk knob.
func overDefaultCapDiff() string {
	line := "+" + strings.Repeat("x", 998) + "\n"
	var sb strings.Builder
	sb.WriteString("diff --git a/a.txt b/a.txt\n--- a/a.txt\n+++ b/a.txt\n@@ -1 +1 @@\n")
	for sb.Len() <= DefaultDiffCap {
		sb.WriteString(line)
	}
	return sb.String()
}

// TestRunLocalLegacyCapFileIgnored pins the breaking typed-contract
// rule: the retired .review/max_bytes knob file no longer arms, loses,
// or refuses anything. A legacy file alone reads the silent default, a
// conflicting file loses to the typed repository configuration, and
// even garbage in the file cannot refuse the run.
func TestRunLocalLegacyCapFileIgnored(t *testing.T) {
	size := strconv.Itoa(len(capTestDiff))
	t.Run("legacy file alone reads the silent default", func(t *testing.T) {
		dir := t.TempDir()
		t.Chdir(dir)
		got := capLine(t, nil, map[string]string{".review/max_bytes": size + "\n"})
		if strings.Contains(got, "» cap:") {
			t.Errorf("retired knob file armed a cap line:\n%s", got)
		}
	})
	t.Run("typed config wins over a conflicting legacy file", func(t *testing.T) {
		dir := t.TempDir()
		t.Chdir(dir)
		got := capLine(t, nil, map[string]string{
			".verdict/config.json": typedConfig(`"max_bytes":` + size),
			".review/max_bytes":    "10\n",
		})
		if !strings.Contains(got, "from .verdict/config.json") {
			t.Errorf("typed config did not win over the retired file:\n%s", got)
		}
	})
	t.Run("legacy garbage cannot refuse the run", func(t *testing.T) {
		dir := t.TempDir()
		t.Chdir(dir)
		if _, _, err := runLocalStagedDiff(t, capTestDiff, nil,
			map[string]string{".review/max_bytes": "lots\n"}, false); err != nil {
			t.Fatalf("retired knob garbage refused the run: %v", err)
		}
	})
}

// TestRunLocalChunkTypedOptIn pins the typed chunk opt-in on the
// over-cap path: defaults.chunk true arms slicing naming the typed
// configuration, while the retired .review/chunk file arms nothing —
// an opted-out over-cap run refuses diff-too-large and even garbage in
// the file passes silently.
func TestRunLocalChunkTypedOptIn(t *testing.T) {
	t.Run("typed chunk true arms slicing", typedChunkArmsSlicing)
	t.Run("retired chunk file arms nothing", retiredChunkFileArmsNothing)
	t.Run("retired chunk garbage passes silently", retiredChunkGarbagePassesSilently)
}

// typedChunkArmsSlicing checks the typed opt-in slices an over-cap diff and
// names the configuration that armed it.
func typedChunkArmsSlicing(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	chunked := map[string]string{".verdict/config.json": typedConfig(`"chunk":true`)}
	cmdr := stagedLocalCommander(t, overCapTwoFiles())
	scriptBasePolicy(cmdr, localTestHead, chunked[".verdict/config.json"])
	var human, diag bytes.Buffer
	var streamed []string
	resp, err := RunLocal(context.Background(),
		localScriptReq(cmdr, &human, &diag, &streamed, nil, core.FetchLocalFlags{Staged: true}))
	if err != nil {
		t.Fatalf("chunked run: %v", err)
	}
	if resp.Data.Chunk == nil {
		t.Fatal("over-cap run with typed chunk true wrote no chunks")
	}
	if !strings.Contains(diag.String(), "chunking opted in (from .verdict/config.json)") {
		t.Errorf("opt-in notice misses the typed source:\n%s", diag.String())
	}
}

// retiredChunkFileArmsNothing checks the retired .review/chunk knob never
// enables slicing and is not even consulted by the refusal.
func retiredChunkFileArmsNothing(t *testing.T) {
	t.Chdir(t.TempDir())
	_, _, err := runLocalStagedDiff(t, overCapTwoFiles(), nil,
		map[string]string{".review/chunk": "true\n"}, false)
	ref := requireRefusal(t, err, "diff-too-large")
	if strings.Contains(ref.Message, ".review/chunk") {
		t.Errorf("refusal consults the retired file: %q", ref.Message)
	}
}

// retiredChunkGarbagePassesSilently checks unparsable retired content is inert.
func retiredChunkGarbagePassesSilently(t *testing.T) {
	t.Chdir(t.TempDir())
	if _, _, err := runLocalStagedDiff(t, capTestDiff, nil,
		map[string]string{".review/chunk": "maybe\n"}, false); err != nil {
		t.Fatalf("retired knob garbage refused the run: %v", err)
	}
}

// TestRunLocalLegacyPanelIgnored pins the retired panel file: members
// in .review/panel no longer land in the bundle — the run reports no
// panel pinned, exactly like a run with no panel anywhere.
func TestRunLocalLegacyPanelIgnored(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	resp, human, err := runLocalStagedDiff(t, capTestDiff, nil,
		map[string]string{".review/panel": "# staff\nbase-panel\n\n"}, false)
	if err != nil {
		t.Fatalf("RunLocal: %v", err)
	}
	if len(resp.Data.Panel.Members) != 0 {
		t.Errorf("panel = %+v, want no members from the retired file", resp.Data.Panel)
	}
	if !strings.Contains(human.String(), "» panel: none pinned") {
		t.Errorf("panel notice misses the none-pinned line:\n%s", human.String())
	}
}
