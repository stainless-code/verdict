package fetchcore

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/stainless-code/verdict/internal/atomic"
	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/readcommon"
)

func finishTruncation(data *core.FetchData) error {
	source := readcommon.SourceTruncation(data.Dir)
	if source != nil {
		data.Receipt = source
	} else if data.Receipt != nil {
		data.Receipt = core.ResultCut(data.Receipt.TruncationReason, int(readcommon.NumOr(data.Files)), nil, nil)
	}
	path := filepath.Join(data.Dir, "truncation.json")
	if data.Receipt == nil {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return truncationWriteError(err)
		}
		return nil
	}
	if err := atomic.WriteFile(path, []byte(jq.PrettyOrdered(core.Wire(data.Receipt)))); err != nil {
		return truncationWriteError(err)
	}
	if data.Artifacts != nil {
		data.Artifacts.Set("truncation.json", path)
	}
	return nil
}

func truncationWriteError(err error) error {
	return &core.RefusalError{Code: CodeFetchFailed, Message: fmt.Sprintf("could not update truncation.json: %v", err)}
}

func (s *session) observePaging(cut bool) {
	if cut {
		s.receipt = core.ScanCut(true, 0)
	}
}
