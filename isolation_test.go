package skillfs_test

import (
	"os"
	"testing"

	"github.com/stainless-code/verdict/internal/testenv"
)

// TestMain scrubs the git environment the launcher exported, so repoFiles
// resolves the checkout from the working directory, not from GIT_DIR.
func TestMain(m *testing.M) {
	os.Exit(testenv.Main(m))
}
