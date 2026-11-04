package mcp

import (
	"reflect"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/core"
)

// TestPresenceFieldsCovered proves every Has* field on any MCP request type is
// transport-owned (hidden from the schema) and declared in the presence table
// of its flags struct. A new optional flag therefore edits its core flags type,
// never internal/mcp, and cannot go silent.
func TestPresenceFieldsCovered(t *testing.T) {
	t.Parallel()
	visited := map[reflect.Type]bool{}
	for _, tool := range allTools(false, true) {
		auditPresence(t, tool.name, tool.request, visited)
	}
}

// auditPresence walks one request type recursively, checking every Has* bool
// it finds against the owning presence table.
//
//nolint:revive // the audit is one branch per field shape
func auditPresence(t *testing.T, tool string, typ reflect.Type, visited map[reflect.Type]bool) {
	t.Helper()
	if visited[typ] {
		return
	}
	visited[typ] = true
	if typ.Kind() != reflect.Struct {
		return
	}
	for sf := range typ.Fields() {
		if sf.Type.Kind() == reflect.Struct {
			auditPresence(t, tool, sf.Type, visited)
		}
	}
	present := presenceFields(typ)
	if len(present) == 0 {
		return
	}
	deriver, ok := reflect.TypeAssert[core.PresenceDeriver](reflect.New(typ))
	if !ok {
		t.Errorf("%s: %v carries Has* fields but no presence table", tool, typ)
		return
	}
	table := presenceEntries(t, tool, typ, deriver)
	checkPresenceFields(t, tool, typ, reflect.ValueOf(deriver).Elem(), present, table)
	checkPresenceNames(t, tool, typ, reflect.ValueOf(deriver).Elem(), table)
}

// presenceFields lists the Has* bools of one struct type.
func presenceFields(typ reflect.Type) []reflect.StructField {
	var present []reflect.StructField
	for sf := range typ.Fields() {
		if strings.HasPrefix(sf.Name, "Has") && sf.Type.Kind() == reflect.Bool {
			present = append(present, sf)
		}
	}
	return present
}

// presenceEntries indexes a presence table by flag name.
func presenceEntries(t *testing.T, tool string, typ reflect.Type, deriver core.PresenceDeriver) map[string]*bool {
	t.Helper()
	table := map[string]*bool{}
	for _, entry := range deriver.Presences() {
		if entry.Name == "" || entry.Flag == nil {
			t.Errorf("%s: %v has a malformed presence entry %+v", tool, typ, entry)
			continue
		}
		table[entry.Name] = entry.Flag
	}
	return table
}

// checkPresenceFields proves each Has* field is hidden from the schema and
// that the table entry points at that exact field.
//
//nolint:revive // the field audit reads as one assertion block
func checkPresenceFields(t *testing.T, tool string, typ reflect.Type, value reflect.Value, present []reflect.StructField, table map[string]*bool) {
	t.Helper()
	for i := range present {
		sf := &present[i]
		if !skipField(sf, nil) {
			t.Errorf("%s: presence field %v.%s is exposed in the schema (tag it json:\"-\")", tool, typ, sf.Name)
		}
		flag, ok := table[sf.Name]
		if !ok {
			t.Errorf("%s: presence field %v.%s has no table entry", tool, typ, sf.Name)
			continue
		}
		field := value.FieldByName(sf.Name)
		if !field.IsValid() {
			t.Errorf("%s: presence entry %q names no field on %v", tool, sf.Name, typ)
			continue
		}
		addr, ok := reflect.TypeAssert[*bool](field.Addr())
		if !ok || flag != addr {
			t.Errorf("%s: presence entry %q does not point at %v.%s", tool, sf.Name, typ, sf.Name)
		}
	}
}

// checkPresenceNames proves every table entry names a real bool field.
func checkPresenceNames(t *testing.T, tool string, typ reflect.Type, value reflect.Value, table map[string]*bool) {
	t.Helper()
	for name := range table {
		field := value.FieldByName(name)
		if !field.IsValid() || field.Kind() != reflect.Bool {
			t.Errorf("%s: presence table names %q, not a bool field of %v", tool, name, typ)
		}
	}
}

// TestDerivePresences pins the derived semantics the MCP surface had before
// the table: a bit is set exactly when its value is non-empty.
//
//nolint:revive // the per-flags matrix reads as one assertion block
func TestDerivePresences(t *testing.T) {
	t.Parallel()
	t.Run("fetch", func(t *testing.T) {
		t.Parallel()
		empty := core.FetchFlags{}
		core.DerivePresences(&empty)
		if empty.HasBase || empty.HasProvider {
			t.Errorf("empty fetch flags derived %+v", empty)
		}
		full := core.FetchFlags{BaseSHA: "abc", Provider: "github"}
		core.DerivePresences(&full)
		if !full.HasBase || !full.HasProvider {
			t.Errorf("populated fetch flags derived %+v", full)
		}
		noProvider := core.FetchFlags{BaseSHA: "abc"}
		core.DerivePresences(&noProvider)
		if !noProvider.HasBase || noProvider.HasProvider {
			t.Errorf("empty provider derived %+v", noProvider)
		}
	})
	t.Run("fetch-local", func(t *testing.T) {
		t.Parallel()
		stashed := core.FetchLocalFlags{StashN: "2"}
		core.DerivePresences(&stashed)
		if !stashed.HasStash {
			t.Errorf("stash flag derived %+v", stashed)
		}
		empty := core.FetchLocalFlags{}
		core.DerivePresences(&empty)
		if empty.HasStash {
			t.Errorf("empty local flags derived %+v", empty)
		}
	})
}
