package schemagen

import (
	"reflect"
	"testing"

	"github.com/stainless-code/verdict/internal/core"
)

// TestReportComplianceMirrorsTheSourceDTO keeps the mirror honest: the schema
// may add bounds the wire lacks, but it may not drop, rename, or retype a
// field the source DTO emits. A source addition fails here until the mirror
// and the generated schemas move together.
func TestReportComplianceMirrorsTheSourceDTO(t *testing.T) {
	source := reflect.TypeFor[core.ReportCompliance]()
	mirror := reflect.TypeFor[ReportCompliance]()
	if source.NumField() != mirror.NumField() {
		t.Fatalf("core.ReportCompliance has %d fields, the published mirror %d: regenerate the mirror",
			source.NumField(), mirror.NumField())
	}
	for i := range source.NumField() {
		sf, mf := source.Field(i), mirror.Field(i)
		if sf.Name != mf.Name || sf.Type.Kind() != mf.Type.Kind() {
			t.Fatalf("field %d: source %s %s vs mirror %s %s", i, sf.Name, sf.Type, mf.Name, mf.Type)
		}
		if tag := sf.Tag.Get("json"); tag != mf.Tag.Get("json") {
			t.Fatalf("field %s: source json tag %q vs mirror %q", sf.Name, tag, mf.Tag.Get("json"))
		}
	}
}
