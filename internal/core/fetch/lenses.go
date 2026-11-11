package fetchcore

import (
	"fmt"
	"os"
	"strings"

	"github.com/stainless-code/verdict/internal/atomic"
	"github.com/stainless-code/verdict/internal/config"
	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/core/pack"
)

// loadLensPack resolves the built-in pack through the injected skill FS. No
// injected FS or no pack means no lens surface, never a failure.
func loadLensPack(ctx2 *fetchRunCtx) *core.RefusalError {
	if ctx2.runner.packFS == nil {
		return nil
	}
	p, ref := pack.Load(ctx2.runner.packFS, pack.BuiltinRoot)
	if ref != nil {
		return ref
	}
	ctx2.lensPack = p
	return nil
}

// writeLensBodies writes lenses.txt: every lens body delimited and labeled
// with its id and hash8. A pack that cannot be written is advisory context,
// so the run warns and keeps the bundle.
func writeLensBodies(ctx2 *fetchRunCtx) {
	path := ctx2.dir + suffixLensesTxt
	if ctx2.lensPack == nil {
		_ = os.Remove(path)
		return
	}
	if err := atomic.WriteFile(path, []byte(ctx2.lensPack.Note())); err != nil {
		ctx2.runner.Warn(fmt.Sprintf("lens pack skipped: could not write %s (%v)", path, err))
	}
}

// lensPlanFields appends the pack identity and the group's offered lens ids
// with their eight-hex body hashes, sorted by id.
func lensPlanFields(p *pack.Pack, group *config.PlanGroup) string {
	if p == nil {
		return ""
	}
	offered := p.Offered(group.Files)
	ids := make([]string, len(offered))
	for i := range offered {
		ids[i] = offered[i].ID + ":" + offered[i].Hash8
	}
	return fmt.Sprintf(" lens_pack=%s@%s lenses=[%s]",
		p.Name, p.Version, strings.Join(ids, listSep))
}
