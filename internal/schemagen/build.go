package schemagen

import (
	"reflect"
	"strings"
)

// Schema keywords and JSON type names used across the generator.
const (
	keySchema               = "$schema"
	keyID                   = "$id"
	keyTitle                = "title"
	keyDescription          = "description"
	keyType                 = "type"
	keyProperties           = "properties"
	keyRequired             = "required"
	keyAdditionalProperties = "additionalProperties"
	keyItems                = "items"
	keyAnyOf                = "anyOf"
	keyRef                  = "$ref"
	keyDefs                 = "$defs"
	keyConst                = "const"
	keyEnum                 = "enum"
	keyMinimum              = "minimum"
	keyMaximum              = "maximum"
	keyFormat               = "format"
	keyPattern              = "pattern"
	typeObject              = "object"
	typeArray               = "array"
	typeString              = "string"
	typeInteger             = "integer"
	typeNumber              = "number"
	typeBoolean             = "boolean"
	typeNull                = "null"
	defsPrefix              = "#/$defs/"
	draft2020               = "https://json-schema.org/draft/2020-12/schema"
	idBase                  = "https://github.com/stainless-code/verdict/blob/main/schemas/"
	inlineTag               = "inline"
	inlineTrue              = "true"
)

// nodeKind is the JSON value kind of one schema node.
type nodeKind int

// The JSON value kinds a schema node can have.
const (
	kindObject nodeKind = iota
	kindArray
	kindString
	kindNumber
	kindBool
	kindNull
)

// member is one ordered JSON object member.
type member struct { //nolint:govet // key-then-value keeps every schema literal readable
	key   string
	value *node
}

// node is a minimal ordered JSON tree; ordering is what keeps the generated
// documents byte-stable and header-first.
type node struct {
	text       string
	members    []member
	elems      []*node
	kind       nodeKind
	boolean    bool
	forceMulti bool
}

// object builds an object node. A forced object always prints multiline;
// unforced leaf objects print inline when they fit.
func object(forceMulti bool, members ...member) *node {
	return &node{kind: kindObject, members: members, forceMulti: forceMulti}
}

// array builds an array node.
func array(elems ...*node) *node {
	return &node{kind: kindArray, elems: elems}
}

// stringNode builds a string node.
func stringNode(s string) *node { return &node{kind: kindString, text: s} }

// numberNode builds a number node from its JSON literal.
func numberNode(lit string) *node { return &node{kind: kindNumber, text: lit} }

// boolNode builds a boolean node.
func boolNode(b bool) *node { return &node{kind: kindBool, boolean: b} }

// nullNode builds a JSON null node.
func nullNode() *node { return &node{kind: kindNull} }

// anyNode builds the empty schema, which accepts any value.
func anyNode() *node { return object(false) }

// definition registers one named struct type under a $defs name.
type definition struct {
	typ  reflect.Type
	name string
}

// document is one published schema: its file stem, title, root shape and
// the named definitions it references.
type document struct {
	name                 string
	title                string
	root                 reflect.Type
	anyOf                []reflect.Type
	defs                 []definition
	additionalProperties bool
}

// interfaceType is the [reflect.Type] of any.
var interfaceType = reflect.TypeFor[any]()

// documents returns every published schema, in filename order.
func documents() []document {
	return []document{
		{
			name:  "corpus",
			title: "corpus.json — benchmark corpus case manifest",
			root:  reflect.TypeFor[CorpusCase](),
			defs: []definition{
				{typ: reflect.TypeFor[CorpusProvenance](), name: "provenance"},
				{typ: reflect.TypeFor[CorpusBug](), name: "bug"},
			},
			additionalProperties: false,
		},
		{
			name:  "cost",
			title: "cost.json — agent self-reported spend record",
			anyOf: []reflect.Type{reflect.TypeFor[CostRecord](), reflect.TypeFor[[]CostRecord]()},
			defs: []definition{
				{typ: reflect.TypeFor[CostRecord](), name: "record"},
				{typ: reflect.TypeFor[ModelSpend](), name: "modelSpend"},
			},
			additionalProperties: true,
		},
		{
			name:                 "envelope",
			title:                "verdict --json envelope",
			root:                 reflect.TypeFor[Envelope](),
			defs:                 envelopeDefs(),
			additionalProperties: false,
		},
		{
			name:  "findings",
			title: "findings.json — merged findings store",
			root:  reflect.TypeFor[[]FindingEntry](),
			defs: []definition{
				{typ: reflect.TypeFor[FindingEntry](), name: "entry"},
				{typ: reflect.TypeFor[HistoryEvent](), name: "historyEvent"},
				{typ: reflect.TypeFor[KBCredit](), name: "kbCredit"},
			},
			additionalProperties: true,
		},
		{
			name:                 "post",
			title:                "post.json — completed-post record (PR bundles)",
			root:                 reflect.TypeFor[Post](),
			additionalProperties: true,
		},
		{
			name:                 "record",
			title:                "record.json — completed-record (local bundles)",
			root:                 reflect.TypeFor[Record](),
			additionalProperties: true,
		},
		{
			name:                 "review",
			title:                "review.json — agent-written review input",
			root:                 reflect.TypeFor[Review](),
			defs:                 reviewDefs(),
			additionalProperties: true,
		},
	}
}

// envelopeDefs lists the envelope $defs in their published order.
func envelopeDefs() []definition {
	return []definition{
		{typ: reflect.TypeFor[SchemaError](), name: "error"},
		{typ: reflect.TypeFor[Candidate](), name: "candidate"},
		{typ: reflect.TypeFor[AnchorsSummary](), name: "anchorsSummary"},
		{typ: reflect.TypeFor[FindingsCounts](), name: "findingsCounts"},
		{typ: reflect.TypeFor[FindingItem](), name: "findingItem"},
		{typ: reflect.TypeFor[Provenance](), name: "provenance"},
		{typ: reflect.TypeFor[ReportData](), name: "reportData"},
		{typ: reflect.TypeFor[ReportHead](), name: "reportHead"},
		{typ: reflect.TypeFor[ReportVerdict](), name: "reportVerdict"},
		{typ: reflect.TypeFor[ReportCounts](), name: "reportCounts"},
		{typ: reflect.TypeFor[ReportPanel](), name: "reportPanel"},
		{typ: reflect.TypeFor[ReportFilters](), name: "reportFilters"},
		{typ: reflect.TypeFor[ReportCost](), name: "reportCost"},
		{typ: reflect.TypeFor[CostTiers](), name: "costTiers"},
		{typ: reflect.TypeFor[CostTier](), name: "costTier"},
		{typ: reflect.TypeFor[ReportCompleteness](), name: "reportCompleteness"},
		{typ: reflect.TypeFor[PostData](), name: "postData"},
		{typ: reflect.TypeFor[RecordData](), name: "recordData"},
		{typ: reflect.TypeFor[ShowData](), name: "showData"},
		{typ: reflect.TypeFor[ShowFix](), name: "showFix"},
		{typ: reflect.TypeFor[VerifyHint](), name: "verifyHint"},
		{typ: reflect.TypeFor[FetchEstimate](), name: "fetchEstimate"},
	}
}

// reviewDefs lists the review input's definitions.
func reviewDefs() []definition {
	return []definition{
		{typ: reflect.TypeFor[Comment](), name: "comment"},
		{typ: reflect.TypeFor[TriageClaim](), name: "triageClaim"},
		{typ: reflect.TypeFor[SectionTag](), name: "sectionTag"},
	}
}

// builder reflects the model types for one document.
type builder struct {
	defNames map[reflect.Type]string
}

// newBuilder indexes the document's definitions by Go type.
func newBuilder(defs []definition) *builder {
	b := &builder{defNames: make(map[reflect.Type]string, len(defs))}
	for _, def := range defs {
		b.defNames[def.typ] = def.name
	}
	return b
}

// documentNode builds the complete schema document for doc.
func (b *builder) documentNode(doc *document) (*node, error) {
	content, err := b.rootNode(doc)
	if err != nil {
		return nil, err
	}
	members := []member{
		{keySchema, stringNode(draft2020)},
		{keyID, stringNode(idBase + doc.name + ".schema.json")},
		{keyTitle, stringNode(doc.title)},
	}
	members = append(members, content.members...)
	if len(doc.defs) > 0 {
		defs, err := b.defsNode(doc.defs)
		if err != nil {
			return nil, err
		}
		members = append(members, member{keyDefs, defs})
	}
	return object(true, members...), nil
}

// rootNode builds the root schema keywords (type/properties/required or
// type/items or anyOf).
func (b *builder) rootNode(doc *document) (*node, error) {
	if len(doc.anyOf) > 0 {
		return b.anyOfRoot(doc.anyOf)
	}
	if doc.root.Kind() == reflect.Struct {
		return b.structNode(doc.root, doc.additionalProperties)
	}
	return b.typeNode(doc.root, "")
}

// anyOfRoot builds the root anyOf branches.
func (b *builder) anyOfRoot(types []reflect.Type) (*node, error) {
	branches := make([]*node, 0, len(types))
	for _, typ := range types {
		branch, err := b.typeNode(typ, "")
		if err != nil {
			return nil, err
		}
		branches = append(branches, branch)
	}
	return object(true, member{keyAnyOf, array(branches...)}), nil
}

// defsNode builds the $defs container in registration order.
func (b *builder) defsNode(defs []definition) (*node, error) {
	members := make([]member, 0, len(defs))
	for _, def := range defs {
		schema, err := b.structNode(def.typ, true)
		if err != nil {
			return nil, err
		}
		members = append(members, member{def.name, schema})
	}
	return object(true, members...), nil
}

// structNode builds an object schema from a struct type.
func (b *builder) structNode(typ reflect.Type, additional bool) (*node, error) {
	if typ.Kind() != reflect.Struct {
		return nil, errorf("%s is not a struct", typ)
	}
	props, required, err := b.fieldNodes(typ)
	if err != nil {
		return nil, err
	}
	return object(true,
		member{keyType, stringNode(typeObject)},
		member{keyProperties, props},
		member{keyRequired, required},
		member{keyAdditionalProperties, boolNode(additional)},
	), nil
}

// fieldNodes builds the properties object and required list for a struct.
func (b *builder) fieldNodes(typ reflect.Type) (*node, *node, error) {
	props := object(true)
	required := array()
	for field := range typ.Fields() {
		if field.PkgPath != "" {
			continue
		}
		if err := b.appendField(typ, &field, props, required); err != nil {
			return nil, nil, err
		}
	}
	return props, required, nil
}

// appendField adds one struct field to the properties and required nodes.
func (b *builder) appendField(parent reflect.Type, field *reflect.StructField, props, required *node) error {
	prop, isRequired, skip, err := b.fieldNode(parent, field)
	if err != nil {
		return err
	}
	if skip {
		return nil
	}
	props.members = append(props.members, prop)
	if isRequired {
		required.elems = append(required.elems, stringNode(prop.key))
	}
	return nil
}

// fieldNode builds one property schema and its requiredness.
func (b *builder) fieldNode(parent reflect.Type, field *reflect.StructField) (member, bool, bool, error) {
	name, optional, err := jsonFieldName(field)
	if err != nil {
		return member{}, false, false, err
	}
	if name == "" {
		return member{}, false, true, nil
	}
	schema, err := b.typeNode(field.Type, field.Tag)
	if err != nil {
		return member{}, false, false, wrapError(parent.String()+"."+field.Name, err)
	}
	return member{name, schema}, !optional, false, nil
}

// jsonFieldName reads the JSON name and omitempty marker from a field tag.
func jsonFieldName(field *reflect.StructField) (name string, optional bool, err error) {
	tag := field.Tag.Get("json")
	if tag == "-" {
		return "", false, nil
	}
	parts := strings.Split(tag, ",")
	if len(parts) == 0 || parts[0] == "" {
		return "", false, errorf("%s.%s has no json name", field.Type, field.Name)
	}
	name = parts[0]
	for _, opt := range parts[1:] {
		if opt == "omitempty" {
			optional = true
		}
	}
	if field.Tag.Get("optional") == inlineTrue {
		optional = true
	}
	return name, optional, nil
}

// typeNode builds the schema for one Go type plus its struct tag keywords.
func (b *builder) typeNode(typ reflect.Type, tag reflect.StructTag) (*node, error) {
	if schema, ok, err := b.taggedNode(typ, tag); ok || err != nil {
		return schema, err
	}
	return b.plainNode(typ, tag)
}

// taggedNode handles fields whose tag overrides the Go type entirely.
func (b *builder) taggedNode(typ reflect.Type, tag reflect.StructTag) (*node, bool, error) {
	if enum := tag.Get(keyEnum); enum != "" {
		return enumNode(enum), true, nil
	}
	if value := tag.Get(keyConst); value != "" {
		return object(false, member{keyConst, stringNode(value)}), true, nil
	}
	if override := tag.Get(keyType); override != "" {
		return typeListNode(override), true, nil
	}
	if union := tag.Get(keyAnyOf); union != "" {
		schema, err := b.anyOfNode(typ, union)
		return schema, true, err
	}
	if tag.Get(inlineTag) == inlineTrue {
		schema, err := b.inlineStructNode(typ)
		return schema, true, err
	}
	return nil, false, nil
}

// inlineStructNode inlines a named struct instead of referencing a $def.
func (b *builder) inlineStructNode(typ reflect.Type) (*node, error) {
	base := typ
	if base.Kind() == reflect.Pointer {
		base = base.Elem()
	}
	return b.structNode(base, true)
}

// anyOfNode builds the null-or-$ref union used by the envelope error field.
func (b *builder) anyOfNode(typ reflect.Type, union string) (*node, error) {
	if union != typeNull {
		return nil, errorf("unsupported anyOf tag %q", union)
	}
	base := typ
	if base.Kind() == reflect.Pointer {
		base = base.Elem()
	}
	if base.Kind() != reflect.Struct {
		return nil, errorf("anyOf=null wants a struct, got %s", typ)
	}
	ref, err := b.refNode(base)
	if err != nil {
		return nil, err
	}
	return object(false, member{keyAnyOf, array(object(false, member{keyType, stringNode(typeNull)}), ref)}), nil
}

// plainNode handles fields with no type-overriding tag.
func (b *builder) plainNode(typ reflect.Type, tag reflect.StructTag) (*node, error) {
	base := typ
	nullable := false
	if base.Kind() == reflect.Pointer {
		base = base.Elem()
		nullable = true
	}
	schema, err := b.baseNode(base)
	if err != nil {
		return nil, err
	}
	if nullable {
		schema, err = nullableNode(schema, base)
		if err != nil {
			return nil, err
		}
	}
	appendKeywords(schema, tag)
	return schema, nil
}

// baseNode maps one non-pointer Go type to its schema node.
func (b *builder) baseNode(typ reflect.Type) (*node, error) {
	switch typ.Kind() {
	case reflect.Interface:
		if typ.NumMethod() != 0 {
			return nil, errorf("%s is not an empty interface", typ)
		}
		return anyNode(), nil
	case reflect.String:
		return object(false, member{keyType, stringNode(typeString)}), nil
	case reflect.Bool:
		return object(false, member{keyType, stringNode(typeBoolean)}), nil
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return object(false, member{keyType, stringNode(typeInteger)}), nil
	case reflect.Float32, reflect.Float64:
		return object(false, member{keyType, stringNode(typeNumber)}), nil
	case reflect.Slice:
		return b.sliceNode(typ)
	case reflect.Map:
		return b.mapNode(typ)
	case reflect.Struct:
		if typ.Name() == "" {
			return b.structNode(typ, true)
		}
		return b.refNode(typ)
	default:
		return nil, errorf("unsupported Go type %s", typ)
	}
}

// sliceNode builds an array schema; []any carries no items.
func (b *builder) sliceNode(typ reflect.Type) (*node, error) {
	elem := typ.Elem()
	if elem == interfaceType {
		return object(true, member{keyType, stringNode(typeArray)}), nil
	}
	items, err := b.typeNode(elem, "")
	if err != nil {
		return nil, err
	}
	return object(true, member{keyType, stringNode(typeArray)}, member{keyItems, items}), nil
}

// mapNode builds an open object schema for map[string]any.
func (b *builder) mapNode(typ reflect.Type) (*node, error) {
	if typ.Key().Kind() != reflect.String {
		return nil, errorf("map key must be a string, got %s", typ.Key())
	}
	return object(false,
		member{keyType, stringNode(typeObject)},
		member{keyAdditionalProperties, boolNode(true)},
	), nil
}

// refNode builds a $ref to a registered definition.
func (b *builder) refNode(typ reflect.Type) (*node, error) {
	name, ok := b.defNames[typ]
	if !ok {
		return nil, errorf("%s has no $defs registration", typ)
	}
	return object(false, member{keyRef, stringNode(defsPrefix + name)}), nil
}

// nullableNode widens a scalar/array/object type with JSON null.
func nullableNode(schema *node, base reflect.Type) (*node, error) {
	if base.Kind() == reflect.Struct {
		return nil, errorf("nullable structs need an explicit anyOf tag")
	}
	for i, m := range schema.members {
		if m.key != keyType {
			continue
		}
		switch m.value.kind {
		case kindString:
			schema.members[i].value = array(stringNode(m.value.text), stringNode(typeNull))
		case kindArray:
			m.value.elems = append(m.value.elems, stringNode(typeNull))
		case kindObject, kindNumber, kindBool, kindNull:
			return nil, errorf("%s has a non-widenable type node", base)
		}
		return schema, nil
	}
	return nil, errorf("%s has no type to widen", base)
}

// typeListNode turns "integer|string|null" into a type array.
func typeListNode(spec string) *node {
	parts := strings.Split(spec, "|")
	elems := make([]*node, 0, len(parts))
	for _, part := range parts {
		elems = append(elems, stringNode(part))
	}
	return object(false, member{keyType, array(elems...)})
}

// enumNode turns "a|b|null" into an enum keyword.
func enumNode(spec string) *node {
	parts := strings.Split(spec, "|")
	elems := make([]*node, 0, len(parts))
	for _, part := range parts {
		if part == typeNull {
			elems = append(elems, nullNode())
			continue
		}
		elems = append(elems, stringNode(part))
	}
	return object(false, member{keyEnum, array(elems...)})
}

// appendKeywords adds min/max/format keywords to a leaf schema node.
func appendKeywords(schema *node, tag reflect.StructTag) {
	if value := tag.Get("min"); value != "" {
		schema.members = append(schema.members, member{keyMinimum, numberNode(value)})
	}
	if value := tag.Get("max"); value != "" {
		schema.members = append(schema.members, member{keyMaximum, numberNode(value)})
	}
	if value := tag.Get(keyFormat); value != "" {
		schema.members = append(schema.members, member{keyFormat, stringNode(value)})
	}
}
