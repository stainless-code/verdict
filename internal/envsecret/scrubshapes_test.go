package envsecret

import "testing"

func TestScrubShapesPrivateKey(t *testing.T) {
	const input = "before\n-----BEGIN PRIVATE KEY-----\nsecret material\n-----END PRIVATE KEY-----\nafter"
	if got := ScrubShapes(input); got != "before\n***REDACTED***\nafter" {
		t.Fatalf("scrub = %q", got)
	}
}
