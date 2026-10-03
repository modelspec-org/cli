package modelspec

import (
	"strings"
	"testing"
)

const hclExt = ".modelspec.hcl"

func member(name, body string) string {
	return "  property \"" + name + "\" {\n" + body + "  }\n"
}

func entityWith(name string, props ...string) string {
	return "entity \"" + name + "\" {\n  key = [\"id\"]\n  property \"id\" {\n    type = \"int\"\n  }\n" + strings.Join(props, "") + "}\n"
}

func TestCheckHCL(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		src  string
		want []string
	}{
		{"clean", okEntity, nil},
		{"empty file", "", nil},
		{"reserved name", `entity "entities" {
  key = ["id"]
  property "id" {
    type = "int"
  }
}
`, []string{`:1: error: entity name "entities" is a reserved kind token`}},
		{"all five reserved names", "enum \"components\" {\n  values = [\"a\"]\n}\ncomponent \"enums\" {\n}\ncollection \"collections\" {\n  kind = \"editable\"\n}\nrecordset \"recordsets\" {\n}\nentity \"entities\" {\n  key = []\n}\n", []string{`enum name "components"`, `component name "enums"`, `collection name "collections"`, `recordset name "recordsets"`, `entity name "entities"`, "has an empty key"}},
		{"a dot in a concept name", "enum \"a.b\" {\n  values = [\"x\"]\n}\n", []string{`enum name "a.b" must not contain a dot`}},
		{"names are not restricted to identifiers", `entity "Order-Item" {
  key = ["unit-price"]
  property "unit-price" {
    type = "decimal"
  }
  property "my prop" {
    type = "string"
  }
  property "a.b" {
    type = "string"
  }
}
component "my component" {
  field "created-at" {
    type = "datetime"
  }
}
collection "order-items" {
  kind = "editable"
  field "created-at" {
    type = "datetime"
  }
}
recordset "Order Totals" {
  column "Total Sales" {
    type = "decimal"
  }
}
enum "9lives" {
  values = ["a"]
}
`, nil},
		{"duplicate concepts", okEntity + okEntity + `enum "A" {
  values = ["x"]
}
collection "c" {
  kind = "editable"
}
collection "c" {
  kind = "editable"
}
recordset "c" {
}
`, []string{`:7: error: duplicate concept name "A" in the entity/component/enum scope (also declared at line 1)`, `:13: error: duplicate concept name "A" in the entity/component/enum scope (also declared at line 1)`, `duplicate concept name "c" in the collection scope`}},
		{"enum without values attribute", "enum \"E\" {\n}\n", []string{`enum "E" must declare at least one value`}},
		{"enum with values not a list", "enum \"E\" {\n  values = \"a\"\n}\n", []string{`enum "E": "values" must be a list of strings or integers`}},
		{"enum empty and duplicate", "enum \"E\" {\n  values = []\n}\nenum \"F\" {\n  values = [\"a\", \"a\"]\n}\n", []string{`enum "E" must declare at least one value`, `enum "F" has duplicate value "a"`}},
		{"integer enum values", "enum \"N\" {\n  values = [1, 2, 3]\n}\nenum \"Mixed\" {\n  values = [1, \"1\", -4]\n}\n", nil},
		{"duplicate integer enum values", "enum \"N\" {\n  values = [1, 2, 1]\n}\n", []string{`enum "N" has duplicate value "1"`}},
		{"float and bool enum values", "enum \"F\" {\n  values = [1.5]\n}\nenum \"B\" {\n  values = [true]\n}\n", []string{`"values" must be a list of strings or integers`, `"values" must be a list of strings or integers`}},
		{"inline integer enum", entityWith("A2", member("n", "    type = \"int\"\n    enum = [1, 2]\n")), nil},
		{"unsupported concept attribute", "component \"C\" {\n  x = 1\n}\n", []string{`component "C" has unsupported attribute "x"`}},
		{"attribute value types", entityWith("A2", member("p", `    type     = "int"
    required = "yes"
    unique   = 1
    min_len  = -1
    max_len  = 1.5
    pattern  = true
    format   = ["x"]
    enum     = 5
`)), []string{`"required" must be true or false`, `"unique" must be true or false, not a number`, `"min_len" must be a non-negative integer`, `"max_len" must be a non-negative integer`, `"pattern" must be a string, not a boolean`, `"format" must be a string, not an array`, `"enum" must be the name of an enum or a list of string or integer values`}},
		{"unsupported member attribute", entityWith("A2", member("p", "    type = \"int\"\n    bind = \"x\"\n")), []string{`entity "A2" property "p" has unsupported attribute "bind"`}},
		{"entity without key", "entity \"A\" {\n  property \"id\" {\n    type = \"int\"\n  }\n}\n", []string{`:1: error: entity "A" has no key`}},
		{"entity key not a list", "entity \"A\" {\n  key = \"id\"\n  property \"id\" {\n    type = \"int\"\n  }\n}\n", []string{`"key" must be a list of strings`}},
		{"entity empty key", "entity \"A\" {\n  key = []\n  property \"id\" {\n    type = \"int\"\n  }\n}\n", []string{`:2: error: entity "A" has an empty key`}},
		{"entity key not a property", "entity \"A\" {\n  key = [\"ghost\"]\n  property \"id\" {\n    type = \"int\"\n  }\n}\n", []string{`entity "A" key "ghost" is not a property of the entity`}},
		{"entity without properties is fine", "entity \"A\" {\n  key = []\n}\nentity \"B\" {\n  key = [\"x\"]\n}\n", []string{`entity "A" has an empty key`, `entity "B" key "x" is not a property`}},
		{"key from a used component", `component "C" {
  field "id" {
    type = "int"
  }
}
entity "A" {
  key = ["id"]
  use = ["C"]
}
`, nil},
		{"key naming a field a used component lacks", `component "C" {
  field "id" {
    type = "int"
  }
}
entity "A" {
  key = ["nope"]
  use = ["C"]
}
`, []string{`key "nope" is not a property of the entity (or of a component it uses)`}},
		{"use not a list", "entity \"A\" {\n  key = []\n  use = \"C\"\n}\n", []string{`"use" must be a list of strings`, `has an empty key`}},
		{"use unresolved and wrong kind", `enum "E" {
  values = ["a"]
}
entity "A" {
  key = ["id"]
  use = ["Missing", "E"]
  property "id" {
    type = "int"
  }
}
`, []string{`entity "A" use reference "Missing" does not resolve to a component in module "a"`, `entity "A" use reference "E": "E" is an enum, not a component`}},
		{"duplicate properties", "entity \"A\" {\n  key = [\"id\"]\n  property \"id\" {\n    type = \"int\"\n  }\n  property \"id\" {\n    type = \"int\"\n  }\n}\n", []string{`:6: error: duplicate property "id" in entity "A" (also declared at line 3)`}},
		{"member kind", entityWith("A2", member("none", "    required = true\n"), member("two", "    type   = \"int\"\n    entity = \"A2\"\n")), []string{`property "none" must have exactly one of type, entity or component`, `property "two" must have exactly one of type, entity or component`}},
		{"unknown type", "entity \"A\" {\n  key = [\"id\"]\n  property \"id\" {\n    type = \"serial\"\n  }\n}\n", []string{`:4: error: entity "A" property "id" has type "serial"`}},
		{"type not a string", "entity \"A\" {\n  key = [\"id\"]\n  property \"id\" {\n    type = 1\n  }\n}\n", []string{`"type" must be a string`}},
		{"references", `component "C" {
  field "f" {
    type = "int"
  }
}
enum "E" {
  values = ["a"]
}
entity "A" {
  key = ["id"]
  property "id" {
    type = "int"
  }
  property "ok1" {
    entity = "A"
  }
  property "ok2" {
    component = "C"
  }
  property "ok3" {
    type = "string"
    enum = "E"
  }
  property "ok4" {
    type = "string"
    enum = ["a", "b"]
  }
  property "bad1" {
    entity = "C"
  }
  property "bad2" {
    component = "Nope"
  }
  property "bad3" {
    type = "string"
    enum = "A"
  }
  property "bad4" {
    type = "string"
    enum = []
  }
  property "bad5" {
    type = "string"
    enum = ["a", "a"]
  }
}
`, []string{`property "bad1" entity reference "C": "C" is a component, not an entity`, `property "bad2" component reference "Nope" does not resolve to a component`, `property "bad3" enum reference "A": "A" is an entity, not an enum`, `property "bad4" enum must declare at least one value`, `property "bad5" enum has duplicate value "a"`}},
		{"component members", "component \"C\" {\n  field \"f\" {\n    type = \"nope\"\n  }\n}\n", []string{`component "C" field "f" has type "nope"`}},
		{"component with no member kind", "component \"C\" {\n  field \"f\" {\n  }\n}\n", []string{`component "C" field "f" must have exactly one of type, entity or component`}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			expect(t, run(map[string]string{"a" + hclExt: tc.src}), tc.want...)
		})
	}
}

// The standard's own examples lint clean under the default profile, with no
// warning (spec/core-model.md).
func TestStandardExamplesAreClean(t *testing.T) {
	t.Parallel()
	src := `component "Auditable" {
  field "createdAt" {
    type     = "datetime"
    required = true
  }
}
component "CurrencyAmount" {
  field "amount" {
    type     = "decimal"
    required = true
  }
  field "currency" {
    type     = "string"
    required = true
  }
}
enum "BookingStatus" {
  values = ["requested", "confirmed", "cancelled"]
}
entity "User" {
  key = ["id"]
  property "id" {
    type = "uuid"
  }
}
entity "Invoice" {
  key = ["id"]
  use = ["Auditable"]
  property "id" {
    type = "uuid"
  }
  property "total" {
    component = "CurrencyAmount"
  }
  property "status" {
    type = "string"
    enum = "BookingStatus"
  }
  property "owner" {
    entity   = "User"
    required = true
  }
  index "invoice_status" {
    properties = ["status"]
  }
}
`
	expect(t, run(map[string]string{"invoice" + hclExt: src}))
}

func TestCheckModules(t *testing.T) {
	t.Parallel()
	core := `entity "Space" {
  key = ["id"]
  property "id" {
    type = "int"
  }
}
component "Window" {
  field "from" {
    type = "date"
  }
}
component "Identified" {
  field "id" {
    type = "uuid"
  }
}
enum "Role" {
  values = ["a"]
}
`
	booking := func(refs ...string) string { return entityWith("Booking", refs...) }
	prop := func(name, attr, ref string) string {
		return member(name, "    "+attr+" = \""+ref+"\"\n")
	}
	tests := []struct {
		name  string
		files map[string]string
		want  []string
	}{
		{"qualified references resolve", map[string]string{"core" + hclExt: core, "booking" + hclExt: booking(prop("s", "entity", "core.Space"), prop("w", "component", "core.Window"), member("r", "    type = \"string\"\n    enum = \"core.Role\"\n"))}, nil},
		{"self-qualified reference", map[string]string{"booking" + hclExt: booking(prop("s", "entity", "booking.Booking"))}, nil},
		{"unknown module says how to supply it", map[string]string{"booking" + hclExt: booking(prop("s", "entity", "core.Space"))}, []string{`reference "core.Space": unknown module "core" (lint the files that declare it together with this one, or name it with --module core=<path>)`}},
		{"unknown concept in module", map[string]string{"core" + hclExt: core, "booking" + hclExt: booking(prop("s", "entity", "core.Nope"))}, []string{`reference "core.Nope": unknown entity "Nope" in module "core"`}},
		{"wrong kind in module", map[string]string{"core" + hclExt: core, "booking" + hclExt: booking(prop("s", "entity", "core.Role"))}, []string{`reference "core.Role": "Role" is an enum, not an entity`}},
		{"two different sources claim one module", map[string]string{"a/core" + hclExt: core, "b/core" + hclExt: core, "booking" + hclExt: booking(prop("s", "entity", "core.Space"))}, []string{`module "core" is ambiguous: 2 different sources claim it (a/core.modelspec.hcl, b/core.modelspec.hcl)`}},
		{"a reference into a broken module is not reported", map[string]string{"core" + hclExt: "entity {", "booking" + hclExt: booking(prop("s", "entity", "core.Space"), "  property \"k\" {\n    type = \"int\"\n  }\n")}, []string{"core.modelspec.hcl:1: error: "}},
		{"malformed qualified names", map[string]string{"booking" + hclExt: booking(prop("a", "entity", "x.y.z"), prop("b", "entity", ".y"), prop("c", "entity", "x."))},
			[]string{`"x.y.z" is not a bare name or a <module>.<Name> reference`, `".y" is not a bare name`, `"x." is not a bare name`}},
		// M5: a key or bind sees the fields a component contributes, also from another module.
		{"key through a component of another module", map[string]string{"core" + hclExt: core, "sales" + hclExt: "entity \"Order\" {\n  key = [\"id\"]\n  use = [\"core.Identified\"]\n}\ncollection \"orders\" {\n  kind = \"editable\"\n  source = \"Order\"\n  field \"id\" {\n    type = \"uuid\"\n    bind = \"Order.id\"\n  }\n}\n"}, nil},
		{"key naming a field the other module's component lacks", map[string]string{"core" + hclExt: core, "sales" + hclExt: "entity \"Order\" {\n  key = [\"zzz\"]\n  use = [\"core.Identified\"]\n}\n"}, []string{`key "zzz" is not a property of the entity (or of a component it uses)`}},
		{"key through a component of an unsupplied module is not judged", map[string]string{"sales" + hclExt: "entity \"Order\" {\n  key = [\"id\"]\n  use = [\"core.Identified\"]\n}\ncollection \"orders\" {\n  kind = \"editable\"\n  field \"id\" {\n    type = \"uuid\"\n    bind = \"Order.id\"\n  }\n}\n"}, []string{`use reference "core.Identified": unknown module "core"`}},
		{"key through a component of a broken module is not judged", map[string]string{"core" + hclExt: "entity {", "sales" + hclExt: "entity \"Order\" {\n  key = [\"id\"]\n  use = [\"core.Identified\"]\n}\n"}, []string{"core.modelspec.hcl:1: error: "}},
		{"key through a component that does not exist is judged by the reference only", map[string]string{"sales" + hclExt: "entity \"Order\" {\n  key = [\"id\"]\n  use = [\"Nope\"]\n}\n"}, []string{`use reference "Nope" does not resolve to a component`}},
		{"key through a used component that is not a list", map[string]string{"sales" + hclExt: "entity \"Order\" {\n  key = [\"id\"]\n  use = \"X\"\n}\n"}, []string{`"use" must be a list of strings`}},
		{"bind through a component of another module", map[string]string{"core" + hclExt: core, "sales" + hclExt: entityWith("Order", "  use = [\"core.Identified\"]\n") + "collection \"o\" {\n  kind = \"editable\"\n  field \"x\" {\n    type = \"uuid\"\n    bind = \"Order.nope\"\n  }\n}\n"}, []string{`bind "Order.nope": entity "Order" has no property "nope" (or component field of that name)`}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			expect(t, run(tc.files), tc.want...)
		})
	}
}

// layout builds a path in the SpecScore layout.
func layout(module, file string) string {
	return "spec/graph/modules/" + module + "/models/" + file
}

func TestCheckSpecScoreLayout(t *testing.T) {
	t.Parallel()
	sales := map[string]string{
		layout("sales", "entities.modelspec.hcl"): "entity \"Order\" {\n  key = [\"id\"]\n  property \"id\" {\n    type = \"int\"\n  }\n  property \"status\" {\n    type = \"string\"\n    enum = \"OrderStatus\"\n  }\n  property \"customer\" {\n    entity = \"core.Customer\"\n  }\n}\n",
		layout("sales", "enums.modelspec.hcl"):    "enum \"OrderStatus\" {\n  values = [\"open\"]\n}\n",
		layout("core", "model.modelspec.hcl"):     "entity \"Customer\" {\n  key = [\"id\"]\n  property \"id\" {\n    type = \"int\"\n  }\n}\n",
	}
	copyWith := func(extra map[string]string) map[string]string {
		out := map[string]string{}
		for k, v := range sales {
			out[k] = v
		}
		for k, v := range extra {
			out[k] = v
		}
		return out
	}
	tests := []struct {
		name  string
		files map[string]string
		want  []string
	}{
		{"files of one module see each other, whatever they are called", sales, nil},
		{"any .hcl file in a models directory", copyWith(map[string]string{layout("sales", "plain.hcl"): "enum \"Extra\" {\n  values = [\"x\"]\n}\n"}), nil},
		{"a concept declared in two files of one module", copyWith(map[string]string{layout("sales", "extra.modelspec.hcl"): "enum \"OrderStatus\" {\n  values = [\"x\"]\n}\n"}),
			[]string{"extra.modelspec.hcl:1: error: duplicate concept name \"OrderStatus\" in the entity/component/enum scope (also declared at " + layout("sales", "enums.modelspec.hcl") + ":1)"}},
		{"the same name in two modules is not a duplicate", copyWith(map[string]string{layout("core", "enums.hcl"): "enum \"OrderStatus\" {\n  values = [\"x\"]\n}\n"}), nil},
		{"a sibling's concept is missing", map[string]string{layout("sales", "entities.hcl"): sales[layout("sales", "entities.modelspec.hcl")], layout("core", "m.hcl"): sales[layout("core", "model.modelspec.hcl")]},
			[]string{`entity "Order" property "status" enum reference "OrderStatus" does not resolve to an enum in module "sales"`}},
		{"a .hcl file outside a layout is not a model file", map[string]string{"models/x.hcl": "entity \"A\" {\n}\n"}, []string{"error: no ModelSpec files"}},
		{"a models directory not under modules is not a layout", map[string]string{"models/entities.modelspec.hcl": sales[layout("sales", "entities.modelspec.hcl")], "models/enums.modelspec.hcl": sales[layout("sales", "enums.modelspec.hcl")]},
			[]string{`enum reference "OrderStatus" does not resolve to an enum in module "entities"`, `reference "core.Customer": unknown module "core"`}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			expect(t, run(tc.files), tc.want...)
		})
	}
}

func TestCheckTwins(t *testing.T) {
	t.Parallel()
	coreHCL := entityWith("Space")
	coreJSON := `{"modelspec": "1.0-draft", "module": {"id": "x/core", "name": "core", "version": "1"}, "entities": {"Space": {"key": ["id"], "properties": {"id": {"type": "int"}}}}}`
	booking := entityWith("Booking", member("s", "    entity = \"core.Space\"\n"))
	tests := []struct {
		name  string
		files map[string]string
		want  []string
	}{
		{"an HCL module beside its JSON export is one module", map[string]string{"f/core" + hclExt: coreHCL, "f/core.modelspec.json": coreJSON, "f/booking" + hclExt: booking}, nil},
		{"a twin resolves its own qualified references", map[string]string{"core" + hclExt: coreHCL, "core.modelspec.json": coreJSON}, nil},
		{"a twin in another directory is a second source", map[string]string{"a/core" + hclExt: coreHCL, "b/core.modelspec.json": coreJSON, "booking" + hclExt: booking}, []string{`module "core" is ambiguous: 2 different sources claim it`}},
		{"a JSON file with no HCL beside it is a module of its own", map[string]string{"core.modelspec.json": coreJSON, "booking" + hclExt: booking}, nil},
		{"two JSON modules of one name", map[string]string{"a/x.modelspec.json": coreJSON, "b/y.modelspec.json": coreJSON, "booking" + hclExt: booking}, []string{`module "core" is ambiguous`}},
		{"JSON without module.name is named by its file", map[string]string{"core.modelspec.json": strings.Replace(coreJSON, `"name": "core", `, "", 1), "booking" + hclExt: booking}, nil},
		{"the twin is still linted", map[string]string{"core" + hclExt: coreHCL, "core.modelspec.json": strings.Replace(coreJSON, `"int"`, `"serial"`, 1)}, []string{`core.modelspec.json:1: error: entity "Space" property "id" has type "serial"`}},
		{"a twin's duplicate does not clash with the HCL", map[string]string{"core" + hclExt: coreHCL, "core.modelspec.json": coreJSON}, nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			expect(t, run(tc.files), tc.want...)
		})
	}
}

func TestCheckCollectionsAndRecordsets(t *testing.T) {
	t.Parallel()
	task := `entity "Task" {
  key = ["id"]
  use = ["Audit"]
  property "id" {
    type = "int"
  }
}
component "Audit" {
  field "at" {
    type = "datetime"
  }
}
`
	tests := []struct {
		name string
		src  string
		want []string
	}{
		{"valid", task + `collection "tasks" {
  kind   = "editable"
  source = "Task"
  field "id" {
    type = "int"
    bind = "Task.id"
  }
  field "at" {
    type = "datetime"
    bind = "a.Task.at"
  }
  field "onlybind" {
    bind = "Task.id"
  }
  field "constrained" {
    type     = "string"
    required = true
    max_len  = 3
  }
}
collection "view" {
  kind  = "computed"
  query = "from tasks"
}
recordset "r" {
  key   = ["id"]
  query = "q"
  column "id" {
    type     = "int"
    bind     = "Task.id"
    required = true
  }
  column "n" {
    type   = "int"
    source = "count(x)"
  }
  column "n" {
    type = "int"
  }
}
`, nil},
		{"collection kinds", task + `collection "c1" {
  kind = "computed"
}
collection "c2" {
  kind = "weird"
}
collection "c3" {
  source = "Task"
}
collection "c4" {
  kind = 1
}
`, []string{`collection "c1" is computed but carries no query`, `collection "c2" kind is "weird"`, `collection "c3" has no kind`, `"kind" must be a string`}},
		{"collection source", task + `collection "c" {
  kind   = "editable"
  source = "Nope"
}
collection "d" {
  kind   = "editable"
  source = 1
}
`, []string{`collection "c" source reference "Nope" does not resolve to an entity`, `"source" must be a string`}},
		{"collection field bind", task + `collection "c" {
  kind = "editable"
  field "a" {
    type = "int"
    bind = "Task"
  }
  field "b" {
    type = "int"
    bind = "Nope.id"
  }
  field "c" {
    type = "int"
    bind = "Task.nope"
  }
  field "d" {
    type = "int"
    bind = "Audit.at"
  }
  field "e" {
    type = "int"
    bind = "core.Task.id"
  }
  field "f" {
    type = "int"
    bind = 1
  }
  field "g" {
    type = "int"
    bind = "a.b.c.d"
  }
}
`, []string{`bind "Task" must be <Entity>.<property>`, `bind "Nope.id": no entity "Nope" in module "a"`, `bind "Task.nope": entity "Task" has no property "nope"`, `bind "Audit.at": no entity "Audit" in module "a"`, `bind "core.Task.id": unknown module "core"`, `"bind" must be a string`, `bind "a.b.c.d" must be`}},
		{"bind to a missing entity in a known module", task + `collection "c" {
  kind = "editable"
  field "a" {
    type = "int"
    bind = "a.Nope.id"
  }
}
`, []string{`bind "a.Nope.id": no entity "Nope" in module "a"`}},
		{"recordset checks", task + `recordset "r" {
  key = ["ghost"]
  column "a" {
    type = "nope"
    bind = "Task.nope"
    unknown = true
  }
}
`, []string{`key "ghost" is not one of its columns`, `column "a" has type "nope"`, `bind "Task.nope": entity "Task" has no property "nope"`, `column "a" has unsupported attribute "unknown"`}},
		{"recordset key not a list", task + "recordset \"r\" {\n  key = \"id\"\n}\n", []string{`"key" must be a list of strings`}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			expect(t, run(map[string]string{"a" + hclExt: tc.src}), tc.want...)
		})
	}
}

func TestBindIntoBrokenModule(t *testing.T) {
	t.Parallel()
	src := entityWith("A") + "collection \"c\" {\n  kind = \"editable\"\n  field \"f\" {\n    type = \"int\"\n    bind = \"core.X.id\"\n  }\n}\n"
	expect(t, run(map[string]string{"a" + hclExt: src, "core" + hclExt: "entity {"}), "core.modelspec.hcl:1: error: ")
}

func TestPublishProfile(t *testing.T) {
	t.Parallel()
	good := entityWith("Order", member("customer", "    entity = \"Order\"\n"))
	tests := []struct {
		name  string
		files map[string]string
		want  []string
	}{
		{"a model that meets it", map[string]string{"a" + hclExt: good}, nil},
		{"no entities", map[string]string{"a" + hclExt: "enum \"E\" {\n  values = [\"x\"]\n}\n"}, []string{"a.modelspec.hcl:1: error: has no entities; a published model declares at least one entity"}},
		{"an empty file", map[string]string{"a" + hclExt: ""}, []string{"has no entities; a published"}},
		{"entities in a sibling file count", map[string]string{layout("m", "a.hcl"): "enum \"E\" {\n  values = [\"x\"]\n}\n", layout("m", "b.hcl"): good}, nil},
		{"an entity with no properties of its own", map[string]string{"a" + hclExt: "component \"C\" {\n  field \"id\" {\n    type = \"int\"\n  }\n}\nentity \"A\" {\n  key = [\"id\"]\n  use = [\"C\"]\n}\n"}, []string{`entity "A" has no properties of its own; the catalogue lists an entity by its properties`}},
		{"names that are not identifiers", map[string]string{"a" + hclExt: "entity \"Order-Item\" {\n  key = [\"unit-price\"]\n  property \"unit-price\" {\n    type = \"int\"\n  }\n}\ncomponent \"my comp\" {\n  field \"x y\" {\n    type = \"int\"\n  }\n}\n"}, []string{`entity name "Order-Item" is not an identifier`, `property name "unit-price" of entity "Order-Item" is not an identifier`}},
		{"a component value", map[string]string{"a" + hclExt: "component \"C\" {\n}\n" + entityWith("A", member("c", "    component = \"C\"\n"))}, []string{`entity "A" property "c" has a component value; the catalogue lists only scalar`}},
		{"an entity reference into another module", map[string]string{"core" + hclExt: entityWith("Space"), "a" + hclExt: entityWith("A", member("s", "    entity = \"core.Space\"\n"))}, []string{`refers to "core.Space" in another module; the catalogue resolves entity references inside the one model only`}},
		{"a broken module adds no publish finding", map[string]string{"a" + hclExt: "entity {"}, []string{"[syntax]"}},
		{"JSON module name missing", map[string]string{"a.modelspec.json": `{"modelspec": "1.0-draft", "module": {"id": "x", "version": "1"}, "entities": {"A": {"key": ["id"], "properties": {"id": {"type": "int"}}}}}`}, []string{"a.modelspec.json:1: error: has no module.name; a published model names its module, as an identifier"}},
		{"JSON module name not an identifier", map[string]string{"a.modelspec.json": `{"modelspec": "1.0-draft", "module": {"id": "x", "name": "my-app", "version": "1"}, "entities": {"A": {"key": ["id"], "properties": {"id": {"type": "int"}}}}}`}, []string{`module.name "my-app" is not an identifier (letters, digits and _, not starting with a digit); the catalogue refers to a model by it`}},
		{"JSON without entities", map[string]string{"a.modelspec.json": `{"modelspec": "1.0-draft", "module": {"id": "x", "name": "a", "version": "1"}, "enums": {"E": {"values": ["x"]}}}`}, []string{"has no entities; a published model"}},
		{"JSON entity without properties", map[string]string{"a.modelspec.json": `{"modelspec": "1.0-draft", "module": {"id": "x", "name": "a", "version": "1"}, "entities": {"A": {"key": []}}}`}, []string{"has no properties of its own", "has an empty key"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			expect(t, runPublish(tc.files), tc.want...)
		})
	}
	// The same models are clean under the default profile where the standard allows them.
	expect(t, run(map[string]string{"a" + hclExt: "enum \"E\" {\n  values = [\"x\"]\n}\n"}))
	expect(t, run(map[string]string{"a.modelspec.json": `{"modelspec": "1.0-draft", "module": {"id": "x", "version": "1"}, "enums": {"E": {"values": ["x"]}}}`}))
}

func TestProfiles(t *testing.T) {
	t.Parallel()
	for _, p := range Profiles {
		got, err := ParseProfile(string(p))
		if err != nil || got != p {
			t.Errorf("ParseProfile(%q) = %q, %v", p, got, err)
		}
	}
	if _, err := ParseProfile("strict"); err == nil || !strings.Contains(err.Error(), `unknown profile "strict"`) {
		t.Errorf("ParseProfile(strict) = %v", err)
	}
	if !(Options{Profile: ProfilePublish}).publish() || (Options{}).publish() || (Options{Profile: ProfileDefault}).publish() {
		t.Error("publish() wrong")
	}
}

// The vocabularies are the standard's. Each is pinned so that a silent change to
// one is a failing test.
func TestVocabularies(t *testing.T) {
	t.Parallel()
	sorted := func(m map[string]bool) string {
		var keys []string
		for k := range m {
			keys = append(keys, k)
		}
		sortStrings(keys)
		return strings.Join(keys, " ")
	}
	if got, want := sorted(PrimitiveTypes), "any bool date datetime decimal document float int json string time uuid"; got != want {
		t.Errorf("types = %s, want %s", got, want)
	}
	if got, want := sorted(ReservedNames), "collections components entities enums recordsets"; got != want {
		t.Errorf("reserved names = %s, want %s", got, want)
	}
	constraints := map[string]bool{}
	for k := range constraintAttrs {
		constraints[k] = true
	}
	if got, want := sorted(constraints), "format max_len min_len pattern required unique"; got != want {
		t.Errorf("constraints = %s, want %s", got, want)
	}
	attrNames := func(k Kind) string {
		names := map[string]bool{}
		for n := range memberAttrs(k) {
			names[n] = true
		}
		return sorted(names)
	}
	for kind, want := range map[Kind]string{
		KindEntity:     "component entity enum format max_len min_len pattern required type unique",
		KindComponent:  "component entity enum format max_len min_len pattern required type unique",
		KindCollection: "bind component entity enum format max_len min_len pattern required type unique",
		KindRecordset:  "bind enum format max_len min_len pattern required source type unique",
	} {
		if got := attrNames(kind); got != want {
			t.Errorf("%s member attributes = %s, want %s", kind, got, want)
		}
	}
	conceptNames := func(k Kind) string {
		names := map[string]bool{}
		for n := range conceptAttrs[k] {
			names[n] = true
		}
		return sorted(names)
	}
	for kind, want := range map[Kind]string{KindEntity: "key use", KindComponent: "", KindEnum: "values", KindCollection: "kind query source", KindRecordset: "key query"} {
		if got := conceptNames(kind); got != want {
			t.Errorf("%s attributes = %q, want %q", kind, got, want)
		}
	}
}

func TestHasErrorsAndOrder(t *testing.T) {
	t.Parallel()
	if HasErrors(nil) || HasErrors([]Finding{{Severity: SeverityWarning}}) || !HasErrors([]Finding{{Severity: SeverityWarning}, {Severity: SeverityError}}) {
		t.Fatal("HasErrors wrong")
	}
	fs := []Finding{
		{File: "b", Line: 1, Rule: "x", Message: "m"},
		{File: "a", Line: 2, Rule: "x", Message: "m"},
		{File: "a", Line: 1, Rule: "y", Message: "m"},
		{File: "a", Line: 1, Rule: "x", Message: "z"},
		{File: "a", Line: 1, Rule: "x", Message: "a"},
		{File: "a", Rule: "x", Message: "unlocated", Severity: SeverityWarning},
	}
	SortFindings(fs)
	want := []string{"a:unlocated", "a:1:a", "a:1:z", "a:1:m", "a:2:m", "b:1:m"}
	for i, f := range fs {
		got := f.File
		if f.Line > 0 {
			got += ":" + string(rune('0'+f.Line))
		} else {
			got += ":" + f.Message
			if f.String() != "a: warning: unlocated [x]" {
				t.Errorf("String() = %q", f.String())
			}
			continue
		}
		got += ":" + f.Message
		if got != want[i] {
			t.Errorf("order[%d] = %q, want %q", i, got, want[i])
		}
	}
	if s := (Finding{File: "f", Line: 3, Rule: "r", Severity: SeverityError, Message: "m"}).String(); s != "f:3: error: m [r]" {
		t.Errorf("String() = %q", s)
	}
}
