//go:build unix

package pack

import (
	"fmt"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/stainless-code/verdict/internal/core"
)

// TestListRefusesFIFOUnderRepoPacks proves a special file directly under the
// pack root refuses pack-unsafe-path instead of reading as an ignored
// non-pack: the controls read refuses it, so the list cannot promise to skip
// it.
func TestListRefusesFIFOUnderRepoPacks(t *testing.T) {
	t.Parallel()
	root, settings := listFixtures(t, `"replace":["acme/org"],`)
	pipe := filepath.Join(root, ".verdict", "packs", "pipe")
	if err := syscall.Mkfifo(pipe, 0o600); err != nil {
		t.Skipf("this platform cannot create a FIFO here: %v", err)
	}
	_, ref := RunList(t.Context(), &Request{
		core.ReadRequest{Runtime: &core.Runtime{PackFS: listBuiltin()}, RepoRoot: root},
		settings,
		true,
	})
	if ref == nil || ref.Code != CodePackUnsafePath {
		t.Fatalf("FIFO pack root = %v, want pack-unsafe-path", ref)
	}
	if want := fmt.Sprintf("%q", pipe); !strings.Contains(ref.Message, want) {
		t.Fatalf("message = %q, want it to name %s", ref.Message, want)
	}
}
