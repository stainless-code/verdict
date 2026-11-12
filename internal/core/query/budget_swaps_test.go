package query

import (
	"os"
	"path/filepath"
	"testing"
)

// TestInventoryBudgetSwaps verifies the published refusal or read contract.
func TestInventoryBudgetSwaps(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "review.json")
	checkInventoryOversizeCharge(t)
	for _, budget := range []int64{1, 7, 31} {
		for _, sizes := range [][]int{{1, 80, 2, 0}, {80, 1, 0, 90}, {0, 2, 90, 1}} {
			checkInventorySwaps(t, root, path, budget, sizes)
		}
	}
}

func checkInventorySwaps(t *testing.T, root, path string, budget int64, sizes []int) {
	t.Helper()
	c := &sourceCollector{rootPath: root, maxBytes: budget}
	for i, size := range sizes {
		writeSwapFile(t, path, size)
		c.beforeOpen = func(p string) {
			writeSwapFile(t, p, sizes[(i+1)%len(sizes)])
		}
		c.add("42", path)
		if c.bytes < 0 || c.bytes > budget {
			t.Fatalf("charged %d exceeds budget %d", c.bytes, budget)
		}
	}
}

func checkInventoryOversizeCharge(t *testing.T) {
	t.Helper()
	root := t.TempDir()
	path := filepath.Join(root, "review.json")
	c := &sourceCollector{rootPath: root, maxBytes: 7}
	if err := os.WriteFile(path, make([]byte, sourceCap+1), 0o600); err != nil {
		t.Fatal(err)
	}
	c.add("42", path)
	if c.bytes != 7 {
		t.Fatalf("charged %d; want 7", c.bytes)
	}
}

func writeSwapFile(t *testing.T, path string, size int) {
	t.Helper()
	if err := os.WriteFile(path, make([]byte, size), 0o600); err != nil {
		t.Fatal(err)
	}
}
