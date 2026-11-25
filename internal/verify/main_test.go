package verify_test

import (
	"os"
	"testing"

	"github.com/stainless-code/verdict/internal/testenv"
)

// TestMain isolates every git child from the inherited GIT_DIR, index, and
// config, so a fixture command can never act on the developer's checkout.
func TestMain(m *testing.M) {
	os.Exit(testenv.Main(m))
}
