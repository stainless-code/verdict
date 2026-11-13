package config

import "testing"

func TestSeverityWeights25Plus5Plus1Equals31(t *testing.T) {
	c, ref := Parse([]byte(`{"schema_version":"1","severity_weights":{"important":7}}`))
	if ref != nil {
		t.Fatal(ref)
	}
	w := SeverityWeights(nil)
	if w.Critical != 25 || w.Important != 5 || w.Informational != 1 {
		t.Fatalf("default weights=%+v; want 25/5/1", w)
	}
	if got := w.Weight("critical") + w.Weight("important") + w.Weight("informational"); got != 31 {
		t.Fatalf("default sum = %d, want 31", got)
	}
	w = SeverityWeights(c)
	if got := w.Weight("critical") + w.Weight("important") + w.Weight("informational"); got != 33 {
		t.Fatalf("override sum = %d, want 33", got)
	}
}

func TestSeverityWeightsRejectInvalid(t *testing.T) {
	for _, raw := range []string{`null`, `{"critical":null}`, `{"critical":0}`, `{"important":-1}`, `{"informational":1.5}`, `{"critical":1000001}`, `{"typo":1}`} {
		if _, ref := Parse([]byte(`{"schema_version":"1","severity_weights":` + raw + `}`)); ref == nil {
			t.Errorf("accepted weights %s", raw)
		}
	}
}
