package initcore

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// shareConfigTail is the verify section init writes for a checkout whose only
// lockfile is npm's.
const shareConfigTail = `  },
  "verify": {
    "share": ["node_modules"]
  }
}`

// TestInitProposesTheDependencyDirAPackageLockImplies proves the proposal
// lands in the written configuration only when a lockfile implies it.
func TestInitProposesTheDependencyDirAPackageLockImplies(t *testing.T) {
	t.Parallel()
	repo := t.TempDir()
	if err := os.WriteFile(filepath.Join(repo, "package-lock.json"), []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustRunInit(t, ciRequest(repo, githubTarget(t)))
	if got := writtenConfigText(t, repo); !strings.HasSuffix(strings.TrimRight(got, "\n"), shareConfigTail) {
		t.Fatalf(".verdict/config.json =\n%s\nwant it to end with the node_modules proposal:\n%s", got, shareConfigTail)
	}

	bare := t.TempDir()
	mustRunInit(t, ciRequest(bare, githubTarget(t)))
	if got := writtenConfigText(t, bare); strings.Contains(got, `"verify"`) {
		t.Fatalf("init proposed a share with no lockfile:\n%s", got)
	}
}

// TestProposedSharesReadTheLockfileTable covers every row once: one entry
// per directory, in table order, and nothing for a lockfile that is not a
// regular file.
func TestProposedSharesReadTheLockfileTable(t *testing.T) {
	t.Parallel()
	repo := t.TempDir()
	for _, name := range []string{"yarn.lock", "pnpm-lock.yaml", "poetry.lock", "uv.lock"} {
		if err := os.WriteFile(filepath.Join(repo, name), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Mkdir(filepath.Join(repo, "package-lock.json"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(proposedShares(repo), ","); got != "node_modules,.venv" {
		t.Fatalf("proposedShares = %q, want node_modules,.venv", got)
	}
}

func writtenConfigText(t *testing.T, repo string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(repo, reviewDotName, configName))
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}
