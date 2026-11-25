package loopcore

import (
	"os"
	"testing"

	"github.com/stainless-code/verdict/internal/testenv"
)

func TestMain(m *testing.M) { os.Exit(testenv.Main(m)) }
