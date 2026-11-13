package corpuscore

import "testing"

func TestAnyOfThreeUnionTwoOfThreeNotThreeOfNine(t *testing.T) {
	samples := []Sample{
		{CaseID: "bugs", Bugs: 3, Hits: []int{0}, Sampling: Sampling{RunID: "run", K: 3, Repeats: 3, Sample: 1}},
		{CaseID: "bugs", Bugs: 3, Hits: []int{1}, Sampling: Sampling{RunID: "run", K: 3, Repeats: 3, Sample: 2}},
		{CaseID: "bugs", Bugs: 3, Hits: []int{0}, Sampling: Sampling{RunID: "run", K: 3, Repeats: 3, Sample: 3}},
	}
	m, incomplete := AnyOfKRecall(samples)
	if m.Value != 0.6667 || m.N != 3 || m.Numerator != 2 || m.Denominator != 3 || incomplete != 0 {
		t.Fatalf("any-of-three = %+v incomplete=%d; want 2/3=0.6667 n=3", m, incomplete)
	}
	m, incomplete = AnyOfKRecall(samples[:2])
	if m.Value != nil || incomplete != 1 {
		t.Fatalf("incomplete = %+v cohorts=%d; want null, 1", m, incomplete)
	}
}
