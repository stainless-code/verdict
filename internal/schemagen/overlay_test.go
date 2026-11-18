package schemagen

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// overlayRoot builds the smallest document the pointer walker descends into: a
// $defs property that already carries a description and a Go type.
func overlayRoot() *node {
	return object(true,
		member{keyDefs, object(true,
			member{"entry", object(true,
				member{"properties", object(true,
					member{"category", object(true,
						member{keyDescription, stringNode("Optional taxonomy.")},
						member{keyType, stringNode("string")},
					)},
				)},
			)},
		)},
	)
}

// TestEnumOverlayReplacesTheTagForm proves an overlay enum prints the way the
// equivalent struct tag does: the closed list, null for the null type, and no
// separate Go type beside it.
func TestEnumOverlayReplacesTheTagForm(t *testing.T) {
	t.Parallel()
	root := overlayRoot()
	enums := map[string][]string{"/$defs/entry/properties/category": {"logic", "style", "null"}}
	if err := mergeEnums("findings", root, enums); err != nil {
		t.Fatalf("mergeEnums: %v", err)
	}
	want := `{"$defs":{"entry":{"properties":{"category":{
		"description":"Optional taxonomy.","enum":["logic","style",null]}}}}}`
	var gotDoc, wantDoc any
	if err := json.Unmarshal(printDocument(root), &gotDoc); err != nil {
		t.Fatalf("decode rendered overlay: %v", err)
	}
	if err := json.Unmarshal([]byte(want), &wantDoc); err != nil {
		t.Fatalf("decode expectation: %v", err)
	}
	if !reflect.DeepEqual(gotDoc, wantDoc) {
		t.Errorf("rendered overlay enum = %s, want %s", printDocument(root), want)
	}
}

// TestEnumOverlayRefusesCollisions proves an unusable overlay entry fails
// generation instead of printing an empty or doubly-declared list.
func TestEnumOverlayRefusesCollisions(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		enums map[string][]string
		want  string
	}{
		{"empty list", map[string][]string{"/$defs/entry/properties/category": {}}, "empty enum"},
		{
			"not an object",
			map[string][]string{"/$defs/entry/properties/category/description": {"logic"}},
			"target is not an object schema",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := mergeEnums("findings", overlayRoot(), tc.enums)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("mergeEnums error = %v, want one naming %q", err, tc.want)
			}
		})
	}
}

// TestEnumOverlayRefusesASecondList proves a tagged enum and an overlay enum
// cannot both land on one property.
func TestEnumOverlayRefusesASecondList(t *testing.T) {
	t.Parallel()
	root := overlayRoot()
	enums := map[string][]string{"/$defs/entry/properties/category": {"logic"}}
	if err := mergeEnums("findings", root, enums); err != nil {
		t.Fatalf("first mergeEnums: %v", err)
	}
	err := mergeEnums("findings", root, enums)
	if err == nil || !strings.Contains(err.Error(), "enum already set") {
		t.Fatalf("second mergeEnums error = %v, want one naming the collision", err)
	}
}
