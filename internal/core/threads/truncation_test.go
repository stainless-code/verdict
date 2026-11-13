package threadscore

import "testing"

func TestEmptyFeedReceipt(t *testing.T) {
	t.Parallel()
	got := (&File{Truncated: true}).Wire()
	for key, want := range map[string]any{"truncated": true, "truncation_reason": "scan_cap", "returned": int64(0), "total": nil, "next_offset": nil} {
		value, ok := got.Get(key)
		if !ok || value != want {
			t.Errorf("receipt %s = %v (present %v), want %v", key, value, ok, want)
		}
	}
}
