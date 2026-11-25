package cli

import (
	"strings"
	"testing"
)

func TestResolveAttributionEnv(t *testing.T) {
	t.Parallel()
	cases := []struct {
		val, want, warn string
	}{
		{"full", "full", ""},
		{"off", "off", ""},
		{"product", "product", ""},
		{"true", "", "ignoring invalid REVIEW_PUBLISH_ATTRIBUTION 'true' (want off|product|full)"},
		{"false", "", "ignoring invalid REVIEW_PUBLISH_ATTRIBUTION 'false' (want off|product|full)"},
	}
	for _, c := range cases {
		t.Run(c.val, func(t *testing.T) {
			t.Parallel()
			checkAttributionEnv(t, c.val, c.want, c.warn)
		})
	}
}

func checkAttributionEnv(t *testing.T, val, want, warn string) {
	t.Helper()
	var eff Effective
	var warns []string
	resolveAttributionEnv(map[string]string{"REVIEW_PUBLISH_ATTRIBUTION": val}, &eff, func(m string) { warns = append(warns, m) })
	if eff.PublishAttribution != want {
		t.Errorf("attribution = %q, want %q", eff.PublishAttribution, want)
	}
	if got := strings.Join(warns, "|"); got != warn {
		t.Errorf("warn = %q, want %q", got, warn)
	}
}

func TestResolveAttributionKeyBoolRefuses(t *testing.T) {
	t.Parallel()
	for _, b := range []bool{true, false} {
		var eff Effective
		err := resolveAttributionKey("s.json", map[string]any{"attribution": b}, &eff)
		if err == nil || !strings.Contains(err.Error(), "must be a string (want off|product|full)") {
			t.Errorf("bool %v: err = %v", b, err)
		}
	}
}
