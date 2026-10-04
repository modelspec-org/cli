package modelspec

import (
	"fmt"
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

// valueKind is the kind of value an attribute must have.
type valueKind int

const (
	vString valueKind = iota
	vBool
	vCount
	vStringList
	vEnum       // an enum name, or a list of enum values
	vEnumValues // a list of enum values
)

// constraintAttrs are the constraints of spec/core-model.md ("Constraints") that
// take a value of their own; `enum` is handled with the reference attributes.
var constraintAttrs = map[string]valueKind{
	"required": vBool, "unique": vBool, "min_len": vCount, "max_len": vCount,
	"pattern": vString, "format": vString,
}

var conceptAttrs = map[Kind]map[string]valueKind{
	KindEntity:     {"key": vStringList, "use": vStringList},
	KindComponent:  {},
	KindEnum:       {"values": vEnumValues},
	KindCollection: {"kind": vString, "source": vString, "query": vString},
	KindRecordset:  {"key": vStringList, "query": vString},
}

// memberAttrs lists the attributes a member of the kind may carry: a type or a
// reference, the constraints, and for collection fields and recordset columns
// the `bind` and `source` mappings.
func memberAttrs(k Kind) map[string]valueKind {
	attrs := map[string]valueKind{"type": vString, "enum": vEnum}
	for n, v := range constraintAttrs {
		attrs[n] = v
	}
	switch k {
	case KindRecordset:
		attrs["bind"], attrs["source"] = vString, vString
	case KindCollection:
		attrs["bind"] = vString
		attrs["entity"], attrs["component"] = vString, vString
	default:
		attrs["entity"], attrs["component"] = vString, vString
	}
	return attrs
}

// unit is one module as Check sees it: the models of one Group.
type unit struct {
	name    string
	aliases map[string]bool // other names the module's own references may use: a twin's module.name
	models  []*Model
	broken  bool // some file of the module could not be read
	idx     *unitIndex
	visits  int // concepts visited building the index: see steps
}

// unitIndex finds the concepts of a unit by kind and name, and the kind of a
// name in the entity/component/enum scope, in one pass over the unit. Looking a
// concept up used to scan every concept of every file, once for each reference.
type unitIndex struct {
	byKind map[Kind]map[string]*Concept
	trio   map[string]Kind
}

// index builds the unit's index on first use. The first declaration of a name
// wins, as the scan it replaced found the first.
func (u *unit) index() *unitIndex {
	if u.idx != nil {
		return u.idx
	}
	idx := &unitIndex{byKind: map[Kind]map[string]*Concept{}, trio: map[string]Kind{}}
	for _, m := range u.models {
		for _, c := range m.Concepts {
			u.visits++
			if idx.byKind[c.Kind] == nil {
				idx.byKind[c.Kind] = map[string]*Concept{}
			}
			if _, seen := idx.byKind[c.Kind][c.Name]; !seen {
				idx.byKind[c.Kind][c.Name] = c
			}
			if c.Kind == KindEntity || c.Kind == KindComponent || c.Kind == KindEnum {
				if _, seen := idx.trio[c.Name]; !seen {
					idx.trio[c.Name] = c.Kind
				}
			}
		}
	}
	u.idx = idx
	return idx
}

func (u *unit) find(kind Kind, name string) *Concept {
	return u.index().byKind[kind][name]
}

// trioKind returns the kind of the entity, component or enum with the name.
func (u *unit) trioKind(name string) (Kind, bool) {
	kind, ok := u.index().trio[name]
	return kind, ok
}

// Check applies the semantic rules of the profile in opts to the models and
// returns the sorted findings.
//
// Models are grouped into modules by Model.Group (Load sets it from the module
// rules): concept names are unique per module across all its files, and a
// reference resolves against the whole module. A module-qualified reference
// (decision 0014) names another module and resolves among the models given, by
// Model.Name. A Twin (the JSON copy of an HCL module) is checked as a module of
// its own but is not a second source for the name. Broken models are skipped, and
// references into their modules are not reported.
func Check(models []*Model, opts Options) []Finding {
	return runCheck(models, opts).result()
}

// runCheck does the work of Check and returns the checker, whose steps a test
// can read.
func runCheck(models []*Model, opts Options) *checker {
	c := &checker{opts: opts, byName: map[string][]*unit{}, props: map[*Concept]*propSet{}, memberSets: map[*Concept]map[string]bool{}}
	groups := map[string]*unit{}
	var order []*unit
	for _, m := range models {
		var u *unit
		if m.Twin {
			u = &unit{name: m.Name, aliases: map[string]bool{}}
			if m.Module != nil && m.Module.Name != "" {
				u.aliases[m.Module.Name] = true
			}
			order = append(order, u)
		} else if u = groups[m.Group]; u == nil {
			u = &unit{name: m.Name}
			groups[m.Group] = u
			order = append(order, u)
			c.byName[m.Name] = append(c.byName[m.Name], u)
		}
		u.models = append(u.models, m)
		u.broken = u.broken || m.Broken
	}
	for _, u := range order {
		c.unit(u)
	}
	c.units = order
	return c
}

type checker struct {
	opts   Options
	byName map[string][]*unit
	findingList
	cur        *Model
	props      map[*Concept]*propSet
	memberSets map[*Concept]map[string]bool // see memberSet
	units      []*unit
	builds     int // member sets built: see steps
}

// steps counts the work that lookups do, in units that do not depend on time: the
// concepts visited to index the units, the members listed to build member sets,
// and the sets and names consulted or copied to answer whether a name is a
// property. Tests assert that it grows with the size of the model, and not with
// the number of lookups.
func (c *checker) steps() (concepts, members, probes int) {
	for _, u := range c.units {
		concepts += u.visits
	}
	for _, p := range c.props {
		probes += p.probes
	}
	return concepts, c.builds, probes
}

func (c *checker) add(line int, rule string, sev Severity, format string, args ...any) {
	for i, a := range args {
		if text, ok := a.(string); ok {
			args[i] = clipText(text, MaxEchoBytes)
		}
	}
	c.put(Finding{File: c.cur.File, Line: line, Rule: rule, Severity: sev, Message: fmt.Sprintf(format, args...)})
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

// quote1 quotes a piece of the user's text for a message, cut to MaxEchoBytes.
func quote1(s string) string { return fmt.Sprintf("%q", clipText(s, MaxEchoBytes)) }

func (c *checker) unit(u *unit) {
	c.duplicates(u)
	for _, m := range u.models {
		if m.Broken {
			continue
		}
		c.cur = m
		if m.Twin {
			c.staleTwin(m)
		}
		for _, k := range m.Concepts {
			c.names(k)
			c.attrs(string(k.Kind)+" "+quote1(k.Name), k.Attrs, conceptAttrs[k.Kind])
			switch k.Kind {
			case KindEntity:
				c.entity(u, k)
			case KindComponent:
				c.members(u, k)
			case KindEnum:
				c.enum(k)
			case KindCollection:
				c.collection(u, k)
			default:
				c.recordset(u, k)
			}
		}
	}
	if c.opts.publish() {
		c.publishUnit(u)
	}
}

// duplicates reports a concept name declared twice in one name scope of the
// module, wherever the two declarations are (decision 0015).
func (c *checker) duplicates(u *unit) {
	type decl struct {
		model *Model
		line  int
		name  string
	}
	where := func(prev decl, m *Model) string {
		if prev.model != m {
			return fmt.Sprintf("%s:%d", prev.model.File, prev.line)
		}
		return fmt.Sprintf("line %d", prev.line)
	}
	seen := map[string]decl{}
	folded := map[string]decl{}
	for _, m := range u.models {
		if m.Broken {
			continue
		}
		c.cur = m
		for _, k := range m.Concepts {
			key := conceptScope(k.Kind) + "\x00" + k.Name
			if prev, dup := seen[key]; dup {
				c.errorf(k.Line, RuleDuplicate, "duplicate concept name %q in the %s scope (also declared at %s)", k.Name, conceptScope(k.Kind), where(prev, m))
				continue
			}
			seen[key] = decl{m, k.Line, k.Name}
			fkey := conceptScope(k.Kind) + "\x00" + strings.ToLower(k.Name)
			if prev, dup := folded[fkey]; dup {
				c.add(k.Line, RuleNameCase, SeverityWarning, "%s name %q differs only by case from %q (declared at %s) in the %s scope; on a case-insensitive store they collide", k.Kind, k.Name, prev.name, where(prev, m), conceptScope(k.Kind))
				continue
			}
			folded[fkey] = decl{m, k.Line, k.Name}
		}
	}
}

// names checks a concept's name: the standard forbids a dot (decision 0014)
// and the five reserved tokens (decision 0015), and nothing else.
func (c *checker) names(k *Concept) {
	switch {
	case strings.TrimSpace(k.Name) == "":
		c.errorf(k.Line, RuleNameForm, "%s name must not be empty or blank; it could never be referenced", k.Kind)
	case ReservedNames[k.Name]:
		c.errorf(k.Line, RuleReserved, "%s name %q is a reserved kind token (entities, components, enums, collections, recordsets) and cannot name a concept", k.Kind, k.Name)
	case strings.Contains(k.Name, "."):
		c.errorf(k.Line, RuleNameForm, "%s name %q must not contain a dot (decision 0014: a dot marks a module-qualified reference)", k.Kind, k.Name)
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

// enumValues returns the values of an enum list as comparable keys, or false
// when an item is not a string or an integer.
func enumValues(n *Node) ([]string, bool) {
	if n.Type != NodeArray {
		return nil, false
	}
	keys := make([]string, 0, len(n.Items))
	for _, it := range n.Items {
		switch {
		case it.Type == NodeString:
			keys = append(keys, "s:"+it.Str)
		case it.Type == NodeNumber && isIntegerNumber(it.Str):
			keys = append(keys, "n:"+it.Str)
		default:
			return nil, false
		}
	}
	return keys, true
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
		if v.Type != NodeNumber || !isIntegerNumber(v.Str) || strings.HasPrefix(v.Str, "-") {
			return "must be a non-negative integer"
		}
	case vStringList:
		if _, ok := v.stringList(); !ok {
			return "must be a list of strings"
		}
	case vEnumValues:
		if _, ok := enumValues(v); !ok {
			return "must be a list of strings or integers"
		}
	default: // vEnum
		if v.Type != NodeString {
			if _, ok := enumValues(v); !ok {
				return "must be the name of an enum or a list of string or integer values"
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
	vals, ok := enumValues(a.Value)
	if !ok {
		return // reported as an attribute type problem
	}
	c.valueList(a.Line, "enum "+quote1(k.Name), vals)
}

// valueList checks that a list of enum values is non-empty and has no repeats.
func (c *checker) valueList(line int, who string, vals []string) {
	if len(vals) == 0 {
		c.errorf(line, RuleEnumValues, "%s must declare at least one value", who)
	}
	seen := map[string]bool{}
	for _, v := range vals {
		if seen[v] {
			c.errorf(line, RuleEnumValues, "%s has duplicate value %q", who, v[2:])
		}
		seen[v] = true
	}
}

// ---- entities, components and their members

func (c *checker) entity(u *unit, k *Concept) {
	c.members(u, k)
	who := "entity " + quote1(k.Name)
	if use, ok := k.Attr("use"); ok {
		if names, ok := use.Value.stringList(); ok {
			for _, n := range names {
				c.ref(u, use.Line, who+" use", n, KindComponent)
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
	known := c.propertyNames(u, k)
	for _, n := range names {
		if known.complete && !known.has(n) {
			c.errorf(key.Line, RuleKey, "%s key %q is not a property of the entity (or of a component it uses)", who, n)
		}
	}
}

// propSet answers whether a name is a property of an entity: one of its own, or
// a field of a component it uses (which may be in the module's other files or in
// another module, decision 0014). complete is false when some used component
// could not be read, so a name's absence proves nothing.
//
// The names of a component's fields are kept once for the component, and not
// copied into every entity that uses it. Lookups go through the used components
// one by one until that would cost more than copying their fields into one set,
// which is then built once; so the work for an entity is at most about the lesser
// of (lookups x used components) and the fields of the components it uses.
type propSet struct {
	own      map[string]bool
	used     []map[string]bool
	fields   int
	lookups  int
	union    map[string]bool
	complete bool
	probes   int // sets consulted and names copied: see checker.steps
}

func (p *propSet) has(name string) bool {
	if p.own[name] {
		return true
	}
	if p.union == nil {
		if p.lookups++; p.lookups*len(p.used) <= p.fields+len(p.used) {
			for _, set := range p.used {
				p.probes++
				if set[name] {
					return true
				}
			}
			return false
		}
		p.union = make(map[string]bool, p.fields)
		for _, set := range p.used {
			for n := range set {
				p.probes++
				p.union[n] = true
			}
		}
	}
	return p.union[name]
}

// memberSet is the set of the names of a concept's members, built once.
func (c *checker) memberSet(k *Concept) map[string]bool {
	if set, ok := c.memberSets[k]; ok {
		return set
	}
	set := make(map[string]bool, len(k.Members))
	for _, mem := range k.Members {
		c.builds++
		set[mem.Name] = true
	}
	c.memberSets[k] = set
	return set
}

// propertyNames returns the properties of the entity k of unit u, built once for
// each entity.
func (c *checker) propertyNames(u *unit, k *Concept) *propSet {
	if p, ok := c.props[k]; ok {
		return p
	}
	p := &propSet{own: c.memberSet(k), complete: true}
	c.props[k] = p
	use, ok := k.Attr("use")
	if !ok {
		return p
	}
	used, ok := use.Value.stringList()
	if !ok {
		p.complete = false
		return p
	}
	for _, n := range used {
		target, name, problem := c.lookup(u, n)
		if problem != "" || target == nil {
			p.complete = false
			continue
		}
		comp := target.find(KindComponent, name)
		if comp == nil {
			p.complete = false
			continue
		}
		set := c.memberSet(comp)
		p.used = append(p.used, set)
		p.fields += len(set)
	}
	return p
}

// members checks the properties of an entity or the fields of a component or
// collection.
func (c *checker) members(u *unit, k *Concept) {
	allowed := memberAttrs(k.Kind)
	seen := map[string]int{}
	foldedMembers := map[string]string{}
	for _, mem := range k.Members {
		who := fmt.Sprintf("%s %s %s %s", k.Kind, quote1(k.Name), memberWord(k.Kind), quote1(mem.Name))
		if strings.TrimSpace(mem.Name) == "" {
			c.errorf(mem.Line, RuleNameForm, "%s name must not be empty or blank in %s %q; it could never be referenced", memberWord(k.Kind), k.Kind, k.Name)
		}
		if prev, dup := seen[mem.Name]; dup {
			c.errorf(mem.Line, RuleDuplicate, "duplicate %s %q in %s %q (also declared at line %d)", memberWord(k.Kind), mem.Name, k.Kind, k.Name, prev)
		} else if prevName, ok := foldedMembers[strings.ToLower(mem.Name)]; ok {
			c.add(mem.Line, RuleNameCase, SeverityWarning, "%s name %q differs only by case from %q in %s %q; on a case-insensitive store they collide", memberWord(k.Kind), mem.Name, prevName, k.Kind, k.Name)
			seen[mem.Name] = mem.Line
		} else {
			seen[mem.Name] = mem.Line
			foldedMembers[strings.ToLower(mem.Name)] = mem.Name
		}
		c.attrs(who, mem.Attrs, allowed)
		if k.Kind == KindEntity || k.Kind == KindComponent {
			c.memberKind(who, mem)
		}
		c.memberRefs(u, who, mem)
		if a, ok := mem.Attr("bind"); ok && k.Kind == KindCollection && a.Value.Type == NodeString {
			c.bind(u, a, who)
		}
	}
}

// memberKind requires exactly one of type, entity and component
// (spec/json-format.md: "Property objects MAY use one of").
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
func (c *checker) memberRefs(u *unit, who string, mem Member) {
	if a, ok := mem.Attr("type"); ok && a.Value.Type == NodeString {
		c.typeName(a, who)
	}
	for _, r := range []struct {
		attr string
		kind Kind
	}{{"entity", KindEntity}, {"component", KindComponent}} {
		if a, ok := mem.Attr(r.attr); ok && a.Value.Type == NodeString {
			c.ref(u, a.Line, who+" "+r.attr, a.Value.Str, r.kind)
		}
	}
	if a, ok := mem.Attr("enum"); ok {
		if a.Value.Type == NodeString {
			c.ref(u, a.Line, who+" enum", a.Value.Str, KindEnum)
		} else if vals, ok := enumValues(a.Value); ok {
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

// lookup resolves a reference to the module that holds it. target is nil when
// the problem is non-empty or the module could not be read.
func (c *checker) lookup(u *unit, ref string) (target *unit, name, problem string) {
	module, name, qualified := strings.Cut(ref, ".")
	if !qualified {
		return u, ref, ""
	}
	if strings.Contains(name, ".") || module == "" || name == "" {
		return nil, "", quote1(ref) + " is not a bare name or a <module>.<Name> reference"
	}
	targets := c.byName[module]
	switch {
	case module == u.name || u.aliases[module]:
		// A module always resolves its own name to itself; a twin also its own
		// module.name.
		return u, name, ""
	case len(targets) == 1:
		if targets[0].broken {
			return nil, "", "" // reported as a syntax problem in that module's file
		}
		return targets[0], name, ""
	case len(targets) > 1:
		return nil, "", fmt.Sprintf("module %s is ambiguous: %d different sources claim it (%s)", quote1(module), len(targets), sourceList(targets))
	default:
		return nil, "", fmt.Sprintf("unknown module %s (lint the files that declare it together with this one, or name it with --module %s=<path>)", quote1(module), clipText(module, MaxEchoBytes))
	}
}

func sourceList(us []*unit) string {
	var files []string
	for _, u := range us {
		files = append(files, u.models[0].File)
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
func (c *checker) ref(u *unit, line int, what, ref string, kind Kind) {
	target, name, problem := c.lookup(u, ref)
	if problem != "" {
		c.errorf(line, RuleReference, "%s reference %q: %s", what, ref, problem)
		return
	}
	if target == nil || target.broken || target.find(kind, name) != nil {
		return
	}
	if actual, ok := target.trioKind(name); ok {
		c.errorf(line, RuleReference, "%s reference %q: %q is %s, not %s", what, ref, name, withArticle(actual), withArticle(kind))
		return
	}
	if strings.Contains(ref, ".") {
		c.errorf(line, RuleReference, "%s reference %q: unknown %s %q in module %q", what, ref, kind, name, target.name)
		return
	}
	c.errorf(line, RuleReference, "%s reference %q does not resolve to %s in module %q", what, ref, withArticle(kind), target.name)
}

// bind checks a bind value, <Entity>.<property> or <module>.<Entity>.<property>.
func (c *checker) bind(u *unit, a *Attr, who string) {
	parts := strings.Split(a.Value.Str, ".")
	if len(parts) != 2 && len(parts) != 3 {
		c.errorf(a.Line, RuleReference, "%s bind %q must be <Entity>.<property> or <module>.<Entity>.<property>", who, a.Value.Str)
		return
	}
	prop := parts[len(parts)-1]
	entityRef := strings.Join(parts[:len(parts)-1], ".")
	target, name, problem := c.lookup(u, entityRef)
	if problem != "" {
		c.errorf(a.Line, RuleReference, "%s bind %q: %s", who, a.Value.Str, problem)
		return
	}
	if target == nil || target.broken {
		return
	}
	ent := target.find(KindEntity, name)
	if ent == nil {
		c.errorf(a.Line, RuleReference, "%s bind %q: no entity %q in module %q", who, a.Value.Str, name, target.name)
		return
	}
	if known := c.propertyNames(target, ent); known.complete && !known.has(prop) {
		c.errorf(a.Line, RuleReference, "%s bind %q: entity %q has no property %q (or component field of that name)", who, a.Value.Str, name, prop)
	}
}

// ---- collections and recordsets

func (c *checker) collection(u *unit, k *Concept) {
	c.members(u, k)
	who := "collection " + quote1(k.Name)
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
		c.ref(u, src.Line, who+" source", src.Value.Str, KindEntity)
	}
}

func (c *checker) recordset(u *unit, k *Concept) {
	allowed := memberAttrs(KindRecordset)
	for _, col := range k.Members {
		who := fmt.Sprintf("recordset %s column %s", quote1(k.Name), quote1(col.Name))
		c.attrs(who, col.Attrs, allowed)
		if a, ok := col.Attr("type"); ok && a.Value.Type == NodeString {
			c.typeName(a, who)
		}
		if a, ok := col.Attr("bind"); ok && a.Value.Type == NodeString {
			c.bind(u, a, who)
		}
	}
	if key, ok := k.Attr("key"); ok {
		if names, ok := key.Value.stringList(); ok {
			columns := c.memberSet(k)
			for _, n := range names {
				if !columns[n] {
					c.errorf(key.Line, RuleKey, "recordset %q key %q is not one of its columns", k.Name, n)
				}
			}
		}
	}
}

// ---- the publish profile

// publishUnit applies the rules of the publish profile that concern the module
// as a whole. Each rule exists because the Directory's JSON reader
// (parseModelSpec in openvaultdb/directory) refuses a model without it; the
// standard does not require it.
func (c *checker) publishUnit(u *unit) {
	entities := 0
	for _, m := range u.models {
		if m.Broken {
			continue
		}
		c.cur = m
		for _, k := range m.Concepts {
			if k.Kind == KindEntity {
				entities++
				c.publishEntity(u, k)
			}
		}
		if m.Form == FormJSON && m.Module != nil {
			c.publishModuleName(m)
		}
	}
	if entities == 0 && !u.broken {
		c.cur = u.models[0]
		c.errorf(1, RulePublishEntities, "has no entities; a published model declares at least one entity (the catalogue lists a model by its entities)")
	}
}

func (c *checker) publishModuleName(m *Model) {
	name := m.Module.Name
	switch {
	case name == "":
		c.errorf(m.ModuleLine, RulePublishModuleName, "has no module.name; a published model names its module, as an identifier (the catalogue refers to a model by it)")
	case !identifier.MatchString(name):
		c.errorf(m.ModuleLine, RulePublishModuleName, "module.name %q is not an identifier (letters, digits and _, not starting with a digit); the catalogue refers to a model by it", name)
	}
}

func (c *checker) publishEntity(u *unit, k *Concept) {
	if !identifier.MatchString(k.Name) {
		c.errorf(k.Line, RulePublishNameForm, "entity name %q is not an identifier (letters, digits and _, not starting with a digit); the catalogue turns entity names into record-set names", k.Name)
	}
	if len(k.Members) == 0 {
		c.errorf(k.Line, RulePublishProperties, "entity %q has no properties of its own; the catalogue lists an entity by its properties", k.Name)
	}
	for _, mem := range k.Members {
		who := fmt.Sprintf("entity %s property %s", quote1(k.Name), quote1(mem.Name))
		if !identifier.MatchString(mem.Name) {
			c.errorf(mem.Line, RulePublishNameForm, "property name %q of entity %q is not an identifier (letters, digits and _, not starting with a digit); the catalogue turns property names into column names", mem.Name, k.Name)
		}
		if a, ok := mem.Attr("component"); ok {
			c.errorf(a.Line, RulePublishComponent, "%s has a component value; the catalogue lists only scalar and entity-reference properties", who)
		}
		if a, ok := mem.Attr("entity"); ok && a.Value.Type == NodeString && strings.Contains(a.Value.Str, ".") {
			c.errorf(a.Line, RulePublishQualified, "%s refers to %q in another module; the catalogue resolves entity references inside the one model only", who, a.Value.Str)
		}
	}
}

// staleTwin compares a JSON twin with the export of the HCL it sits beside. A
// twin that differs is a warning, not an error: it is still a valid model, but
// the copy consumers read is not what the source says. export --check is the
// strict gate.
func (c *checker) staleTwin(m *Model) {
	h := m.TwinOf
	if h == nil || h.Broken || m.Root == nil || m.Module == nil || m.Module.ID == "" || m.Module.Version == "" {
		return // nothing to compare, or the JSON's own findings say what is wrong
	}
	diff, err := h.exportDiff(m.Root, *m.Module)
	if err != nil || diff == "" {
		return // an HCL file that cannot be exported cannot have a twin to compare with
	}
	c.add(1, RuleStaleTwin, SeverityWarning, "stale twin: %s is not what %s exports to (%s); run modelspec export", m.File, h.File, diff)
}
