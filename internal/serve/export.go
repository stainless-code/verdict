package serve

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/stainless-code/verdict/internal/cli"
	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/core/query"
	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/webui"
)

// RunHTML writes the dashboard's static snapshot without starting a listener.
func RunHTML(ctx context.Context, r *cli.Runner, args []string, version string) int {
	var dest, root string
	for i := 0; i < len(args); i += 2 {
		if i+1 >= len(args) {
			return r.ReportUsage(&cli.UsageError{Message: "report --html <dir> [--root <dir>]"})
		}
		switch args[i] {
		case "--html":
			dest = args[i+1]
		case "--root":
			root = args[i+1]
		default:
			return r.ReportUsage(&cli.UsageError{Message: "report --html accepts only --html and --root"})
		}
	}
	if dest == "" {
		return r.ReportUsage(&cli.UsageError{Message: "report --html requires a directory"})
	}
	resolved, ref := resolveRoot(ctx, root)
	if ref != nil {
		return r.Refuse(ref)
	}
	// Snapshot envelopes use a fixed clock; only generated_at records export time.
	clock := func() time.Time { return time.Unix(0, 0).UTC() }
	var layer *query.Layer
	if root == "" {
		layer = query.New(query.Snapshot{Version: version, Roots: []query.Root{{Path: resolved}}, Runtime: &core.Runtime{Now: clock}}, query.Options{Clock: clock, NoWatch: true})
	} else {
		var err error
		layer, err = discoveryLayer(resolved, clock, Request{Version: version}, &serveOptions{depth: query.DefaultDiscoveryDepth, noRegistry: true, rediscover: true, noWatch: true, check: defaultCheck})
		if err != nil {
			return r.Refuse(&cli.RefusalError{Code: codeBadRoot, Message: err.Error()})
		}
	}
	if err := exportHTML(ctx, dest, layer, r.Now()); err != nil {
		return r.Refuse(err)
	}
	fmt.Fprintf(r.Human, "wrote %s\n", dest)
	return cli.ExitOK
}

func snapshotData(ctx context.Context, layer Layer, now time.Time) ([]byte, error) {
	data := map[string]json.RawMessage{}
	add := func(path, verb string, view *query.View, err error) error {
		if err != nil {
			return fmt.Errorf("compose %s: %v", path, err)
		}
		data[path] = json.RawMessage(jq.PrettyOrdered(core.RenderEnvelope(verb, view.Repo, view.Pr, view.At, view.Next, view.Warnings, view.Data, nil)))
		return nil
	}
	if err := add("/v1/roots", verbServe, layer.Roots(ctx), nil); err != nil {
		return nil, err
	}
	bundles, err := layer.Bundles(ctx, "")
	if err := add("/v1/bundles", verbServe, bundles, err); err != nil {
		return nil, err
	}
	for _, by := range []string{"repo", "week", "model"} {
		view, err := layer.Cost(ctx, by)
		if err := add("/v1/cost?by="+by, verbServe, view, err); err != nil {
			return nil, err
		}
	}
	for _, by := range []string{"category", "tier"} {
		view, err := layer.Precision(ctx, by)
		if err := add("/v1/precision?by="+by, verbServe, view, err); err != nil {
			return nil, err
		}
	}
	rows, _ := bundles.Data.Get("rows")
	raw, err := json.Marshal(rows)
	if err != nil {
		return nil, fmt.Errorf("marshal bundle rows: %v", err)
	}
	var listing []struct {
		Root   string `json:"root"`
		Bundle string `json:"bundle"`
	}
	if err := json.Unmarshal(raw, &listing); err != nil {
		return nil, fmt.Errorf("decode bundle rows: %v", err)
	}
	for _, row := range listing {
		path := "/v1/roots/" + url.PathEscape(row.Root) + "/bundles/" + url.PathEscape(row.Bundle)
		view, err := layer.Detail(ctx, row.Root, row.Bundle)
		if err := add(path, verbServe, view, err); err != nil {
			return nil, err
		}
	}
	stamp, err := json.Marshal(core.NowUTC(now))
	if err != nil {
		return nil, fmt.Errorf("marshal export time: %v", err)
	}
	data["generated_at"] = stamp
	body, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal snapshot: %v", err)
	}
	return append(body, '\n'), nil
}

func exportHTML(ctx context.Context, dest string, layer Layer, now time.Time) error {
	body, err := snapshotData(ctx, layer, now)
	if err != nil {
		return &cli.RefusalError{Code: "report-failed", Message: err.Error()}
	}
	dest, err = filepath.Abs(dest)
	if err != nil {
		return &cli.RefusalError{Code: "report-failed", Message: err.Error()}
	}
	stage, err := os.MkdirTemp(filepath.Dir(dest), ".verdict-export-*")
	if err != nil {
		return &cli.RefusalError{Code: "report-failed", Message: err.Error()}
	}
	defer func() { _ = os.RemoveAll(stage) }()
	for _, name := range webui.Names() {
		asset, err := webui.Asset(name)
		if err != nil {
			return &cli.RefusalError{Code: "report-failed", Message: err.Error()}
		}
		if name == "index.html" {
			asset = []byte(strings.Replace(string(asset), `content="live"`, `content="static"`, 1))
		}
		if err := os.WriteFile(filepath.Join(stage, name), asset, 0o600); err != nil {
			return &cli.RefusalError{Code: "report-failed", Message: err.Error()}
		}
	}
	if err := os.WriteFile(filepath.Join(stage, "data.json"), body, 0o600); err != nil {
		return &cli.RefusalError{Code: "report-failed", Message: err.Error()}
	}
	if err := ctx.Err(); err != nil {
		return &cli.RefusalError{Code: "report-failed", Message: err.Error()}
	}
	if err := os.Rename(stage, dest); err != nil {
		return &cli.RefusalError{Code: "report-failed", Message: err.Error()}
	}
	return nil
}
