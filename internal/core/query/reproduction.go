package query

import (
	"context"

	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/readcommon"
)

// Reproduction returns a bounded, symlink-free artifact for a served finding.
func (l *Layer) Reproduction(ctx context.Context, rootID, bundle, sig string) ([]byte, error) {
	view, err := l.Show(ctx, rootID, bundle, sig)
	if err != nil {
		return nil, err
	}
	record, _ := view.Data.Get("item")
	item, ok := record.(*jq.Obj)
	if !ok || item == nil {
		return nil, ErrNotFound
	}
	root, ok := l.rootByID(rootID)
	if !ok {
		return nil, ErrNotFound
	}
	dir, ok, ref := l.resolve(root, bundle)
	if ref != nil {
		return nil, ref
	}
	if !ok {
		return nil, ErrNotFound
	}
	raw, err := readcommon.ReadReproduction(dir, item)
	if err != nil {
		return nil, ErrNotFound
	}
	return raw, nil
}
