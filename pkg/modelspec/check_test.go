package modelspec

import (
	"strings"
	"testing"
)

const hclExt = ".modelspec.hcl"

func member(name, body string) string {
	return "  field \"" + name + "\" {\n" + body + "  }\n"
}

func recordWith(name string, props ...string) string {
	return "record \"" + name + "\" {\n  key = [\"id\"]\n  field \"id\" {\n    type = \"int\"\n  }\n" + strings.Join(props, "") + "}\n"
}

func TestCheckHCL(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		src  string
		want []string
	}{
		{"clean", okRecord, nil},
		{"empty file", "", nil},
		{"reserved name", `record "records" {
  key = ["id"]
  field "id" {
    type = "int"
  }
}
`, []string{`:1: error: record name "records" is a reserved kind token`}},
		{"all six reserved names", "enum \"components\" {\n  values = [\"a\"]\n}\ncomponent \"enums\" {\n}\nenum \"collections\" {\n  values = [\"a\"]\n}\ncomponent \"recordsets\" {\n}\nrecord \"records\" {\n  key = []\n}\nrecord \"entities\" {\n}\n", []string{`enum name "components"`, `component name "enums"`, `enum name "collections"`, `component name "recordsets"`, `record name "records"`, `record name "entities"`, "has an empty key"}},
		{"a dot in a concept name", "enum \"a.b\" {\n  values = [\"x\"]\n}\n", []string{`enum name "a.b" must not contain a dot`}},
		{"names are not restricted to identifiers", `record "Order-Item" {
  key = ["unit-price"]
  field "unit-price" {
    type = "decimal"
  }
  field "my prop" {
    type = "string"
  }
  field "a.b" {
    type = "string"
  }
}
component "my component" {
  field "created-at" {
    type = "datetime"
  }
}
enum "9lives" {
  values = ["a"]
}
`, nil},
		{"duplicate concepts", okRecord + okRecord + `enum "A" {
  values = ["x"]
}
`, []string{`:7: error: duplicate concept name "A" in the record/component/enum scope (also declared at line 1)`, `:13: error: duplicate concept name "A" in the record/component/enum scope (also declared at line 1)`}},
		{"enum without values attribute", "enum \"E\" {\n}\n", []string{`enum "E" must declare at least one value`}},
		{"enum with values not a list", "enum \"E\" {\n  values = \"a\"\n}\n", []string{`enum "E": "values" must be a list of strings or integers`}},
		{"enum empty and duplicate", "enum \"E\" {\n  values = []\n}\nenum \"F\" {\n  values = [\"a\", \"a\"]\n}\n", []string{`enum "E" must declare at least one value`, `enum "F" has duplicate value "a"`}},
		{"integer enum values", "enum \"N\" {\n  values = [1, 2, 3]\n}\nenum \"Mixed\" {\n  values = [1, \"1\", -4]\n}\n", nil},
		{"duplicate integer enum values", "enum \"N\" {\n  values = [1, 2, 1]\n}\n", []string{`enum "N" has duplicate value "1"`}},
		{"float and bool enum values", "enum \"F\" {\n  values = [1.5]\n}\nenum \"B\" {\n  values = [true]\n}\n", []string{`"values" must be a list of strings or integers`, `"values" must be a list of strings or integers`}},
		{"inline integer enum", recordWith("A2", member("n", "    type = \"int\"\n    enum = [1, 2]\n")), nil},
		{"unsupported concept attribute", "component \"C\" {\n  x = 1\n}\n", []string{`component "C" has unsupported attribute "x"`}},
		{"attribute value types", recordWith("A2", member("p", `    type     = "int"
    required = "yes"
    unique   = 1
    min_len  = -1
    max_len  = 1.5
    pattern  = true
    format   = ["x"]
    enum     = 5
`)), []string{`"required" must be true or false`, `"unique" must be true or false, not a number`, `"min_len" must be a non-negative integer`, `"max_len" must be a non-negative integer`, `"pattern" must be a string, not a boolean`, `"format" must be a string, not an array`, `"enum" must be the name of an enum or a list of string or integer values`}},
		{"unsupported member attribute", recordWith("A2", member("p", "    type = \"int\"\n    bind = \"x\"\n")), []string{`record "A2" field "p" has unsupported attribute "bind"`}},
		{"record without key", "record \"A\" {\n  field \"id\" {\n    type = \"int\"\n  }\n}\n", nil},
		{"record key not a list", "record \"A\" {\n  key = \"id\"\n  field \"id\" {\n    type = \"int\"\n  }\n}\n", []string{`"key" must be a list of strings`}},
		{"record empty key", "record \"A\" {\n  key = []\n  field \"id\" {\n    type = \"int\"\n  }\n}\n", []string{`:2: error: record "A" has an empty key`}},
		{"record key not a field", "record \"A\" {\n  key = [\"ghost\"]\n  field \"id\" {\n    type = \"int\"\n  }\n}\n", []string{`record "A" key "ghost" is not a field of the record`}},
		{"record key has duplicate fields", "record \"A\" {\n  key = [\"id\", \"id\"]\n  field \"id\" {\n    type = \"int\"\n  }\n}\n", []string{`record "A" key "id" is duplicated`}},
		{"record without fields is fine", "record \"A\" {\n  key = []\n}\nrecord \"B\" {\n  key = [\"x\"]\n}\n", []string{`record "A" has an empty key`, `record "B" key "x" is not a field`}},
		{"key from a used component", `component "C" {
  field "id" {
    type = "int"
  }
}
record "A" {
  key = ["id"]
  use = ["C"]
}
`, nil},
		{"key naming a field a used component lacks", `component "C" {
  field "id" {
    type = "int"
  }
}
record "A" {
  key = ["nope"]
  use = ["C"]
}
`, []string{`key "nope" is not a field of the record (or of a component it uses)`}},
		{"use not a list", "record \"A\" {\n  key = []\n  use = \"C\"\n}\n", []string{`"use" must be a list of strings`, `has an empty key`}},
		{"use unresolved and wrong kind", `enum "E" {
  values = ["a"]
}
record "A" {
  key = ["id"]
  use = ["Missing", "E"]
  field "id" {
    type = "int"
  }
}
`, []string{`record "A" use reference "Missing" does not resolve to a component in module "a"`, `record "A" use reference "E": "E" is an enum, not a component`}},
		{"duplicate fields", "record \"A\" {\n  key = [\"id\"]\n  field \"id\" {\n    type = \"int\"\n  }\n  field \"id\" {\n    type = \"int\"\n  }\n}\n", []string{`:6: error: duplicate field "id" in record "A" (also declared at line 3)`}},
		{"member kind", recordWith("A2", member("none", "    required = true\n"), member("two", "    type   = \"int\"\n    record = \"A2\"\n")), []string{`field "none" must have exactly one of type, record or component`, `field "two" must have exactly one of type, record or component`}},
		{"unknown type", "record \"A\" {\n  key = [\"id\"]\n  field \"id\" {\n    type = \"serial\"\n  }\n}\n", []string{`:4: error: record "A" field "id" has type "serial"`}},
		{"type not a string", "record \"A\" {\n  key = [\"id\"]\n  field \"id\" {\n    type = 1\n  }\n}\n", []string{`"type" must be a string`}},
		{"references", `component "C" {
  field "f" {
    type = "int"
  }
}
enum "E" {
  values = ["a"]
}
record "A" {
  key = ["id"]
  field "id" {
    type = "int"
  }
  field "ok1" {
    record = "A"
  }
  field "ok2" {
    component = "C"
  }
  field "ok3" {
    type = "string"
    enum = "E"
  }
  field "ok4" {
    type = "string"
    enum = ["a", "b"]
  }
  field "bad1" {
    record = "C"
  }
  field "bad2" {
    component = "Nope"
  }
  field "bad3" {
    type = "string"
    enum = "A"
  }
  field "bad4" {
    type = "string"
    enum = []
  }
  field "bad5" {
    type = "string"
    enum = ["a", "a"]
  }
}
`, []string{`field "bad1" record reference "C": "C" is a component, not a record`, `field "bad2" component reference "Nope" does not resolve to a component`, `field "bad3" enum reference "A": "A" is a record, not an enum`, `field "bad4" enum must declare at least one value`, `field "bad5" enum has duplicate value "a"`}},
		{"component members", "component \"C\" {\n  field \"f\" {\n    type = \"nope\"\n  }\n}\n", []string{`component "C" field "f" has type "nope"`}},
		{"component with no member kind", "component \"C\" {\n  field \"f\" {\n  }\n}\n", []string{`component "C" field "f" must have exactly one of type, record or component`}},
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
record "User" {
  key = ["id"]
  field "id" {
    type = "uuid"
  }
}
record "Invoice" {
  key = ["id"]
  use = ["Auditable"]
  field "id" {
    type = "uuid"
  }
  field "total" {
    component = "CurrencyAmount"
  }
  field "status" {
    type = "string"
    enum = "BookingStatus"
  }
  field "owner" {
    record   = "User"
    required = true
  }
}
`
	expect(t, run(map[string]string{"invoice" + hclExt: src}))
}

func TestCheckModules(t *testing.T) {
	t.Parallel()
	core := `record "Space" {
  key = ["id"]
  field "id" {
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
	booking := func(refs ...string) string { return recordWith("Booking", refs...) }
	prop := func(name, attr, ref string) string {
		return member(name, "    "+attr+" = \""+ref+"\"\n")
	}
	tests := []struct {
		name  string
		files map[string]string
		want  []string
	}{
		{"qualified references resolve", map[string]string{"core" + hclExt: core, "booking" + hclExt: booking(prop("s", "record", "core.Space"), prop("w", "component", "core.Window"), member("r", "    type = \"string\"\n    enum = \"core.Role\"\n"))}, nil},
		{"self-qualified reference", map[string]string{"booking" + hclExt: booking(prop("s", "record", "booking.Booking"))}, nil},
		{"unknown module says how to supply it", map[string]string{"booking" + hclExt: booking(prop("s", "record", "core.Space"))}, []string{`reference "core.Space": unknown module "core" (lint the files that declare it together with this one, or name it with --module core=<path>)`}},
		{"unknown concept in module", map[string]string{"core" + hclExt: core, "booking" + hclExt: booking(prop("s", "record", "core.Nope"))}, []string{`reference "core.Nope": unknown record "Nope" in module "core"`}},
		{"wrong kind in module", map[string]string{"core" + hclExt: core, "booking" + hclExt: booking(prop("s", "record", "core.Role"))}, []string{`reference "core.Role": "Role" is an enum, not a record`}},
		{"two different sources claim one module", map[string]string{"a/core" + hclExt: core, "b/core" + hclExt: core, "booking" + hclExt: booking(prop("s", "record", "core.Space"))}, []string{`module "core" is ambiguous: 2 different sources claim it (a/core.modelspec.hcl, b/core.modelspec.hcl)`}},
		{"a reference into a broken module is not reported", map[string]string{"core" + hclExt: "record {", "booking" + hclExt: booking(prop("s", "record", "core.Space"), "  field \"k\" {\n    type = \"int\"\n  }\n")}, []string{"core.modelspec.hcl:1: error: "}},
		{"malformed qualified names", map[string]string{"booking" + hclExt: booking(prop("a", "record", "x.y.z"), prop("b", "record", ".y"), prop("c", "record", "x."))},
			[]string{`"x.y.z" is not a bare name or a <module>.<Name> reference`, `".y" is not a bare name`, `"x." is not a bare name`}},
		// M5: a key or bind sees the fields a component contributes, also from another module.
		{"key through a component of another module", map[string]string{"core" + hclExt: core, "sales" + hclExt: "record \"Order\" {\n  key = [\"id\"]\n  use = [\"core.Identified\"]\n}\n"}, nil},
		{"key naming a field the other module's component lacks", map[string]string{"core" + hclExt: core, "sales" + hclExt: "record \"Order\" {\n  key = [\"zzz\"]\n  use = [\"core.Identified\"]\n}\n"}, []string{`key "zzz" is not a field of the record (or of a component it uses)`}},
		{"key through a component of an unsupplied module is not judged", map[string]string{"sales" + hclExt: "record \"Order\" {\n  key = [\"id\"]\n  use = [\"core.Identified\"]\n}\n"}, []string{`use reference "core.Identified": unknown module "core"`}},
		{"key through a component of a broken module is not judged", map[string]string{"core" + hclExt: "record {", "sales" + hclExt: "record \"Order\" {\n  key = [\"id\"]\n  use = [\"core.Identified\"]\n}\n"}, []string{"core.modelspec.hcl:1: error: "}},
		{"key through a component that does not exist is judged by the reference only", map[string]string{"sales" + hclExt: "record \"Order\" {\n  key = [\"id\"]\n  use = [\"Nope\"]\n}\n"}, []string{`use reference "Nope" does not resolve to a component`}},
		{"key through a used component that is not a list", map[string]string{"sales" + hclExt: "record \"Order\" {\n  key = [\"id\"]\n  use = \"X\"\n}\n"}, []string{`"use" must be a list of strings`}},
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
		layout("sales", "records.modelspec.hcl"): "record \"Order\" {\n  key = [\"id\"]\n  field \"id\" {\n    type = \"int\"\n  }\n  field \"status\" {\n    type = \"string\"\n    enum = \"OrderStatus\"\n  }\n  field \"customer\" {\n    record = \"core.Customer\"\n  }\n}\n",
		layout("sales", "enums.modelspec.hcl"):   "enum \"OrderStatus\" {\n  values = [\"open\"]\n}\n",
		layout("core", "model.modelspec.hcl"):    "record \"Customer\" {\n  key = [\"id\"]\n  field \"id\" {\n    type = \"int\"\n  }\n}\n",
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
			[]string{"extra.modelspec.hcl:1: error: duplicate concept name \"OrderStatus\" in the record/component/enum scope (also declared at " + layout("sales", "enums.modelspec.hcl") + ":1)"}},
		{"the same name in two modules is not a duplicate", copyWith(map[string]string{layout("core", "enums.hcl"): "enum \"OrderStatus\" {\n  values = [\"x\"]\n}\n"}), nil},
		{"a sibling's concept is missing", map[string]string{layout("sales", "records.hcl"): sales[layout("sales", "records.modelspec.hcl")], layout("core", "m.hcl"): sales[layout("core", "model.modelspec.hcl")]},
			[]string{`record "Order" field "status" enum reference "OrderStatus" does not resolve to an enum in module "sales"`}},
		{"a .hcl file outside a layout is not a model file", map[string]string{"models/x.hcl": "record \"A\" {\n}\n"}, []string{"error: no ModelSpec files"}},
		{"a models directory not under modules is not a layout", map[string]string{"models/records.modelspec.hcl": sales[layout("sales", "records.modelspec.hcl")], "models/enums.modelspec.hcl": sales[layout("sales", "enums.modelspec.hcl")]},
			[]string{`enum reference "OrderStatus" does not resolve to an enum in module "records"`, `reference "core.Customer": unknown module "core"`}},
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
	coreHCL := recordWith("Space")
	coreJSON := `{"modelspec": "1.0-draft-2", "module": {"id": "x/core", "name": "core", "version": "1"}, "records": {"Space": {"key": ["id"], "fields": {"id": {"type": "int"}}}}}`
	booking := recordWith("Booking", member("s", "    record = \"core.Space\"\n"))
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
		{"the twin is still linted", map[string]string{"core" + hclExt: coreHCL, "core.modelspec.json": strings.Replace(coreJSON, `"int"`, `"serial"`, 1)}, []string{`core.modelspec.json:1: error: record "Space" field "id" has type "serial"`, "stale twin"}},
		{"a twin's duplicate does not clash with the HCL", map[string]string{"core" + hclExt: coreHCL, "core.modelspec.json": coreJSON}, nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			expect(t, run(tc.files), tc.want...)
		})
	}
}

func TestPublishProfile(t *testing.T) {
	t.Parallel()
	good := recordWith("Order", member("customer", "    record = \"Order\"\n"))
	tests := []struct {
		name  string
		files map[string]string
		want  []string
	}{
		{"a model that meets it", map[string]string{"a" + hclExt: good}, nil},
		{"a record for a physical heap omits its key", map[string]string{"a" + hclExt: "record \"HeapRow\" {\n  field \"value\" {\n    type = \"string\"\n  }\n}\n"}, nil},
		{"JSON record for a physical heap omits its key", map[string]string{"a.modelspec.json": `{"modelspec":"1.0-draft-2","module":{"id":"x","name":"a","version":"1"},"records":{"HeapRow":{"fields":{"value":{"type":"string"}}}}}`}, nil},
		{"no records", map[string]string{"a" + hclExt: "enum \"E\" {\n  values = [\"x\"]\n}\n"}, []string{"a.modelspec.hcl:1: error: has no records; a published model declares at least one record"}},
		{"an empty file", map[string]string{"a" + hclExt: ""}, []string{"has no records; a published"}},
		{"records in a sibling file count", map[string]string{layout("m", "a.hcl"): "enum \"E\" {\n  values = [\"x\"]\n}\n", layout("m", "b.hcl"): good}, nil},
		{"a record with no fields of its own", map[string]string{"a" + hclExt: "component \"C\" {\n  field \"id\" {\n    type = \"int\"\n  }\n}\nrecord \"A\" {\n  key = [\"id\"]\n  use = [\"C\"]\n}\n"}, []string{`record "A" has no fields of its own; the catalogue lists a record by its fields`}},
		{"names that are not identifiers", map[string]string{"a" + hclExt: "record \"Order-Item\" {\n  key = [\"unit-price\"]\n  field \"unit-price\" {\n    type = \"int\"\n  }\n}\ncomponent \"my comp\" {\n  field \"x y\" {\n    type = \"int\"\n  }\n}\n"}, []string{`record name "Order-Item" is not an identifier`, `field name "unit-price" of record "Order-Item" is not an identifier`}},
		{"a component value", map[string]string{"a" + hclExt: "component \"C\" {\n}\n" + recordWith("A", member("c", "    component = \"C\"\n"))}, []string{`record "A" field "c" has a component value; the catalogue lists only scalar`}},
		{"a record reference into another module", map[string]string{"core" + hclExt: recordWith("Space"), "a" + hclExt: recordWith("A", member("s", "    record = \"core.Space\"\n"))}, []string{`refers to "core.Space" in another module; the catalogue resolves record references inside the one model only`}},
		{"a broken module adds no publish finding", map[string]string{"a" + hclExt: "record {"}, []string{"[syntax]"}},
		{"JSON module name missing", map[string]string{"a.modelspec.json": `{"modelspec": "1.0-draft-2", "module": {"id": "x", "version": "1"}, "records": {"A": {"key": ["id"], "fields": {"id": {"type": "int"}}}}}`}, []string{"a.modelspec.json:1: error: has no module.name; a published model names its module, as an identifier"}},
		{"JSON module name not an identifier", map[string]string{"a.modelspec.json": `{"modelspec": "1.0-draft-2", "module": {"id": "x", "name": "my-app", "version": "1"}, "records": {"A": {"key": ["id"], "fields": {"id": {"type": "int"}}}}}`}, []string{`module.name "my-app" is not an identifier (letters, digits and _, not starting with a digit); the catalogue refers to a model by it`}},
		{"JSON without records", map[string]string{"a.modelspec.json": `{"modelspec": "1.0-draft-2", "module": {"id": "x", "name": "a", "version": "1"}, "enums": {"E": {"values": ["x"]}}}`}, []string{"has no records; a published model"}},
		{"JSON record without fields", map[string]string{"a.modelspec.json": `{"modelspec": "1.0-draft-2", "module": {"id": "x", "name": "a", "version": "1"}, "records": {"A": {"key": []}}}`}, []string{"has no fields of its own", "has an empty key"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			expect(t, runPublish(tc.files), tc.want...)
		})
	}
	// The same models are clean under the default profile where the standard allows them.
	expect(t, run(map[string]string{"a" + hclExt: "enum \"E\" {\n  values = [\"x\"]\n}\n"}))
	expect(t, run(map[string]string{"a.modelspec.json": `{"modelspec": "1.0-draft-2", "module": {"id": "x", "version": "1"}, "enums": {"E": {"values": ["x"]}}}`}))
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
	if got, want := sorted(ReservedNames), "collections components entities enums records recordsets"; got != want {
		t.Errorf("reserved names = %s, want %s", got, want)
	}
	constraints := map[string]bool{}
	for k := range constraintAttrs {
		constraints[k] = true
	}
	if got, want := sorted(constraints), "format max_len min_len pattern required unique"; got != want {
		t.Errorf("constraints = %s, want %s", got, want)
	}
	names := map[string]bool{}
	for n := range memberAttrs {
		names[n] = true
	}
	if got, want := sorted(names), "component enum format max_len min_len pattern record required type unique"; got != want {
		t.Errorf("member attributes = %s, want %s", got, want)
	}
	conceptNames := func(k Kind) string {
		names := map[string]bool{}
		for n := range conceptAttrs[k] {
			names[n] = true
		}
		return sorted(names)
	}
	for kind, want := range map[Kind]string{KindRecord: "key use", KindComponent: "", KindEnum: "values"} {
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

// A twin that is not what its HCL exports to is a warning, and the lone JSON file
// does not hide it.
func TestStaleTwins(t *testing.T) {
	t.Parallel()
	hclSrc := recordWith("Space")
	jsonOf := func(record, key string) string {
		return `{"modelspec": "1.0-draft-2", "module": {"id": "x/core", "name": "core", "version": "1"}, "records": {"` + record + `": {"key": ["` + key + `"], "fields": {"id": {"type": "int"}}}}}`
	}
	tests := []struct {
		name  string
		files map[string]string
		want  []string
	}{
		{"the exact export", map[string]string{"core" + hclExt: hclSrc, "core.modelspec.json": jsonOf("Space", "id")}, nil},
		{"a different record", map[string]string{"core" + hclExt: hclSrc, "core.modelspec.json": jsonOf("Room", "id")}, []string{`core.modelspec.json:1: warning: stale twin: core.modelspec.json is not what core.modelspec.hcl exports to (records has key "Space" in the first but not in the second); run modelspec export [stale-twin]`}},
		{"references from JSON resolve against the HCL, so a missing concept is found there", map[string]string{"core" + hclExt: hclSrc, "core.modelspec.json": jsonOf("Room", "id"), "booking.modelspec.json": `{"modelspec": "1.0-draft-2", "module": {"id": "x/b", "name": "booking", "version": "1"}, "records": {"B": {"key": ["id"], "fields": {"id": {"type": "int"}, "s": {"record": "core.Space"}}}}}`}, []string{"stale twin"}},
		{"a broken HCL is reported by its own findings", map[string]string{"core" + hclExt: "record {", "core.modelspec.json": jsonOf("Room", "id")}, []string{"core.modelspec.hcl:1: error"}},
		{"a JSON without an identity is reported by its own findings", map[string]string{"core" + hclExt: hclSrc, "core.modelspec.json": `{"modelspec": "1.0-draft-2", "module": {"name": "core"}, "records": {}}`}, []string{"has no module.id", "has no module.version"}},
		{"a JSON that is not an object has no module", map[string]string{"core" + hclExt: hclSrc, "core.modelspec.json": `{"modelspec": "1.0-draft-2", "module": 1}`}, []string{"has no module object"}},
		{"a twin in a layout directory of several files is not compared", map[string]string{layout("m", "a.hcl"): hclSrc, layout("m", "b.hcl"): recordWith("Other"), layout("m", "m.modelspec.json"): jsonOf("Room", "id")}, nil},
		{"the twin's own module.name is its identity, not compared with the module's name", map[string]string{"core" + hclExt: hclSrc, "core.modelspec.json": strings.Replace(jsonOf("Space", "id"), `"name": "core"`, `"name": "Core"`, 1)}, nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			expect(t, run(tc.files), tc.want...)
		})
	}
}

// A twin's own module.name is checked against the module, not discarded: both
// the module's name and the JSON's own name resolve to the module.
func TestTwinNames(t *testing.T) {
	t.Parallel()
	hclSrc := "record \"Task\" {\n  key = [\"id\"]\n  field \"id\" {\n    type = \"int\"\n  }\n  field \"parent\" {\n    record = \"todo.Task\"\n  }\n}\n"
	jsonSrc := func(ref string) string {
		return `{"modelspec": "1.0-draft-2", "module": {"id": "x/todo", "name": "Todo", "version": "1"}, "records": {"Task": {"key": ["id"], "fields": {"id": {"type": "int"}, "parent": {"record": "` + ref + `"}}}}}`
	}
	// Both names resolve inside the twin. (The HCL cannot be exported under the
	// name Todo while it refers to itself as todo, so there is nothing to compare.)
	expect(t, run(map[string]string{"todo" + hclExt: hclSrc, "todo.modelspec.json": jsonSrc("Todo.Task")}))
	expect(t, run(map[string]string{"todo" + hclExt: hclSrc, "todo.modelspec.json": jsonSrc("todo.Task")}))
	// Alone, the JSON's own name is the module's.
	expect(t, run(map[string]string{"todo.modelspec.json": jsonSrc("Todo.Task")}))
	expect(t, run(map[string]string{"todo.modelspec.json": jsonSrc("todo.Task")}), `unknown module "todo"`)
	// A reference to a name that is neither is unknown, twin or not.
	expect(t, run(map[string]string{"todo" + hclExt: strings.Replace(hclSrc, "todo.Task", "Todo.Task", 1), "todo.modelspec.json": jsonSrc("Nope.Task")}), `unknown module "Todo"`, `unknown module "Nope"`, "stale twin")
}

// A module's own name resolves to itself even when another source claims the
// name too: the ambiguity is for the others who refer to it.
func TestOwnNameWinsOverAnAmbiguousOne(t *testing.T) {
	t.Parallel()
	self := "record \"Space\" {\n  key = [\"id\"]\n  field \"id\" {\n    type = \"int\"\n  }\n  field \"up\" {\n    record = \"core.Space\"\n  }\n}\n"
	other := recordWith("Space")
	booking := recordWith("Booking", member("s", "    record = \"core.Space\"\n"))
	expect(t, run(map[string]string{"a/core" + hclExt: self, "b/core" + hclExt: other}))
	expect(t, run(map[string]string{"a/core" + hclExt: self, "b/core" + hclExt: other, "booking" + hclExt: booking}), `module "core" is ambiguous`)
}

func TestEmptyAndBlankNames(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		src  string
		want []string
	}{
		{"empty record name", "record \"\" {\n  key = []\n}\n", []string{`record name must not be empty or blank; it could never be referenced`, "has an empty key"}},
		{"blank record name", "record \" \" {\n  key = []\n}\n", []string{`record name must not be empty or blank`, "has an empty key"}},
		{"empty field name", "record \"A\" {\n  key = [\"id\"]\n  field \"id\" {\n    type = \"int\"\n  }\n  field \"\" {\n    type = \"int\"\n  }\n}\n", []string{`field name must not be empty or blank in record "A"`}},
		{"tab as a field name", "component \"C\" {\n  field \"\t\" {\n    type = \"int\"\n  }\n}\n", []string{`field name must not be empty or blank in component "C"`}},
		{"empty enum name", "enum \"\" {\n  values = [\"a\"]\n}\n", []string{"enum name must not be empty"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			expect(t, run(map[string]string{"a" + hclExt: tc.src}), tc.want...)
		})
	}
}

// Names in one scope that differ only by case are a warning: valid, but they
// collide on a case-insensitive store.
func TestNamesThatDifferOnlyByCase(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		files map[string]string
		want  []string
	}{
		{"two records", map[string]string{"a" + hclExt: recordWith("User") + recordWith("USER")}, []string{`warning: record name "USER" differs only by case from "User" (declared at line 1) in the record/component/enum scope`}},
		{"a record and an enum", map[string]string{"a" + hclExt: recordWith("Status") + "enum \"status\" {\n  values = [\"a\"]\n}\n"}, []string{`enum name "status" differs only by case from "Status"`}},
		{"across the files of a module", map[string]string{layout("m", "a.hcl"): recordWith("User"), layout("m", "b.hcl"): recordWith("user")}, []string{`b.hcl:1: warning: record name "user" differs only by case from "User" (declared at ` + layout("m", "a.hcl") + `:1)`}},
		{"in different modules it is fine", map[string]string{layout("m", "a.hcl"): recordWith("User"), layout("n", "a.hcl"): recordWith("user")}, nil},
		{"fields of one record", map[string]string{"a" + hclExt: recordWith("A", member("Name", "    type = \"string\"\n"), member("name", "    type = \"string\"\n"))}, []string{`field name "name" differs only by case from "Name" in record "A"`}},
		{"an exact duplicate is an error, not also a warning", map[string]string{"a" + hclExt: recordWith("User") + recordWith("User")}, []string{"duplicate concept name"}},
		{"an exact duplicate field is an error, not also a warning", map[string]string{"a" + hclExt: recordWith("A", member("x", "    type = \"int\"\n"), member("x", "    type = \"int\"\n"))}, []string{"duplicate field"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			expect(t, run(tc.files), tc.want...)
		})
	}
}

// A record's fields come from its own and from the components it uses.
// Lookups go through the used components one by one, and through one merged set
// once there have been enough of them; both must give the same answers.
func TestFieldLookupsThroughUsedComponents(t *testing.T) {
	t.Parallel()
	comp := func(name string, fields ...string) string {
		out := "component \"" + name + "\" {\n"
		for _, f := range fields {
			out += "  field \"" + f + "\" {\n    type = \"int\"\n  }\n"
		}
		return out + "}\n"
	}
	components := comp("A", "a1", "a2") + comp("B", "b1") + comp("C", "c1", "c2", "c3")
	record := func(keys string) string {
		return "record \"E\" {\n  use = [\"A\", \"B\", \"C\"]\n  key = [" + keys + "]\n  field \"own\" {\n    type = \"int\"\n  }\n}\n"
	}
	// One lookup, then several: the first are answered component by component, the
	// later ones from the merged set.
	expect(t, run(map[string]string{"m" + hclExt: components + record(`"own"`)}))
	expect(t, run(map[string]string{"m" + hclExt: components + record(`"a1", "b1", "c3", "own", "a2", "c1", "c2"`)}))
	expect(t, run(map[string]string{"m" + hclExt: components + record(`"a1", "nope", "b1", "c3", "a2", "missing", "c1"`)}),
		`key "nope" is not a field`, `key "missing" is not a field`)
}

// The strategy behind a record's field lookups, which gives the same answers
// either way, is as designed: a record's fields are built once, the fields of
// each used component are counted, and the merged set is built only when the
// lookups through the used components would cost more than building it.
func TestPropSetStrategy(t *testing.T) {
	t.Parallel()
	src := "component \"A\" {\n  field \"a1\" {\n    type = \"int\"\n  }\n  field \"a2\" {\n    type = \"int\"\n  }\n}\n" +
		"component \"B\" {\n  field \"b1\" {\n    type = \"int\"\n  }\n}\n" +
		"record \"E\" {\n  use = [\"A\", \"nomodule.X\", \"B\", \"A\", \"Missing\", \"B\"]\n  key = [\"own\"]\n  field \"own\" {\n    type = \"int\"\n  }\n}\n" +
		"record \"F\" {\n  use = [\"A\", \"B\"]\n  key = [\"own\"]\n}\n" +
		"component \"A2\" {\n  field \"a1\" {\n    type = \"int\"\n  }\n  field \"a2\" {\n    type = \"int\"\n  }\n}\n" +
		"record \"G\" {\n  use = [\"A\", \"A2\"]\n  key = []\n}\n"
	m, fs := ParseHCL("a"+hclExt, []byte(src))
	if len(fs) != 0 {
		t.Fatal(fs)
	}
	u := &unit{name: "a", models: []*Model{m}}
	c := &checker{byName: map[string][]*unit{"a": {u}}, props: map[*Concept]*propSet{}, memberSets: map[*Concept]map[string]bool{}}
	record := u.find(KindRecord, "E")
	p := c.fieldNames(u, record)
	if c.fieldNames(u, record) != p {
		t.Error("a record's fields are built more than once")
	}
	// Two of the six used names are a component twice, one is of an unknown module and one is missing (so the set is not complete).
	if p.complete || len(p.used) != 4 || p.fields != 2+1+2+1 || !p.own["own"] || len(p.own) != 1 {
		t.Fatalf("complete %v, used %d, fields %d, own %v", p.complete, len(p.used), p.fields, p.own)
	}
	// Four used components of six fields: lookups go through them while
	// lookups x 4 components <= 6 fields + 4, that is for the first two, and the
	// merged set is built by the third.
	for i, name := range []string{"a1", "b1"} {
		if !p.has(name) || p.union != nil {
			t.Fatalf("lookup %d of %q: merged set built %v, it is built only from the 3rd lookup", i+1, name, p.union != nil)
		}
	}
	if p.has("nope") || p.union == nil || len(p.union) != 3 {
		t.Fatalf("the 3rd lookup builds the merged set once, with the 3 names of the used components: %v", p.union)
	}
	if !p.has("own") || p.has("zzz") {
		t.Error("answers changed once the merged set was built")
	}
	// The merged set is built when the lookups cost strictly more: two components of two fields
	// each give 3 lookups at 6 = 4 + 2, and the 4th builds it.
	two := c.fieldNames(u, u.find(KindRecord, "G"))
	for i := 1; i <= 3; i++ {
		if two.has("zz") || two.union != nil {
			t.Fatalf("G: lookup %d: merged set built %v, it is built by the 4th", i, two.union != nil)
		}
	}
	if two.has("zz") || two.union == nil || !two.has("a1") {
		t.Fatalf("G: the 4th lookup builds the merged set: %v", two.union)
	}
	// With no component to look through, nothing is built.
	plain := c.fieldNames(u, u.find(KindRecord, "F"))
	if !plain.complete || len(plain.used) != 2 || plain.fields != 3 || plain.has("a1") != true || plain.union != nil {
		t.Fatalf("F: %+v", plain)
	}
}

// HCL keeps the final line break of a heredoc, so a heredoc is never a valid name: the
// finding for a name that ends with a line break says so and says to write a quoted
// string. JSON has no heredoc and gets no such hint, and neither does a finding about a
// value that is not a name (an enum value), or a name that does not end with a line break.
func TestAHeredocInANamePositionSaysToUseAQuotedString(t *testing.T) {
	t.Parallel()
	const hint = "the name ends with a line break; HCL keeps the final line break of a heredoc, so a heredoc is never a valid name: write it as a quoted string"
	record := func(field string) string {
		return "record \"A\" {\n  key = [\"id\"]\n  field \"id\" {\n    type = \"int\"\n  }\n  field \"p\" {\n    " + field + "\n  }\n}\n"
	}
	for name, tc := range map[string]struct{ src, want string }{
		"type":      {record("type = <<EOT\nstring\nEOT"), `has type "string\n", which is not a ModelSpec type`},
		"record":    {record("record = <<EOT\nA\nEOT"), `record reference "A\n" does not resolve`},
		"component": {record("component = <<EOT\nC\nEOT"), `component reference "C\n" does not resolve`},
		"key":       {"record \"A\" {\n  key = [<<EOT\nid\nEOT\n  ]\n  field \"id\" {\n    type = \"int\"\n  }\n}\n", `key "id\n" is not a field`},
	} {
		got := run(map[string]string{"m.modelspec.hcl": tc.src})
		found := false
		for _, g := range got {
			if strings.Contains(g, tc.want) {
				found = true
				if !strings.Contains(g, hint) {
					t.Errorf("%s: no hint in %s", name, g)
				}
			}
		}
		if !found {
			t.Errorf("%s: no finding contains %q: %v", name, tc.want, got)
		}
	}
	// No hint where there is no heredoc to blame, or the value is not a name.
	for name, got := range map[string][]string{
		"JSON":            run(map[string]string{"m.modelspec.json": doc(`"records": {"A": {"key": ["id"], "fields": {"id": {"type": "string\n"}}}}`)}),
		"an enum value":   run(map[string]string{"m.modelspec.hcl": "enum \"E\" {\n  values = [<<EOT\nx\nEOT\n, <<EOT\nx\nEOT\n]\n}\n"}),
		"an unknown type": run(map[string]string{"m.modelspec.hcl": record(`type = "nope"`)}),
	} {
		if len(got) == 0 {
			t.Errorf("%s: no finding", name)
		}
		for _, g := range got {
			if strings.Contains(g, "heredoc") {
				t.Errorf("%s: %s", name, g)
			}
		}
	}
}

// A file in the old spelling gets one warning, at the line of the first old
// spelling, with how many there are; it never changes whether the model is valid.
func TestDeprecatedSpelling(t *testing.T) {
	t.Parallel()
	const old = `# The first old spelling is on line 9.

record "New" {
  key = ["id"]
  field "id" {
    type = "int"
  }
}
entity "Old" {
  key = ["id"]
  field "id" {
    type = "int"
  }
  property "r" {
    entity = "New"
  }
}
`
	const warning = `: warning: holds 3 old spellings: entity, property and entity = are the old spellings of record, field and record = (decision 0018, decision 0020); modelspec rewrite "a.modelspec.hcl" rewrites the file [deprecated-spelling]`
	// Under both profiles.
	expect(t, run(map[string]string{"a" + hclExt: old}), "a.modelspec.hcl:9"+warning)
	expect(t, runPublish(map[string]string{"a" + hclExt: old}), "a.modelspec.hcl:9"+warning)
	expect(t, run(map[string]string{"a" + hclExt: "record \"A\" {\n  key = [\"p\"]\n  property \"p\" {\n    type = \"int\"\n  }\n}\n"}), `a.modelspec.hcl:3: warning: holds 1 old spelling: entity, property and entity =`)
	// In JSON the finding is at the line of the identifier, which need not be the first key.
	const key = `"entities": {"A": {"key": [], "properties": {}}}`
	src := "{\n\"module\": {\"id\": \"x\", \"version\": \"1\"},\n" + key + ",\n\"modelspec\": \"1.0-draft\"\n}"
	jsonWarning := func(n string) string {
		return `: warning: is in format 1.0-draft and holds ` + n + `: that identifier, and the keys entities, properties and entity, are the old spellings of 1.0-draft-2, records, fields and record (decision 0018, decision 0020); modelspec rewrite "a.modelspec.json" rewrites the file [deprecated-spelling]`
	}
	expect(t, run(map[string]string{"a" + jsonExt: src}), "a.modelspec.json:4"+jsonWarning("3 old spellings"), "has an empty key")
	expect(t, run(map[string]string{"a" + jsonExt: oldDoc()}), "a.modelspec.json:2"+jsonWarning("1 old spelling"))
	// Nothing to say about the new spelling, and a file that was not read has no spellings.
	expect(t, run(map[string]string{"a" + hclExt: okRecord, "b" + jsonExt: doc(jRecords)}))
	expect(t, run(map[string]string{"a" + hclExt: "entity {"}), "a.modelspec.hcl:1: error")
	// It is a warning: the model is as valid as without it, and the severity is decided in one place.
	res, err := Lint(newMemFS(map[string]string{"a" + hclExt: old}), []string{"."}, LintOptions{})
	if err != nil || len(res.Findings) != 1 || res.Findings[0].Severity != OldSpellingSeverity || HasErrors(res.Findings) {
		t.Fatalf("findings = %v, %v", res.Findings, err)
	}
	if OldSpellingSeverity != SeverityWarning {
		t.Errorf("OldSpellingSeverity = %s; the old spelling is a warning until the owner's later step", OldSpellingSeverity)
	}
}

// An HCL file and a JSON twin in different vocabularies are a stale twin, and the
// message says that modelspec rewrite brings them in line.
func TestStaleTwinInAnotherVocabulary(t *testing.T) {
	t.Parallel()
	const note = "; the two are in different vocabularies (the old entities, properties and entity, and the new records, fields and record), and modelspec rewrite on both files brings the pair in line [stale-twin]"
	oldHCL := strings.NewReplacer("record", "entity", "field", "property").Replace(recordWith("Space"))
	newJSON := `{"modelspec": "1.0-draft-2", "module": {"id": "x/core", "name": "core", "version": "1"}, "records": {"Space": {"key": ["id"], "fields": {"id": {"type": "int"}}}}}`
	oldJSON := `{"modelspec": "1.0-draft", "module": {"id": "x/core", "name": "core", "version": "1"}, "entities": {"Space": {"key": ["id"], "properties": {"id": {"type": "int"}}}}}`
	expect(t, run(map[string]string{"core" + hclExt: recordWith("Space"), "core.modelspec.json": oldJSON}),
		`core.modelspec.json:1: warning: stale twin: core.modelspec.json is not what core.modelspec.hcl exports to (modelspec is "1.0-draft-2" in the first and "1.0-draft" in the second); run modelspec export`+note, "[deprecated-spelling]")
	expect(t, run(map[string]string{"core" + hclExt: oldHCL, "core.modelspec.json": newJSON}),
		`core.modelspec.json:1: warning: stale twin: core.modelspec.json is not what core.modelspec.hcl exports to (modelspec is "1.0-draft" in the first and "1.0-draft-2" in the second); run modelspec export`+note, "core.modelspec.hcl:1: warning: holds 2 old spellings")
	// The same vocabulary, or an identifier it does not know: no note.
	for name, files := range map[string]map[string]string{
		"old":     {"core" + hclExt: oldHCL, "core.modelspec.json": strings.Replace(oldJSON, `"type": "int"`, `"type": "string"`, 1)},
		"new":     {"core" + hclExt: recordWith("Space"), "core.modelspec.json": strings.Replace(newJSON, `"type": "int"`, `"type": "string"`, 1)},
		"unknown": {"core" + hclExt: recordWith("Space"), "core.modelspec.json": strings.Replace(newJSON, `1.0-draft-2`, `3`, 1)},
	} {
		got := run(files)
		found := false
		for _, g := range got {
			found = found || strings.Contains(g, "stale twin")
			if strings.Contains(g, "different vocabularies") {
				t.Errorf("%s: a note where there is none: %s", name, g)
			}
		}
		if name != "unknown" && !found {
			t.Errorf("%s: no stale twin in %v", name, got)
		}
	}
}
