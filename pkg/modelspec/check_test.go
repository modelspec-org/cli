package modelspec

import "testing"

const hclExt = ".modelspec.hcl"

func TestCheckHCL(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		src  string
		want []string
	}{
		{"clean", okEntity, nil},
		{"reserved name", `entity "entities" {
  key = ["id"]
  property "id" {
    type = "int"
  }
}
`, []string{`:1: error: entity name "entities" is a reserved kind token`}},
		{"dotted names", `enum "a.b" {
  values = ["x"]
}
entity "A" {
  key = ["id"]
  property "id" {
    type = "int"
  }
  property "x.y" {
    type = "int"
  }
}
`, []string{`enum name "a.b" must not contain a dot`, `property name "x.y" must not contain a dot`}},
		{"non-identifier names", `entity "9a" {
  key = ["id"]
  property "id" {
    type = "int"
  }
}
recordset "r" {
  column "Total Sales" {
    type = "int"
  }
}
`, []string{`entity name "9a" must be an identifier`}},
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
		{"enum with values not a list", "enum \"E\" {\n  values = \"a\"\n}\n", []string{`enum "E": "values" must be a list of strings`}},
		{"enum empty and duplicate", "enum \"E\" {\n  values = []\n}\nenum \"F\" {\n  values = [\"a\", \"a\"]\n}\n", []string{`enum "E" must declare at least one value`, `enum "F" has duplicate value "a"`}},
		{"unsupported concept attribute", "component \"C\" {\n  x = 1\n}\n", []string{`component "C" has unsupported attribute "x"`}},
		{"attribute value types", `entity "A" {
  key = ["id"]
  property "id" {
    type     = "int"
    required = "yes"
    unique   = 1
    min_len  = -1
    max_len  = 1.5
    pattern  = true
    format   = ["x"]
    enum     = 5
  }
}
`, []string{`"required" must be true or false`, `"unique" must be true or false, not a number`, `"min_len" must be a non-negative integer`, `"max_len" must be a non-negative integer`, `"pattern" must be a string, not a boolean`, `"format" must be a string, not an array`, `"enum" must be the name of an enum or a list of string values`}},
		{"unsupported member attribute", "entity \"A\" {\n  key = [\"id\"]\n  property \"id\" {\n    type = \"int\"\n    bind = \"x\"\n  }\n}\n", []string{`entity "A" property "id" has unsupported attribute "bind"`}},
		{"entity without key", "entity \"A\" {\n  property \"id\" {\n    type = \"int\"\n  }\n}\n", []string{`:1: error: entity "A" has no key`}},
		{"entity key not a list", "entity \"A\" {\n  key = \"id\"\n  property \"id\" {\n    type = \"int\"\n  }\n}\n", []string{`"key" must be a list of strings`}},
		{"entity empty key", "entity \"A\" {\n  key = []\n  property \"id\" {\n    type = \"int\"\n  }\n}\n", []string{`:2: error: entity "A" has an empty key`}},
		{"entity key not a property", "entity \"A\" {\n  key = [\"ghost\"]\n  property \"id\" {\n    type = \"int\"\n  }\n}\n", []string{`entity "A" key "ghost" is not a property of the entity`}},
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
		{"member kind", `entity "A" {
  key = ["id"]
  property "id" {
    type = "int"
  }
  property "none" {
    required = true
  }
  property "two" {
    type   = "int"
    entity = "A"
  }
}
`, []string{`property "none" must have exactly one of type, entity or component`, `property "two" must have exactly one of type, entity or component`}},
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
`, []string{`property "ok2" uses a component`, `property "bad2" uses a component`, `property "bad1" entity reference "C": "C" is a component, not an entity`, `property "bad2" component reference "Nope" does not resolve to a component`, `property "bad3" enum reference "A": "A" is an entity, not an enum`, `property "bad4" enum must declare at least one value`, `property "bad5" enum has duplicate value "a"`}},
		{"component members", "component \"C\" {\n  field \"f\" {\n    type = \"nope\"\n  }\n}\n", []string{`component "C" field "f" has type "nope"`}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			expect(t, run(map[string]string{"a" + hclExt: tc.src}), tc.want...)
		})
	}
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
enum "Role" {
  values = ["a"]
}
`
	booking := func(refs string) string {
		return "entity \"Booking\" {\n  key = [\"id\"]\n  property \"id\" {\n    type = \"int\"\n  }\n" + refs + "}\n"
	}
	prop := func(name, attr, ref string) string {
		return "  property \"" + name + "\" {\n    " + attr + " = \"" + ref + "\"\n  }\n"
	}
	tests := []struct {
		name  string
		files map[string]string
		want  []string
	}{
		{"qualified references resolve", map[string]string{"core" + hclExt: core, "booking" + hclExt: booking(prop("s", "entity", "core.Space") + prop("w", "component", "core.Window") + prop("r", "type", "string"))},
			[]string{`property "w" uses a component`}},
		{"qualified enum reference", map[string]string{"core" + hclExt: core, "booking" + hclExt: booking("  property \"r\" {\n    type = \"string\"\n    enum = \"core.Role\"\n  }\n")}, nil},
		{"self-qualified reference", map[string]string{"booking" + hclExt: booking(prop("s", "entity", "booking.Booking"))}, nil},
		{"unknown module", map[string]string{"booking" + hclExt: booking(prop("s", "entity", "core.Space"))}, []string{`reference "core.Space": unknown module "core"`}},
		{"unknown concept in module", map[string]string{"core" + hclExt: core, "booking" + hclExt: booking(prop("s", "entity", "core.Nope"))}, []string{`reference "core.Nope": unknown entity "Nope" in module "core"`}},
		{"wrong kind in module", map[string]string{"core" + hclExt: core, "booking" + hclExt: booking(prop("s", "entity", "core.Role"))}, []string{`reference "core.Role": "Role" is an enum, not an entity`}},
		{"ambiguous module", map[string]string{"a/core" + hclExt: core, "b/core" + hclExt: core, "booking" + hclExt: booking(prop("s", "entity", "core.Space"))}, []string{`module "core" is ambiguous (declared by a/core.modelspec.hcl, b/core.modelspec.hcl)`}},
		{"reference into a broken module is not reported", map[string]string{"core" + hclExt: "entity {", "booking" + hclExt: booking(prop("s", "entity", "core.Space"))}, []string{"core.modelspec.hcl:1: error: "}},
		{"malformed qualified names", map[string]string{"booking" + hclExt: booking(prop("a", "entity", "x.y.z") + prop("b", "entity", ".y") + prop("c", "entity", "x."))},
			[]string{`"x.y.z" is not a bare name or a <module>.<Name> reference`, `".y" is not a bare name`, `"x." is not a bare name`}},
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
}
collection "view" {
  kind  = "computed"
  query = "from tasks"
}
recordset "r" {
  key   = ["id"]
  query = "q"
  column "id" {
    type = "int"
    bind = "Task.id"
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
    required = true
  }
}
`, []string{`key "ghost" is not one of its columns`, `column "a" has type "nope"`, `bind "Task.nope": entity "Task" has no property "nope"`, `column "a" has unsupported attribute "required"`}},
		{"recordset key not a list", task + "recordset \"r\" {\n  key = \"id\"\n}\n", []string{`"key" must be a list of strings`}},
		{"bind with bare ref is the field's own module", task + `collection "c" {
  kind = "editable"
  field "a" {
    type = "int"
    bind = "a.Task.id"
  }
}
`, nil},
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
	src := "entity \"A\" {\n  key = [\"id\"]\n  property \"id\" {\n    type = \"int\"\n  }\n}\ncollection \"c\" {\n  kind = \"editable\"\n  field \"f\" {\n    type = \"int\"\n    bind = \"core.X.id\"\n  }\n}\n"
	expect(t, run(map[string]string{"a" + hclExt: src, "core" + hclExt: "entity {"}), "core.modelspec.hcl:1: error: ")
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
