package schemagen

import (
	"reflect"
	"strings"
)

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
	idBase                  = "https://github.com/stainless-code/verdict/blob/main/skills/verdict/schemas/"
	inlineTag               = "inline"
	inlineTrue              = "true"
	defsBrief               = "brief"
	defsBriefSite           = "briefSite"
)

type nodeKind int

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

func array(elems ...*node) *node {
	return &node{kind: kindArray, elems: elems}
}

func stringNode(s string) *node { return &node{kind: kindString, text: s} }

func numberNode(lit string) *node { return &node{kind: kindNumber, text: lit} }

func boolNode(b bool) *node { return &node{kind: kindBool, boolean: b} }

func nullNode() *node { return &node{kind: kindNull} }

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

var interfaceType = reflect.TypeFor[any]()

const defsRecord = "record"

// documents returns every published schema, in filename order.
func documents() []document {
	return []document{
		{
			name:  "controls",
			title: "controls.json — resolved review controls snapshot",
			root:  reflect.TypeFor[ControlsFile](),
			defs: []definition{
				{typ: reflect.TypeFor[ControlsFileEntry](), name: "file"},
			},
			additionalProperties: false,
		},
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
				{typ: reflect.TypeFor[CostRecord](), name: defsRecord},
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
				{typ: reflect.TypeFor[Brief](), name: defsBrief},
				{typ: reflect.TypeFor[BriefSite](), name: defsBriefSite},
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
		{
			name:  "threads",
			title: "threads.json — normalized provider review threads",
			root:  reflect.TypeFor[ThreadsFile](),
			defs: []definition{
				{typ: reflect.TypeFor[Thread](), name: "thread"},
				{typ: reflect.TypeFor[ThreadComment](), name: "comment"},
			},
			additionalProperties: false,
		},
		{
			name:  "usage",
			title: "usage.json — explicitly handed spend record",
			anyOf: []reflect.Type{reflect.TypeFor[Usage](), reflect.TypeFor[[]Usage]()},
			defs: []definition{
				{typ: reflect.TypeFor[Usage](), name: defsRecord},
				{typ: reflect.TypeFor[UsageTokens](), name: "usageTokens"},
			},
			additionalProperties: true,
		},
	}
}

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
		{typ: reflect.TypeFor[ReportGate](), name: "reportGate"},
		{typ: reflect.TypeFor[GateDimension](), name: "gateDimension"},
		{typ: reflect.TypeFor[PostData](), name: "postData"},
		{typ: reflect.TypeFor[RecordData](), name: "recordData"},
		{typ: reflect.TypeFor[ShowData](), name: "showData"},
		{typ: reflect.TypeFor[ShowFix](), name: "showFix"},
		{typ: reflect.TypeFor[Brief](), name: defsBrief},
		{typ: reflect.TypeFor[BriefSite](), name: defsBriefSite},
		{typ: reflect.TypeFor[VerifyHint](), name: "verifyHint"},
		{typ: reflect.TypeFor[FetchEstimate](), name: "fetchEstimate"},
		{typ: reflect.TypeFor[TriageData](), name: "triageData"},
		{typ: reflect.TypeFor[ReadyData](), name: "readyData"},
		{typ: reflect.TypeFor[ReadyBlocker](), name: "readyBlocker"},
		{typ: reflect.TypeFor[ReadyCI](), name: "readyCI"},
		{typ: reflect.TypeFor[AdoptData](), name: "adoptData"},
		{typ: reflect.TypeFor[NextData](), name: "nextData"},
		{typ: reflect.TypeFor[FixData](), name: "fixData"},
		{typ: reflect.TypeFor[FixAnchors](), name: "fixAnchors"},
		{typ: reflect.TypeFor[LearnData](), name: "learnData"},
		{typ: reflect.TypeFor[InitData](), name: "initData"},
		{typ: reflect.TypeFor[DoctorData](), name: "doctorData"},
		{typ: reflect.TypeFor[DoctorCheck](), name: "doctorCheck"},
		{typ: reflect.TypeFor[CheckTreeData](), name: "checkTreeData"},
		{typ: reflect.TypeFor[ThreadsData](), name: "threadsData"},
		{typ: reflect.TypeFor[VerifyData](), name: "verifyData"},
		{typ: reflect.TypeFor[TrendData](), name: "trendData"},
		{typ: reflect.TypeFor[TrendStatuses](), name: "trendStatuses"},
		{typ: reflect.TypeFor[TrendTiers](), name: "trendTiers"},
		{typ: reflect.TypeFor[TrendProvenance](), name: "trendProvenance"},
		{typ: reflect.TypeFor[TrendPeriod](), name: "trendPeriod"},
		{typ: reflect.TypeFor[TrendBundleRow](), name: "trendBundleRow"},
		{typ: reflect.TypeFor[CalibrateData](), name: "calibrateData"},
		{typ: reflect.TypeFor[CalibrateHead](), name: "calibrateHead"},
		{typ: reflect.TypeFor[CalibratePR](), name: "calibratePR"},
		{typ: reflect.TypeFor[ProvenanceTriple](), name: "provenanceTriple"},
		{typ: reflect.TypeFor[CalibrateCounts](), name: "calibrateCounts"},
		{typ: reflect.TypeFor[CalibrateJudgment](), name: "calibrateJudgment"},
		{typ: reflect.TypeFor[FetchData](), name: "fetchData"},
		{typ: reflect.TypeFor[FetchLocalData](), name: "fetchLocalData"},
		{typ: reflect.TypeFor[FetchPanel](), name: "fetchPanel"},
	}
}

func reviewDefs() []definition {
	return []definition{
		{typ: reflect.TypeFor[Comment](), name: "comment"},
		{typ: reflect.TypeFor[Brief](), name: defsBrief},
		{typ: reflect.TypeFor[BriefSite](), name: defsBriefSite},
		{typ: reflect.TypeFor[TriageClaim](), name: "triageClaim"},
		{typ: reflect.TypeFor[SectionTag](), name: "sectionTag"},
	}
}

type builder struct {
	defNames map[reflect.Type]string
}

func newBuilder(defs []definition) *builder {
	b := &builder{defNames: make(map[reflect.Type]string, len(defs))}
	for _, def := range defs {
		b.defNames[def.typ] = def.name
	}
	return b
}

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

func (b *builder) rootNode(doc *document) (*node, error) {
	if len(doc.anyOf) > 0 {
		return b.anyOfRoot(doc.anyOf)
	}
	if doc.root.Kind() == reflect.Struct {
		return b.structNode(doc.root, doc.additionalProperties)
	}
	return b.typeNode(doc.root, "")
}

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

func (b *builder) typeNode(typ reflect.Type, tag reflect.StructTag) (*node, error) {
	if schema, ok, err := b.taggedNode(typ, tag); ok || err != nil {
		return schema, err
	}
	return b.plainNode(typ, tag)
}

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

func (b *builder) inlineStructNode(typ reflect.Type) (*node, error) {
	base := typ
	if base.Kind() == reflect.Pointer {
		base = base.Elem()
	}
	return b.structNode(base, true)
}

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

func (b *builder) mapNode(typ reflect.Type) (*node, error) {
	if typ.Key().Kind() != reflect.String {
		return nil, errorf("map key must be a string, got %s", typ.Key())
	}
	return object(false,
		member{keyType, stringNode(typeObject)},
		member{keyAdditionalProperties, boolNode(true)},
	), nil
}

func (b *builder) refNode(typ reflect.Type) (*node, error) {
	name, ok := b.defNames[typ]
	if !ok {
		return nil, errorf("%s has no $defs registration", typ)
	}
	return object(false, member{keyRef, stringNode(defsPrefix + name)}), nil
}

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

func typeListNode(spec string) *node {
	parts := strings.Split(spec, "|")
	elems := make([]*node, 0, len(parts))
	for _, part := range parts {
		elems = append(elems, stringNode(part))
	}
	return object(false, member{keyType, array(elems...)})
}

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
