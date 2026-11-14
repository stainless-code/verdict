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
	if source := readcommon.SourceTruncation(data.Dir); source != nil {
		data.Receipt = source
	}
	path := filepath.Join(data.Dir, "truncation.json")
	if data.Receipt == nil || data.Receipt.TruncationReason == "chunk_cap" {
		return clearBundleTruncation(data, path)
	}
	if err := atomic.WriteFile(path, []byte(jq.PrettyOrdered(core.Wire(data.Receipt)))); err != nil {
		return truncationWriteError(err)
	}
	if data.Artifacts != nil {
		data.Artifacts.Set("truncation.json", path)
	}
	return nil
}

func clearBundleTruncation(data *core.FetchData, path string) error {
	if err := removeTruncation(path); err != nil {
		return err
	}
	if data.Receipt == nil {
		data.Receipt = readcommon.ArtifactTruncation(filepath.Join(data.Dir, "threads.json"))
	}
	return nil
}

func removeTruncation(path string) error {
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return truncationWriteError(err)
	}
	return nil
}

func truncationWriteError(err error) error {
	return &core.RefusalError{Code: CodeFetchFailed, Message: fmt.Sprintf("could not update truncation.json: %v", err)}
}

func (s *session) observePaging(cut bool, returned int) {
	if cut {
		s.receipt = core.ScanCut(true, returned)
	}
}
