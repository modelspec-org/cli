package modelspec

import (
	"fmt"
	"regexp"
	"strings"
)

// ReservedNames are the five kind tokens that no concept may be named
// (ModelSpec decision 0015).
var ReservedNames = map[string]bool{
	"entities": true, "components": true, "enums": true, "collections": true, "recordsets": true,
}

// PrimitiveTypes is the type vocabulary of ModelSpec v0 (spec/core-model.md).
var PrimitiveTypes = map[string]bool{
	"string": true, "int": true, "float": true, "bool": true, "decimal": true, "uuid": true,
	"date": true, "time": true, "datetime": true, "document": true, "json": true, "any": true,
}

var nonNegativeInt = regexp.MustCompile(`^[0-9]+$`)

// attribute kinds a value must have
type valueKind int

const (
	vString valueKind = iota
	vBool
	vCount
	vStringList
	vEnum // a name or a list of values
)

var constraintAttrs = map[string]valueKind{
	"required": vBool, "unique": vBool, "min_len": vCount, "max_len": vCount,
	"pattern": vString, "format": vString,
}

var conceptAttrs = map[Kind]map[string]valueKind{
	KindEntity:     {"key": vStringList, "use": vStringList},
	KindComponent:  {},
	KindEnum:       {"values": vStringList},
	KindCollection: {"kind": vString, "source": vString, "query": vString},
	KindRecordset:  {"key": vStringList, "query": vString},
}

// memberAttrs lists the attributes a member of the kind may carry.
func memberAttrs(k Kind) map[string]valueKind {
	attrs := map[string]valueKind{"type": vString, "enum": vEnum}
	switch k {
	case KindRecordset:
		attrs["bind"], attrs["source"] = vString, vString
		return attrs
	case KindCollection:
		attrs["bind"] = vString
	}
	attrs["entity"], attrs["component"] = vString, vString
	for n, v := range constraintAttrs {
		attrs[n] = v
	}
	return attrs
}

// Check applies the semantic rules to a set of models and returns the sorted
// findings. Models are checked together because a module-qualified reference
// (decision 0014) names another module, which is resolved among the models given
// by the module short name (Model.Name). Broken models are skipped, and
// references into their modules are not reported.
func Check(models []*Model) []Finding {
	c := &checker{byName: map[string][]*Model{}, broken: map[string]bool{}}
	for _, m := range models {
		if m.Broken {
			c.broken[m.Name] = true
			continue
		}
		c.byName[m.Name] = append(c.byName[m.Name], m)
	}
	for _, m := range models {
		if !m.Broken {
			c.model(m)
		}
	}
	SortFindings(c.findings)
	return c.findings
}

type checker struct {
	byName   map[string][]*Model
	broken   map[string]bool
	findings []Finding
	cur      *Model
}

func (c *checker) add(line int, rule string, sev Severity, format string, args ...any) {
	c.findings = append(c.findings, Finding{File: c.cur.File, Line: line, Rule: rule, Severity: sev, Message: fmt.Sprintf(format, args...)})
}

func (c *checker) errorf(line int, rule, format string, args ...any) {
	c.add(line, rule, SeverityError, format, args...)
}

func conceptScope(k Kind) string {
	switch k {
	case KindEntity, KindComponent, KindEnum:
		return "entity/component/enum"
	default:
		return string(k)
	}
}

func (c *checker) model(m *Model) {
	c.cur = m
	seen := map[string]*Concept{}
	for _, k := range m.Concepts {
		c.names(k)
		scope := conceptScope(k.Kind) + "\x00" + k.Name
		if prev, dup := seen[scope]; dup {
			c.errorf(k.Line, RuleDuplicate, "duplicate concept name %q in the %s scope (also declared at line %d)", k.Name, conceptScope(k.Kind), prev.Line)
			continue
		}
		seen[scope] = k
	}
	for _, k := range m.Concepts {
		c.attrs(string(k.Kind)+" "+quote1(k.Name), k.Attrs, conceptAttrs[k.Kind])
		switch k.Kind {
		case KindEntity:
			c.entity(m, k)
		case KindComponent:
			c.members(m, k)
		case KindEnum:
			c.enum(k)
		case KindCollection:
			c.collection(m, k)
		default:
			c.recordset(m, k)
		}
	}
}

func quote1(s string) string { return fmt.Sprintf("%q", s) }

// names checks the form of a concept's name and its members' names.
func (c *checker) names(k *Concept) {
	if ReservedNames[k.Name] {
		c.errorf(k.Line, RuleReserved, "%s name %q is a reserved kind token (entities, components, enums, collections, recordsets) and cannot name a concept", k.Kind, k.Name)
	} else {
		c.nameForm(k.Line, string(k.Kind), k.Name)
	}
	if k.Kind == KindRecordset {
		return // column names are display names: any text, repeats allowed (decision 0007)
	}
	for _, mem := range k.Members {
		c.nameForm(mem.Line, memberWord(k.Kind), mem.Name)
	}
}

func (c *checker) nameForm(line int, what, name string) {
	switch {
	case strings.Contains(name, "."):
		c.errorf(line, RuleNameForm, "%s name %q must not contain a dot (decision 0014: a dot marks a module-qualified reference)", what, name)
	case !identifier.MatchString(name):
		c.errorf(line, RuleNameForm, "%s name %q must be an identifier (letters, digits and _, not starting with a digit)", what, name)
	}
}

// attrs checks that every attribute is supported and has a value of the right
// type.
func (c *checker) attrs(who string, attrs []Attr, allowed map[string]valueKind) {
	for _, a := range attrs {
		kind, ok := allowed[a.Name]
		if !ok {
			c.errorf(a.Line, RuleAttribute, "%s has unsupported attribute %q", who, a.Name)
			continue
		}
		if msg := valueProblem(kind, a.Value); msg != "" {
			c.errorf(a.Line, RuleAttribute, "%s: %q %s", who, a.Name, msg)
		}
	}
}

func valueProblem(kind valueKind, v *Node) string {
	switch kind {
	case vString:
		if v.Type != NodeString {
			return "must be a string, not " + v.typeName()
		}
	case vBool:
		if v.Type != NodeBool {
			return "must be true or false, not " + v.typeName()
		}
	case vCount:
		if v.Type != NodeNumber || !nonNegativeInt.MatchString(v.Str) {
			return "must be a non-negative integer"
		}
	case vStringList:
		if _, ok := v.stringList(); !ok {
			return "must be a list of strings"
		}
	default: // vEnum
		if v.Type != NodeString {
			if _, ok := v.stringList(); !ok {
				return "must be the name of an enum or a list of string values"
			}
		}
	}
	return ""
}

// ---- enums

func (c *checker) enum(k *Concept) {
	a, ok := k.Attr("values")
	if !ok {
		c.errorf(k.Line, RuleEnumValues, "enum %q must declare at least one value", k.Name)
		return
	}
	vals, ok := a.Value.stringList()
	if !ok {
		return // reported as an attribute type problem
	}
	c.valueList(a.Line, fmt.Sprintf("enum %q", k.Name), vals)
}

// valueList checks that a list of enum values is non-empty and has no repeats.
func (c *checker) valueList(line int, who string, vals []string) {
	if len(vals) == 0 {
		c.errorf(line, RuleEnumValues, "%s must declare at least one value", who)
	}
	seen := map[string]bool{}
	for _, v := range vals {
		if seen[v] {
			c.errorf(line, RuleEnumValues, "%s has duplicate value %q", who, v)
		}
		seen[v] = true
	}
}

// ---- entities, components and their members

func (c *checker) entity(m *Model, k *Concept) {
	c.members(m, k)
	who := fmt.Sprintf("entity %q", k.Name)
	if use, ok := k.Attr("use"); ok {
		if names, ok := use.Value.stringList(); ok {
			for _, n := range names {
				c.ref(m, use.Line, who+" use", n, KindComponent)
			}
		}
	}
	key, ok := k.Attr("key")
	if !ok {
		c.errorf(k.Line, RuleKey, "%s has no key (a list of the properties that identify a record)", who)
		return
	}
	names, ok := key.Value.stringList()
	if !ok {
		return // reported as an attribute type problem
	}
	if len(names) == 0 {
		c.errorf(key.Line, RuleKey, "%s has an empty key", who)
	}
	known := c.propertyNames(m, k)
	for _, n := range names {
		if !known[n] {
			c.errorf(key.Line, RuleKey, "%s key %q is not a property of the entity", who, n)
		}
	}
}

// propertyNames are the entity's own properties plus the fields of the
// same-module components it uses.
func (c *checker) propertyNames(m *Model, k *Concept) map[string]bool {
	known := map[string]bool{}
	for _, mem := range k.Members {
		known[mem.Name] = true
	}
	if use, ok := k.Attr("use"); ok {
		if names, ok := use.Value.stringList(); ok {
			for _, n := range names {
				for _, comp := range m.Concepts {
					if comp.Kind == KindComponent && comp.Name == n {
						for _, f := range comp.Members {
							known[f.Name] = true
						}
					}
				}
			}
		}
	}
	return known
}

// members checks the properties of an entity or the fields of a component.
func (c *checker) members(m *Model, k *Concept) {
	allowed := memberAttrs(k.Kind)
	seen := map[string]int{}
	for _, mem := range k.Members {
		who := fmt.Sprintf("%s %q %s %q", k.Kind, k.Name, memberWord(k.Kind), mem.Name)
		if prev, dup := seen[mem.Name]; dup {
			c.errorf(mem.Line, RuleDuplicate, "duplicate %s %q in %s %q (also declared at line %d)", memberWord(k.Kind), mem.Name, k.Kind, k.Name, prev)
		} else {
			seen[mem.Name] = mem.Line
		}
		c.attrs(who, mem.Attrs, allowed)
		if _, ok := mem.Attr("component"); ok && k.Kind == KindEntity {
			c.add(mem.Line, RuleConsumerGap, SeverityWarning, "%s uses a component; readers that accept only scalar and entity-reference properties (the Directory's reader, for one) refuse this model", who)
		}
		c.memberKind(who, mem)
		c.memberRefs(m, who, mem)
		if a, ok := mem.Attr("bind"); ok && k.Kind == KindCollection && a.Value.Type == NodeString {
			c.bind(m, a, who)
		}
	}
}

// memberKind requires exactly one of type, entity and component.
func (c *checker) memberKind(who string, mem Member) {
	n := 0
	for _, name := range []string{"type", "entity", "component"} {
		if _, ok := mem.Attr(name); ok {
			n++
		}
	}
	if n != 1 {
		c.errorf(mem.Line, RuleMemberKind, "%s must have exactly one of type, entity or component", who)
	}
}

// memberRefs checks a member's type, references and enum constraint.
func (c *checker) memberRefs(m *Model, who string, mem Member) {
	if a, ok := mem.Attr("type"); ok && a.Value.Type == NodeString {
		c.typeName(a, who)
	}
	for _, r := range []struct {
		attr string
		kind Kind
	}{{"entity", KindEntity}, {"component", KindComponent}} {
		if a, ok := mem.Attr(r.attr); ok && a.Value.Type == NodeString {
			c.ref(m, a.Line, who+" "+r.attr, a.Value.Str, r.kind)
		}
	}
	if a, ok := mem.Attr("enum"); ok {
		if a.Value.Type == NodeString {
			c.ref(m, a.Line, who+" enum", a.Value.Str, KindEnum)
		} else if vals, ok := a.Value.stringList(); ok {
			c.valueList(a.Line, who+" enum", vals)
		}
	}
}

func (c *checker) typeName(a *Attr, who string) {
	if !PrimitiveTypes[a.Value.Str] {
		c.errorf(a.Line, RuleType, "%s has type %q, which is not a ModelSpec type (string, int, float, bool, decimal, uuid, date, time, datetime, document, json, any)", who, a.Value.Str)
	}
}

// ---- references

// lookup resolves a reference to the model that holds it. found is the model
// the name points into, nil when the problem is non-empty or the module is
// broken.
func (c *checker) lookup(m *Model, ref string) (target *Model, name, problem string) {
	module, name, qualified := strings.Cut(ref, ".")
	if !qualified {
		return m, ref, ""
	}
	if strings.Contains(name, ".") || module == "" || name == "" {
		return nil, "", fmt.Sprintf("%q is not a bare name or a <module>.<Name> reference", ref)
	}
	targets := c.byName[module]
	switch {
	case module == m.Name:
		// A model always resolves its own module name to itself, even when a
		// twin file (the same model in the other form) shares the name.
		return m, name, ""
	case len(targets) == 1:
		return targets[0], name, ""
	case len(targets) > 1:
		return nil, "", fmt.Sprintf("module %q is ambiguous (declared by %s)", module, fileList(targets))
	case c.broken[module]:
		return nil, "", "" // reported as a syntax problem in that module's file
	default:
		return nil, "", fmt.Sprintf("unknown module %q (modules are matched by the file name without %s, or module.name in JSON, among the files checked together)", module, HCLSuffix)
	}
}

func fileList(ms []*Model) string {
	files := make([]string, len(ms))
	for i, m := range ms {
		files[i] = m.File
	}
	return strings.Join(files, ", ")
}

func withArticle(kind Kind) string {
	if strings.ContainsRune("aeiou", rune(kind[0])) {
		return "an " + string(kind)
	}
	return "a " + string(kind)
}

// ref reports a reference that does not resolve to a concept of the kind.
func (c *checker) ref(m *Model, line int, what, ref string, kind Kind) {
	target, name, problem := c.lookup(m, ref)
	if problem != "" {
		c.errorf(line, RuleReference, "%s reference %q: %s", what, ref, problem)
		return
	}
	if target == nil || target.HasConcept(kind, name) {
		return
	}
	if actual, ok := trioKind(target, name); ok {
		c.errorf(line, RuleReference, "%s reference %q: %q is %s, not %s", what, ref, name, withArticle(actual), withArticle(kind))
		return
	}
	if qualified := strings.Contains(ref, "."); qualified {
		c.errorf(line, RuleReference, "%s reference %q: unknown %s %q in module %q", what, ref, kind, name, target.Name)
		return
	}
	c.errorf(line, RuleReference, "%s reference %q does not resolve to %s in module %q", what, ref, withArticle(kind), target.Name)
}

// trioKind returns the kind of the entity, component or enum with the name.
func trioKind(m *Model, name string) (Kind, bool) {
	for _, k := range m.Concepts {
		if k.Name == name && (k.Kind == KindEntity || k.Kind == KindComponent || k.Kind == KindEnum) {
			return k.Kind, true
		}
	}
	return "", false
}

// bind checks a bind value, <Entity>.<property> or <module>.<Entity>.<property>.
func (c *checker) bind(m *Model, a *Attr, who string) {
	parts := strings.Split(a.Value.Str, ".")
	if len(parts) != 2 && len(parts) != 3 {
		c.errorf(a.Line, RuleReference, "%s bind %q must be <Entity>.<property> or <module>.<Entity>.<property>", who, a.Value.Str)
		return
	}
	prop := parts[len(parts)-1]
	entityRef := strings.Join(parts[:len(parts)-1], ".")
	target, name, problem := c.lookup(m, entityRef)
	if problem != "" {
		c.errorf(a.Line, RuleReference, "%s bind %q: %s", who, a.Value.Str, problem)
		return
	}
	if target == nil {
		return
	}
	for _, k := range target.Concepts {
		if k.Kind == KindEntity && k.Name == name {
			if !c.propertyNames(target, k)[prop] {
				c.errorf(a.Line, RuleReference, "%s bind %q: entity %q has no property %q", who, a.Value.Str, name, prop)
			}
			return
		}
	}
	c.errorf(a.Line, RuleReference, "%s bind %q: no entity %q in module %q", who, a.Value.Str, name, target.Name)
}

// ---- collections and recordsets

func (c *checker) collection(m *Model, k *Concept) {
	c.members(m, k)
	who := fmt.Sprintf("collection %q", k.Name)
	kind, hasKind := k.Attr("kind")
	if hasKind && kind.Value.Type == NodeString {
		switch kind.Value.Str {
		case "editable":
		case "computed":
			if _, ok := k.Attr("query"); !ok {
				c.add(kind.Line, RuleCollection, SeverityWarning, "%s is computed but carries no query (computed collections should)", who)
			}
		default:
			c.errorf(kind.Line, RuleCollection, "%s kind is %q; it must be editable or computed", who, kind.Value.Str)
		}
	} else if !hasKind {
		c.errorf(k.Line, RuleCollection, "%s has no kind; it must be editable or computed", who)
	}
	if src, ok := k.Attr("source"); ok && src.Value.Type == NodeString {
		c.ref(m, src.Line, who+" source", src.Value.Str, KindEntity)
	}
}

func (c *checker) recordset(m *Model, k *Concept) {
	allowed := memberAttrs(KindRecordset)
	for _, col := range k.Members {
		who := fmt.Sprintf("recordset %q column %q", k.Name, col.Name)
		c.attrs(who, col.Attrs, allowed)
		if a, ok := col.Attr("type"); ok && a.Value.Type == NodeString {
			c.typeName(a, who)
		}
		if a, ok := col.Attr("bind"); ok && a.Value.Type == NodeString {
			c.bind(m, a, who)
		}
	}
	if key, ok := k.Attr("key"); ok {
		if names, ok := key.Value.stringList(); ok {
			for _, n := range names {
				if !hasMember(k, n) {
					c.errorf(key.Line, RuleKey, "recordset %q key %q is not one of its columns", k.Name, n)
				}
			}
		}
	}
}

func hasMember(k *Concept, name string) bool {
	for _, mem := range k.Members {
		if mem.Name == name {
			return true
		}
	}
	return false
}
