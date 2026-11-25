package readycore

import (
	"maps"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/core"
)

// TestHostImplementationsMatchTable fails when a host is advertised without an
// implementation or implemented without being advertised: the capability table
// is the one source, and these maps are the code behind it.
func TestHostImplementationsMatchTable(t *testing.T) {
	t.Parallel()
	if got := slices.Sorted(maps.Keys(readyHosts)); !slices.Equal(got, core.ReadyProviders()) {
		t.Errorf("ready implementations %v != table %v", got, core.ReadyProviders())
	}
	if got := slices.Sorted(maps.Keys(waitHosts)); !slices.Equal(got, core.PRWaitProviders()) {
		t.Errorf("pr-wait implementations %v != table %v", got, core.PRWaitProviders())
	}
}

// TestWaitRequestEnumMatchesTable pins the struct-tag enum (the one reflected
// into schemas and tool lists) to the table, since a tag cannot be computed.
func TestWaitRequestEnumMatchesTable(t *testing.T) {
	t.Parallel()
	field, ok := reflect.TypeFor[WaitRequest]().FieldByName("Provider")
	if !ok {
		t.Fatal("WaitRequest.Provider missing")
	}
	if got := strings.Split(field.Tag.Get("enum"), "|"); !slices.Equal(got, core.PRWaitProviders()) {
		t.Errorf("WaitRequest.Provider enum %v != table %v", got, core.PRWaitProviders())
	}
}
