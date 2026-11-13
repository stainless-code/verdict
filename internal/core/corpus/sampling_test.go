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

func TestAnyOfKRejectsDuplicateCohortAndIgnoresFourthSample(t *testing.T) {
	s := []Sample{
		{CaseID: "case", Bugs: 2, Hits: []int{0}, Sampling: Sampling{RunID: "r", K: 1, Repeats: 2, Sample: 1}},
		{CaseID: "case", Bugs: 2, Hits: []int{1}, Sampling: Sampling{RunID: "r", K: 1, Repeats: 2, Sample: 2}},
	}
	m, bad := AnyOfKRecall(s)
	if m.Value != 0.5 || bad != 0 {
		t.Fatalf("first-one = %+v bad=%d; want .5 bad=0", m, bad)
	}
	s[1] = s[0]
	m, bad = AnyOfKRecall(s)
	if m.Value != nil || bad != 1 {
		t.Fatalf("duplicate = %+v bad=%d; want null bad=1", m, bad)
	}
}
