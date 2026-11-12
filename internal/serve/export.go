package serve

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/stainless-code/verdict/internal/cli"
	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/core/query"
	"github.com/stainless-code/verdict/internal/jq"
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
	err = exportHTML(ctx, dest, layer, r.Now())
	if code, done := r.Canceled(ctx, "report"); done {
		return code
	}
	if err != nil {
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
		return s.refusal(route, view, err)
	}
	s[route] = jq.PrettyOrdered(core.RenderEnvelope(verbServe, view.Repo, view.Pr, view.At,
		view.Next, view.Warnings, view.Data, nil))
	return nil
}

func (s htmlSnapshot) refusal(route string, view *query.View, err error) error {
	ref, ok := errors.AsType[*core.RefusalError](err)
	if !ok {
		return exportFailure(fmt.Errorf("compose %s: %w", route, err))
	}
	problem := jq.NewObj("code", ref.Code, "message", ref.Message+"\n"+core.HintFor(ref.Code))
	at, pr := core.NowUTC(time.Unix(0, 0)), ""
	if view != nil {
		at, pr = view.At, view.Pr
	}
	s[route] = jq.PrettyOrdered(core.RenderEnvelope(verbServe, "", pr, at, "", nil, nil, problem))
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
		view = detailExportView(view, bundles.At, bundle)
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

func exportString(row *jq.Obj, key string) string {
	value, ok := row.Fields[key].(string)
	if !ok {
		return ""
	}
	return value
}

func detailExportView(view *query.View, at, bundle string) *query.View {
	if view == nil {
		return &query.View{At: at, Pr: bundle}
	}
	return view
}
