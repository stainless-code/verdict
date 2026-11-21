package provenance_test

import (
	"testing"

	"github.com/stainless-code/verdict/internal/provenance"
)

func TestPhaseSpendFooterDoesNotInventRootPrice(t *testing.T) {
	cases := []struct{ name, cost, want string }{
		{"unreported", `{"est_usd":99}`, "unrecorded"},
		{"priced", `{"execution_id":"execution-A","est_usd":99,"phases":[{"execution_id":"execution-A","launch_id":"finder-1-discovery-1","finder":"finder-1","phase":"discovery","usd":0.3,"input_tokens":null,"output_tokens":null},{"execution_id":"execution-A","launch_id":"finder-2-discovery-1","finder":"finder-2","phase":"discovery","usd":0.4,"input_tokens":null,"output_tokens":null}]}`, "$0.7"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := writeBundle(t, map[string]string{"run.json": `{"execution":{"id":"execution-A","finders":2}}`, "cost.json": tc.cost})
			tier, cost := provenance.Spend(dir)
			if tier != "standard" || cost != tc.want {
				t.Fatalf("spend = %s %s; want standard %s", tier, cost, tc.want)
			}
		})
	}
}

func phasedRecord(finder, launch, tier, usd string) string {
	return `{"execution_id":"execution-A","effort":"` + tier + `","phases":[{"execution_id":"execution-A","launch_id":"` +
		launch + `","finder":"` + finder + `","phase":"discovery","usd":` + usd +
		`,"input_tokens":null,"output_tokens":null}]}`
}

func TestPhaseSpendReadsTierFromPhasedRecords(t *testing.T) {
	cases := []struct{ name, first, second, want string }{
		{"both deep", "deep", "deep", "deep"},
		{"both standard", "standard", "standard", "standard"},
		{"mixed", "cheap", "deep", "standard"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := writeBundle(t, map[string]string{
				"run.json":             `{"execution":{"id":"execution-A","finders":2}}`,
				"x-finder-1/cost.json": phasedRecord("finder-1", "finder-1-discovery-1", tc.first, "0.3"),
				"x-finder-2/cost.json": phasedRecord("finder-2", "finder-2-discovery-1", tc.second, "0.4"),
			})
			tier, cost := provenance.Spend(dir)
			if tier != tc.want || cost != "$0.7" {
				t.Fatalf("spend = %s %s; want %s $0.7", tier, cost, tc.want)
			}
		})
	}
}
