package query

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/stainless-code/verdict/internal/config"
	"github.com/stainless-code/verdict/internal/core"
	coreexec "github.com/stainless-code/verdict/internal/core/exec"
	"github.com/stainless-code/verdict/internal/gitcmd"
)

// Git index modes a tracked entry that is not a plain file carries.
const (
	modeSymlink = "120000"
	modeGitlink = "160000"
)

// gateTimeout bounds one git call the trust gate makes.
const gateTimeout = 10 * time.Second

// trustGate decides, once per served root and index state, whether the
// root's verdict data may be read at all.
//
// Threat model: the content under review is the repository's committed
// tree, and git records a committed symlink exactly, as a tracked entry with
// mode 120000. Any tracked symlink (or gitlink) at or under .verdict marks
// the root's verdict data untrusted, and every reader the layer and the
// watcher run over that root refuses. A link the operator made locally
// (untracked, or in a root with no git checkout) is the operator's own and
// is trusted; the static checks (storeOutside, insideRoot) still apply. A
// root inside a checkout whose git call fails refuses: unverifiable is not
// trusted. A link swapped in after a check needs a live local writer, who
// can already read the target, and is out of scope.
type trustGate struct {
	// base is the gate's own lifetime: its cached answer is shared across
	// requests, so no one request's cancel may poison it; every call is
	// still bounded by gateTimeout.
	base  context.Context //nolint:containedctx // see above: the cache outlives any request
	roots map[string]gateState
	run   func(ctx context.Context, root string, args ...string) (*core.Result, error)
	mu    sync.Mutex
}

// gateState is one root's cached verdict: the index file it was computed
// from, that file's stamp, and the refusal (nil when trusted).
type gateState struct {
	ref   *core.RefusalError
	index string
	stamp indexStamp
}

type indexStamp struct {
	mod     int64
	size    int64
	present bool
}

func newTrustGate() *trustGate {
	return &trustGate{base: context.Background(), roots: map[string]gateState{}, run: runGit}
}

func runGit(ctx context.Context, root string, args ...string) (*core.Result, error) {
	probe, stop := context.WithTimeout(ctx, gateTimeout)
	defer stop()
	res, err := (coreexec.Runner{OutputCap: coreexec.ReadOutputCap}).Run(probe, &core.Command{
		Argv: gitcmd.Argv(root, args...), Env: gitcmd.Env(nil), OutputCap: coreexec.ReadOutputCap,
	})
	//nolint:wrapcheck // the caller names the git call in its refusal
	return &res, err
}

// gate is the process's trust gate: its cache is keyed by root and index
// stamp, so every layer over the same root shares one answer.
var gate = newTrustGate()

// rootTrust is the gate every reader of a root's verdict data passes: the
// store listing, every bundle resolution, and the watcher.
func rootTrust(root Root) *core.RefusalError {
	return gate.check(root.Path)
}

func (g *trustGate) check(root string) *core.RefusalError {
	if !insideCheckout(root) {
		return nil
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	state := g.roots[root]
	if state.index == "" {
		index, ref := g.indexPath(root)
		if ref != nil {
			return ref
		}
		state.index = index
	}
	stamp := stampIndex(state.index)
	if cached, ok := g.roots[root]; ok && cached.index == state.index && cached.stamp == stamp {
		return cached.ref
	}
	state.stamp, state.ref = stamp, g.scan(root)
	g.roots[root] = state
	return state.ref
}

// indexPath names the index file git reads for root.
func (g *trustGate) indexPath(root string) (string, *core.RefusalError) {
	res, ref := g.git(root, "rev-parse", "--git-path", "index")
	if ref != nil {
		return "", ref
	}
	index := strings.TrimSpace(res.Stdout)
	if !filepath.IsAbs(index) {
		index = filepath.Join(root, index)
	}
	return index, nil
}

// scan lists the tracked entries at or under .verdict and refuses the first
// symlink or gitlink. The pathspec matches a tracked .verdict link itself,
// whose children git would not list, and matches case-insensitively: on a
// case-insensitive filesystem a tracked .Verdict is the directory the
// readers open as .verdict.
func (g *trustGate) scan(root string) *core.RefusalError {
	res, ref := g.git(root, "--icase-pathspecs", "ls-files", "-s", "-z", "--", config.VerdictDir)
	if ref != nil {
		return ref
	}
	for entry := range bytes.SplitSeq([]byte(res.Stdout), []byte{0}) {
		meta, name, ok := bytes.Cut(entry, []byte{'\t'})
		if !ok {
			continue
		}
		mode, _, _ := bytes.Cut(meta, []byte{' '})
		if m := string(mode); m == modeSymlink || m == modeGitlink {
			return &core.RefusalError{Code: config.CodeBadConfig, Message: core.Msgf(
				"serve: %s is a tracked link in this checkout — verdict data under .verdict is not read "+
					"while the repository commits a symlink there; remove it from the index", string(name))}
		}
	}
	return nil
}

// git runs one gate call, refusing typed when it cannot answer.
func (g *trustGate) git(root string, args ...string) (*core.Result, *core.RefusalError) {
	res, err := g.run(g.base, root, args...)
	sub := gitcmd.Subcommand(gitcmd.Argv(root, args...))
	switch {
	case err != nil:
		return nil, gateFailure(sub, err.Error())
	case res.StdoutTruncated:
		return nil, gateFailure(sub, "output exceeds the read cap")
	case res.ExitCode != 0:
		return nil, gateFailure(sub, strings.TrimSpace(res.Stderr))
	}
	return res, nil
}

func gateFailure(sub, cause string) *core.RefusalError {
	return &core.RefusalError{Code: config.CodeBadConfig, Message: core.Msgf(
		"serve: cannot verify this checkout's .verdict holds no tracked symlink — git %s failed: %s", sub, cause)}
}

// stampIndex reads the index file's identity for the cache.
func stampIndex(path string) indexStamp {
	info, err := os.Stat(path)
	if err != nil {
		return indexStamp{}
	}
	return indexStamp{mod: info.ModTime().UnixNano(), size: info.Size(), present: true}
}

// insideCheckout reports a .git entry at root or any ancestor: a root with
// none holds no committed content.
func insideCheckout(root string) bool {
	dir := root
	for {
		if _, err := os.Lstat(filepath.Join(dir, ".git")); err == nil {
			return true
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return false
		}
		dir = parent
	}
}
