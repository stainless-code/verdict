package ingestcore

import (
	"io/fs"
	"testing"

	"github.com/stainless-code/verdict/internal/forgedline"
)

// A SARIF path is an operator or agent value.
func TestSARIFPathCannotForgeErrorLine(t *testing.T) {
	t.Parallel()
	forgedline.AssertOneErrorLine(t, classifySARIFReadErr(forgedline.Value, fs.ErrNotExist),
		"missing SARIF file "+forgedline.Named)
}
