package run

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stainless-code/verdict/internal/core"
)

func TestOpencodeSeedResolvesDotdotAfterALink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need privileges on Windows")
	}
	t.Parallel()
	base := mustResolve(t, t.TempDir())
	a, b := filepath.Join(base, "a"), filepath.Join(base, "b")
	if err := os.MkdirAll(a, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(b, "deep"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(b, "deep"), filepath.Join(a, "jump")); err != nil {
		t.Fatal(err)
	}
	raw := a + string(filepath.Separator) + "jump" + string(filepath.Separator) + ".."
	for _, variant := range []string{"database", "source"} {
		t.Run(variant, func(t *testing.T) {
			checkSeedDotdot(t, b, raw, variant)
		})
	}
}

func checkSeedDotdot(t *testing.T, b, raw, variant string) {
	t.Helper()
	home, tmp := t.TempDir(), t.TempDir()
	plantOpencodeDB(t, b)
	env := []string{"HOME=" + home}
	if variant == "database" {
		data, err := os.ReadFile(filepath.Join(b, "opencode", "opencode.db"))
		if err != nil {
			t.Fatal(err)
		}
		writeUnder(t, b, "oc.db", string(data))
		env = append(env, "OPENCODE_DB="+raw+string(filepath.Separator)+"oc.db")
	} else {
		env = append(env, "XDG_DATA_HOME="+raw)
	}
	def, _ := lookupAgent("opencode")
	_, warnings, err := ephemeralEnv(&def, env, tmp)
	if err != nil || len(warnings) != 0 {
		t.Fatalf("seed = %v, warnings %q", err, warnings)
	}
	got := readSeeded(t, filepath.Join(tmp, "home-xdg_data_home", "opencode", "auth.json"))
	want := `{"anthropic":{"key":"an-key","type":"api"},"openai":{"key":"active","type":"api"},"opencode":{"type":"oauth","refresh":"r","access":"a","expires":5}}`
	if variant == "source" {
		want = seededLogins
	}
	if string(got) != want {
		t.Errorf("seeded logins = %s, want %s", got, want)
	}
}

func TestConfigPathPolicyFailureIsTyped(t *testing.T) {
	t.Parallel()
	st := &state{}
	def, _ := lookupAgent("claude")
	args := &launchArgs{def: def}
	failure := &core.StartError{Err: &policyError{err: errors.New("configuration path cannot be resolved")}}
	ref := st.verdict(t.Context(), args, nil, failure, artifactState{})
	if ref == nil || ref.Code != "review-sandbox-unavailable" {
		t.Fatalf("refusal = %v, want review-sandbox-unavailable", ref)
	}
}
