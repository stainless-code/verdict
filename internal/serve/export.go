package serve

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/stainless-code/verdict/internal/cli"
	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/core/query"
	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/webui"
)

const (
	exportIndex     = "index.html"
	exportNotExport = "html-not-export"
	exportPerm      = 0o600
)

// RunHTML writes the dashboard's static snapshot without starting a listener.
func RunHTML(ctx context.Context, r *cli.Runner, args []string, version string) int {
	dest, root, usage := htmlFlags(args)
	if usage != nil {
		return r.ReportUsage(usage)
	}
	resolved, ref := resolveRoot(ctx, root)
	if ref != nil {
		return r.Refuse(ref)
	}
	layer, err := htmlLayer(resolved, root != "", version)
	if err != nil {
		return r.Refuse(err)
	}
	if err := exportHTML(ctx, dest, layer, r.Now()); err != nil {
		return r.Refuse(err)
	}
	fmt.Fprintf(r.Human, "wrote %s\n", dest)
	return cli.ExitOK
}

func htmlFlags(args []string) (dest, root string, usage *cli.UsageError) {
	values := map[string]string{}
	for i := 0; i < len(args); i += 2 {
		if i+1 >= len(args) || (args[i] != "--html" && args[i] != "--root") {
			return "", "", &cli.UsageError{Message: "report --html <dir> [--root <dir>]"}
		}
		values[args[i]] = args[i+1]
	}
	if values["--html"] == "" {
		return "", "", &cli.UsageError{Message: "report --html requires a directory"}
	}
	return values["--html"], values["--root"], nil
}

func htmlLayer(root string, discover bool, version string) (*query.Layer, error) {
	// Export envelopes use the Unix epoch; only generated_at records export time.
	clock := func() time.Time { return time.Unix(0, 0).UTC() }
	if discover {
		return discoveryLayer(root, clock, Request{Version: version}, &serveOptions{
			depth: query.DefaultDiscoveryDepth, noRegistry: true, rediscover: true, noWatch: true, check: defaultCheck,
		})
	}
	return query.New(query.Snapshot{
		Version: version, Roots: []query.Root{{Path: root}}, Runtime: &core.Runtime{Now: clock},
	}, query.Options{Clock: clock, NoWatch: true}), nil
}

type htmlSnapshot map[string]string

func (s htmlSnapshot) add(route string, view *query.View, err error) error {
	if err != nil {
		return s.refusal(route, err)
	}
	s[route] = jq.PrettyOrdered(core.RenderEnvelope(verbServe, view.Repo, view.Pr, view.At,
		view.Next, view.Warnings, view.Data, nil))
	return nil
}

func (s htmlSnapshot) refusal(route string, err error) error {
	ref, ok := errors.AsType[*core.RefusalError](err)
	if !ok {
		return exportFailure(fmt.Errorf("compose %s: %w", route, err))
	}
	problem := jq.NewObj("code", ref.Code, "message", ref.Message+"\n"+core.HintFor(ref.Code))
	s[route] = jq.PrettyOrdered(core.RenderEnvelope(verbServe, "", "", core.NowUTC(time.Unix(0, 0)), "", nil, nil, problem))
	return nil
}

func snapshotData(ctx context.Context, layer Layer, now time.Time) ([]byte, error) {
	data := htmlSnapshot{"generated_at": strconv.Quote(core.NowUTC(now))}
	if err := data.add("/v1/roots", layer.Roots(ctx), nil); err != nil {
		return nil, err
	}
	bundles, err := layer.Bundles(ctx, "")
	if err := data.add("/v1/bundles", bundles, err); err != nil {
		return nil, err
	}
	if err := data.aggregates(ctx, layer); err != nil {
		return nil, err
	}
	if err := data.details(ctx, layer, bundles); err != nil {
		return nil, err
	}
	return data.bytes(), nil
}

func (s htmlSnapshot) aggregates(ctx context.Context, layer Layer) error {
	for _, by := range []string{"repo", "week", "model"} {
		view, err := layer.Cost(ctx, by)
		if err := s.add("/v1/cost?by="+by, view, err); err != nil {
			return err
		}
	}
	for _, by := range []string{"category", "tier"} {
		view, err := layer.Precision(ctx, by)
		if err := s.add("/v1/precision?by="+by, view, err); err != nil {
			return err
		}
	}
	return nil
}

func (s htmlSnapshot) details(ctx context.Context, layer Layer, bundles *query.View) error {
	value, _ := bundles.Data.Get("rows")
	rows, ok := value.([]any)
	if !ok && value != nil {
		return exportFailure(errors.New("unexpected bundle listing"))
	}
	for _, value := range rows {
		row, ok := value.(*jq.Obj)
		if !ok {
			return exportFailure(errors.New("unexpected bundle row"))
		}
		root := exportString(row, "root")
		bundle := exportString(row, "bundle")
		route := "/v1/roots/" + url.PathEscape(root) + "/bundles/" + url.PathEscape(bundle)
		view, err := layer.Detail(ctx, root, bundle)
		if err := s.add(route, view, err); err != nil {
			return err
		}
	}
	return nil
}

func (s htmlSnapshot) bytes() []byte {
	var out strings.Builder
	out.WriteString("{\n")
	for i, key := range slices.Sorted(maps.Keys(s)) {
		if i > 0 {
			out.WriteString(",\n")
		}
		out.WriteString(strconv.Quote(key))
		out.WriteByte(':')
		out.WriteString(s[key])
	}
	out.WriteString("\n}\n")
	return []byte(out.String())
}

func exportHTML(ctx context.Context, dest string, layer Layer, now time.Time) error {
	return exportWithRename(ctx, dest, layer, now, os.Rename)
}

func exportWithRename(ctx context.Context, dest string, layer Layer, now time.Time, rename func(string, string) error) error {
	dest = filepath.Clean(dest)
	if err := exportRemnants(dest); err != nil {
		return err
	}
	if err := exportDestination(dest); err != nil {
		return err
	}
	body, err := snapshotData(ctx, layer, now)
	if err != nil {
		return exportFailure(err)
	}
	stage, err := os.MkdirTemp(filepath.Dir(dest), filepath.Base(dest)+".tmp-*")
	if err != nil {
		return exportFailure(err)
	}
	defer func() { _ = os.RemoveAll(stage) }()
	if err := writeExport(stage, body); err != nil {
		return exportFailure(err)
	}
	if err := ctx.Err(); err != nil {
		return exportFailure(err)
	}
	return publishExport(stage, dest, rename)
}

func writeExport(stage string, body []byte) error {
	for _, name := range webui.Names() {
		asset, err := webui.Asset(name)
		if err != nil {
			return exportFailure(err)
		}
		if name == exportIndex {
			asset = []byte(strings.Replace(string(asset), `content="live"`, `content="static"`, 1))
		}
		if err := os.WriteFile(filepath.Join(stage, name), asset, exportPerm); err != nil {
			return err
		}
	}
	return os.WriteFile(filepath.Join(stage, "data.json"), body, exportPerm)
}

func publishExport(stage, dest string, rename func(string, string) error) error {
	_, err := os.Lstat(dest)
	if os.IsNotExist(err) {
		return publishNew(stage, dest, rename)
	}
	if err != nil {
		return exportFailure(err)
	}
	old := dest + ".old-" + strings.TrimPrefix(filepath.Base(stage), filepath.Base(dest)+".tmp-")
	if err := rename(dest, old); err != nil {
		return exportFailure(err)
	}
	if err := rename(stage, dest); err != nil {
		return rollbackExport(old, dest, err, rename)
	}
	if err := os.RemoveAll(old); err != nil {
		return exportFailure(err)
	}
	return nil
}

func publishNew(stage, dest string, rename func(string, string) error) error {
	if err := rename(stage, dest); err != nil {
		return exportFailure(err)
	}
	return nil
}

func rollbackExport(old, dest string, publish error, rename func(string, string) error) error {
	if err := rename(old, dest); err != nil {
		return exportFailure(fmt.Errorf("publish: %w; rollback: %w; restore %s manually", publish, err, old))
	}
	return exportFailure(publish)
}

func exportDestination(dest string) error {
	info, err := os.Lstat(dest)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return exportFailure(err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return &cli.RefusalError{Code: "html-symlink", Message: "HTML export destination must not be a symlink"}
	}
	if !info.IsDir() {
		return notExport()
	}
	return validateExport(dest)
}

func validateExport(dest string) error {
	entries, err := os.ReadDir(dest)
	if err != nil {
		return exportFailure(err)
	}
	if len(entries) == 0 {
		return nil
	}
	if !exportEntries(entries) {
		return notExport()
	}
	page, err := os.ReadFile(filepath.Join(dest, exportIndex))
	if err != nil || !strings.Contains(string(page), `<meta name="verdict-mode" content="static" />`) {
		return notExport()
	}
	return nil
}

func exportEntries(entries []os.DirEntry) bool {
	expected := map[string]bool{exportIndex: true, "app.js": true, "app.css": true, "data.json": true}
	if len(entries) != len(expected) {
		return false
	}
	for _, entry := range entries {
		if !expected[entry.Name()] || !entry.Type().IsRegular() {
			return false
		}
	}
	return true
}

func notExport() error {
	return &cli.RefusalError{Code: exportNotExport, Message: "non-empty destination is not a previous HTML export"}
}

func exportFailure(err error) error {
	return &cli.RefusalError{Code: "report-failed", Message: err.Error()}
}

func exportRemnants(dest string) error {
	entries, err := os.ReadDir(filepath.Dir(dest))
	if err != nil {
		return exportFailure(err)
	}
	base := filepath.Base(dest)
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), base+".old-") || strings.HasPrefix(entry.Name(), base+".tmp-") {
			return exportFailure(errors.New("interrupted HTML export: inspect sibling " + entry.Name() +
				"; restore the .old-* directory if the destination is absent, then move stale " +
				".old-* and .tmp-* siblings away before retrying"))
		}
	}
	return nil
}

func exportString(row *jq.Obj, key string) string {
	value, ok := row.Fields[key].(string)
	if !ok {
		return ""
	}
	return value
}
