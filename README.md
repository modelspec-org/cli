# modelspec

`modelspec` validates [ModelSpec](https://github.com/specscore/modelspec) models and
exports them to their JSON interchange form. It is one static binary that needs no
project, no network and no other tool.

- `modelspec lint` checks models written in HCL (`*.modelspec.hcl`) or in the JSON
  form (`*.modelspec.json`).
- `modelspec export` writes the JSON form of an HCL model, and `export --check`
  fails when a committed JSON file is not what its HCL exports to.
- `modelspec version` and `modelspec self-update`.

The checks are also an importable Go library, `github.com/modelspec-org/cli/pkg/modelspec`.

## Install

Pin a release in CI by version and SHA-256. Every release publishes its archives and
`modelspec_<version>_checksums.txt` on the
[releases page](https://github.com/modelspec-org/cli/releases); take the SHA-256 of
your platform's archive from that file and keep it in your repository next to the
version:

```sh
VERSION=X.Y.Z    # the release you pin, for example 0.1.0
SHA256=...       # that archive's line in modelspec_${VERSION}_checksums.txt
ARCHIVE="modelspec_${VERSION}_linux_amd64.tar.gz"   # or darwin_arm64, darwin_amd64, linux_arm64
curl -fsSL -o "$ARCHIVE" "https://github.com/modelspec-org/cli/releases/download/v${VERSION}/${ARCHIVE}"
echo "${SHA256}  ${ARCHIVE}" | sha256sum -c -      # on macOS: shasum -a 256 -c -
tar -xzf "$ARCHIVE" modelspec
./modelspec version
```

Archives exist for linux, darwin and windows on amd64 and arm64 (no windows/arm64);
windows archives are `.zip`.

With a Go toolchain:

```sh
go install github.com/modelspec-org/cli/cmd/modelspec@vX.Y.Z
```

A binary installed from a release archive updates itself with `modelspec self-update`
(`--check` only reports; see `modelspec self-update --help`).

## Lint

```sh
modelspec lint                                   # the current directory, recursively
modelspec lint model/                            # a directory
modelspec lint model/chinook.modelspec.hcl model/chinook.modelspec.json
modelspec lint spec/                             # a SpecScore tree
modelspec lint sales.modelspec.hcl --module core=shared/core/
modelspec lint --profile publish --format json models/ | jq .
```

Each path is a file or a directory. A directory is searched recursively for
`*.modelspec.hcl` and `*.modelspec.json` (and, inside a SpecScore `models/` directory,
for any `*.hcl`). Hidden directories and `node_modules` are skipped. **A search follows no
symbolic link**, to a directory or to a file (a repository can hold a link to `/dev/zero`, or to a
file outside it, and `lint .` on someone else's branch must not read it): a link that would have
been a model file is an **error** (rule `skipped-file`, exit 1, under both profiles) that names it,
and so is a model file that is not a regular file (a named pipe, a device). It is an error, not a
warning, because the run did not check what it was asked to: a warning would let an invalid model
reached through a link pass, and let `export` write half a module. The module the file belongs to is
not checked further, so what the missing file would have answered is not reported as an unresolved
reference; the other modules of the run are checked, and `export` and `export --check` refuse the
module (exit 1, the finding on standard error). Replace the link with the file, or name it on the
command line. A file named on the command line may be a symbolic link to a
regular file and is read through; one that is not a regular file (a device, a pipe, a directory, or
a link to one) is an error, exit 2, whose message says what it is. A file reached by two names (a
relative and an absolute path, a symbolic link named on the command line) is read once.

Text output, sorted by file, line and rule, then a summary:

```
model/shop.modelspec.hcl:12: error: entity "Order" property "customer" entity reference "Customer" does not resolve to an entity in module "shop" [reference]
failed: 1 file checked, 1 error, 0 warnings
```

`--format json` prints one object: `files`, `errors`, `warnings`, `notes` and `findings`, each
finding with `file`, `line` (omitted when unknown), `rule`, `severity` and `message`.

**A module is the unit of checking.** Given one file of a module that has more files on disk
(a SpecScore models directory, or `X.modelspec.hcl` and its `X.modelspec.json`), `lint` loads
and checks the whole module and reports the other files' findings with their paths; a `note:`
line (the `notes` array in JSON) says so once per module. The same module reached through
two files is checked once. `export` loads a module the same way. A module directory that
cannot be listed is an error (exit 2), not a quiet partial check.

### Exit codes

| Code | Meaning |
| --- | --- |
| 0 | Clean: no finding at error severity (warnings may be printed) |
| 1 | At least one finding at error severity; for `export`, a model that cannot be exported, has errors, or a committed JSON file that has drifted |
| 2 | Usage or I/O error: unknown flag or profile, unreadable or missing file, no model files found |
| 10 | `self-update --check` found a newer release |

With `--format json`, every exit-2 path also writes one JSON object to standard output,
`{"error": "...", "exit": 2}`, so a script never finds an empty or success-looking
standard output; the message is on standard error as well. `modelspec` makes no network
request except `self-update`, and sends no telemetry.

### Profiles

The **default profile implements the standard** as written in
[specscore/modelspec](https://github.com/specscore/modelspec) (`spec/core-model.md`,
`spec/hcl-authoring.md`, `spec/json-format.md` and the decisions) and nothing else. A
model the standard allows is never refused because one consumer's reader is narrower.
Narrowing for one consumer is opt-in.

`--profile publish` adds the rules a model needs to be listed in public catalogues. These
are exactly what the JSON reader of [openvaultdb/directory](https://github.com/openvaultdb/directory)
(`parseModelSpec`) requires on top of the standard; the corpus tests run that reader's
recorded verdicts against this profile.

| Rule | Requires | Because |
| --- | --- | --- |
| `publish-module-name` | JSON: `module.name` is present and an identifier (letters, digits, `_`, not starting with a digit) | the catalogue refers to a model by it |
| `publish-entities` | the module declares at least one entity | the catalogue lists a model by its entities |
| `publish-properties` | every entity has at least one property of its own | the catalogue lists an entity by its properties |
| `publish-name-form` | entity names and property names are identifiers | the catalogue turns them into record-set and column names |
| `publish-component-property` | no property has a `component` value | the catalogue lists only scalar and entity-reference properties |
| `publish-qualified-entity` | an `entity` reference names an entity of the same module, not `<module>.<Name>` | the catalogue resolves entity references inside the one model only |

The publish profile includes every rule of the default profile.

### Modules

A module is a set of files, not one file. Concept names are unique per module across all its
files, and a reference resolves against the whole module.

1. **`--module <name>=<path>`** (repeatable; a file or a directory, any `.hcl` name) assigns
   files to module `<name>` and wins over the other rules. Assigned files are linted too, so
   `modelspec lint --module core=shared/` is a complete command.
2. **SpecScore layout.** Every `*.hcl` file directly inside `…/modules/<id>/models/` belongs to
   module `<id>`, whatever the files are called. The layout is detected from the path of each
   file as given or found (symbolic links are not resolved first), with no need for the
   directory to be searched: a single file named on the command line is enough.
3. **Standalone.** `<name>.modelspec.hcl` is module `<name>` by itself. A JSON file is module
   `module.name`, or its file name without `.modelspec.json` when it has none.

`--module` wins in every case: a file assigned to a module is in that module whatever its name or
place, and a JSON file in an assigned directory joins the module as a copy of it. Assigning part of
a layout module's directory, or one file to two modules, is a usage error (exit 2), because it would
split a module.

`X.modelspec.json` beside `X.modelspec.hcl` is the interchange copy of the same module, not a
second module (this is the layout `export --out` produces); so is any JSON file in the models directory
of a layout module, or in the directory of an explicit module that has HCL. Both are linted, references
into the module resolve to the HCL, and the pair is never "ambiguous". A copy is checked against
its HCL: when it is not what the HCL exports to, `stale-twin` (a warning) says so and which
key differs. The copy's own `module.name` is accepted for references inside it. Two genuinely
different sources claiming one module name are an error where the module is referenced.

A module-qualified reference such as `entity = "core.Space"` (decision 0014) resolves against the
modules among the files linted together. When the module is not among them, the error says so
and how to supply it (`--module core=<path>`, or name its files too). A key or a `bind` sees the
fields a component contributes through `use`, including a component of another module when that
module is supplied; when it is not, the key and bind are not judged.

### What the default profile checks

Both forms, on the same typed model:

| Rule | Checks |
| --- | --- |
| `syntax` | HCL syntax, with the real HCL parser; JSON syntax |
| `encoding` | the source is UTF-8 |
| `limit` | the source is within the size, nesting and heredoc limits (below) |
| `shape` | blocks, labels and JSON groups have the structure ModelSpec defines; no unknown block types |
| `literal` | HCL attribute values are literals: no expressions, references, functions or map-style containers (decisions 0007, 0009). Syntax a literal cannot contain is refused from the tokens before the file is parsed (below) |
| `reference` | `entity`, `component`, `enum`, `use`, collection `source` and `bind` resolve, with the right kind, including module-qualified names (decisions 0013, 0014) |
| `reserved-name` | no concept is named `entities`, `components`, `enums`, `collections` or `recordsets` (decision 0015) |
| `duplicate-name` | names are unique per scope in a module: entity, component and enum share one scope; collections and recordsets have their own (decision 0015); property and field names are unique; JSON object keys are never repeated |
| `name-form` | a concept name contains no dot (decision 0014), and no concept, property or field name is empty or blank (nothing could refer to it). Nothing else is required of a name: `Order-Item` is valid |
| `name-case` | warning: two names of one scope that differ only by case (`User` and `user`; a collision on a case-insensitive store). Not an error, because the standard keeps names case-sensitive |
| `stale-twin` | warning: a JSON twin is not what its HCL exports to |
| `skipped-file` | error, both profiles: a search found a model-named file that is not a regular file (a symbolic link, a named pipe, a device) and did not read it. The finding names the path and what it is, and the way out: replace the link with the file, or name it on the command line. The module the file belongs to is not checked at all in that run (no reference, twin or other finding is reported from a partial load), and `export` and `export --check` refuse it; other modules in the run are checked as usual |
| `enum-values` | an enum has at least one value and no repeats, also for an inline `enum = [...]`. Values are strings or integers (decision 0013: an enum constrains a string or an int property) |
| `unknown-type` | `type` is one of the ModelSpec types |
| `attribute` | only supported attributes, with values of the right type |
| `member-kind` | an entity property or component field has exactly one of `type`, `entity`, `component` |
| `key` | an entity has a non-empty `key` naming its properties (or fields of components it uses); a recordset key names its columns |
| `collection` | `kind` is `editable` or `computed`; a computed collection without a `query` is a warning |
| `modelspec-version`, `module` | JSON: `"modelspec"` is `"1.0-draft"`; `module.id` and `module.version` are present (`module.name` is optional) |
| `unknown-field` | JSON: a top-level field the format does not define is a warning (`$schema` is accepted; the format is silent on other fields) |

A JSON document needs no entities, and an entity needs no properties: a module of components and
enums is valid, and so is an entity whose key comes from a component it uses. JSON that `export`
writes from HCL that lints clean lints clean (a test runs that round trip over the whole accepting
HCL corpus).

### What the default profile does not check

- **Attributes.** The accepted attributes are the lists in `spec/core-model.md` (types,
  constraints, `bind`, `source`, `query`, `kind`, `key`, `use`, `values`). The standard says
  attributes include "similar metadata", so an attribute outside those lists is refused; if you
  need one, that is a point for the standard (see "Open points").
- **Values it only reads.** `pattern` is not compiled, `format` is not checked against a list,
  `query` text and recordset `source` expressions are not interpreted, an enum's values are not
  checked against the type of the property it constrains.
- **Constructs with no defined JSON form.** The bodies of `index`, `projection` and `migration`
  blocks are not read (the blocks lint clean; `export` refuses them), and the contents of the
  JSON `projections` and `migrations` objects are carried through unchecked.
- **Other repositories.** A module-qualified reference is resolved only against the files linted
  together.
- **SpecScore's own rules**, such as a module's `dependsOn`, and any comparison with data.

### Literal values only, and limits

**What a value can be.** ModelSpec v0 HCL has literal values only: decision 0009 and
`spec/hcl-authoring.md` ("V0 Grammar Scope") allow singular named blocks, attributes, and strings,
numbers, booleans and lists (object literals only "where explicitly specified"; the standard specifies none, a search of `spec/` and
`examples/` finds only the non-canonical `properties = { … }`, so `modelspec` reads one and refuses it with
the map-style message of decision 0007),
and "do not use dynamic HCL expressions or functions". A reference to another module is a string
(`entity = "core.Space"`, decision 0014), so it is allowed. `modelspec` takes that to mean this grammar:

- block types and labels (a label is a quoted string; a bare word is read as a label too);
- attribute names, `=`, and these values: a quoted string or a heredoc, a number (`-` only as its sign),
  `true`, `false`, `null` (read, and refused as "not a ModelSpec value"), and a list of those, with
  commas, newlines and comments between items; a bare word (a reference) is read and refused with the
  message that it must be a literal.

**What is refused before parsing.** The HCL parser is recursive, and a stack overflow in Go is fatal, so
the source is lexed first (the lexer does not recurse) and refused, without being parsed, if it holds any
token a literal cannot contain. That closes the whole class instead of the recursive constructs one by one.
Each is a finding with rule `literal` that names the construct and the line (at most one for each construct
on a line, and 50 for a file), exit 1:

| Refused | Because |
| --- | --- |
| `(` `)` | grouping and function calls |
| `.` `.*` `::` `...` | traversals, attribute splats, namespaced functions, argument expansion |
| `[` after a value, `[*]` | an index or a full splat on a value |
| `+` `-` `*` `/` `%`, `-` not before a number | arithmetic (`-` is allowed only as the sign of a number, `-1`) |
| `==` `!=` `<` `<=` `>` `>=` `&&` `\|\|` `!` | comparison and logic |
| `?` | conditionals |
| `[for …` `{for …`, `=>` | `for` expressions |
| `${` and `%{` in a string or a heredoc | template interpolation and directives (`$${` and `%%{` are the escapes and are allowed) |

Brackets, quotes and operators inside strings, heredocs and comments are text, so a file whose
`pattern = <<EOT … [[[ … EOT` holds unbalanced brackets is read normally.

**What is bounded.** One table, every number in the code:

| Limit | Value | What happens past it |
| --- | --- | --- |
| size of one source file, of any file a command reads (the JSON operand of `export --check` too) | 1 MiB (`MaxInputBytes`) | refused before it is read (the size is from the file's metadata) and, whatever the metadata says, at most one byte over the limit is read, so a file that grows or never ends is bounded too; only a regular file is read (a device, a pipe or a directory is an error, exit 2, and a symbolic link is followed only when named on the command line); not valid UTF-8 is refused before it is parsed. `export` refuses to write a JSON twin over the limit (exit 1, nothing written, and the message says why), since `lint` would refuse it. Reason: the lexer holds every token at once, about 420 to 470 MiB at the peak for each MB of one-byte tokens, so 1 MiB keeps one file under 500 MiB; Chinook's model is under 8 KB and the largest corpus file is 30 KB, and a module is a set of files, each under the limit |
| nesting of braces, brackets, quoted strings and heredocs (JSON: arrays and objects) | 64 levels (`MaxDepth`) | `limit` finding; counted from tokens, so brackets in strings and comments do not count |
| lines of one heredoc | 1,000 (`MaxHeredocLines`) | `limit` finding naming the number of lines; `$` and `%` in a query do not change the count |
| one number literal (JSON the same) | 40 characters, not counting a sign (`MaxNumberLength`), and, written as digits (no trailing zeros) times a power of ten, an exponent of at most 100 either way (`MaxNumberExponent`) | `limit` finding that says how many characters or which exponent, and the limit. Stricter than the standard, which sets none: see "Numbers and names" |
| one name: a block label, an identifier, the value of `type`, `entity`, `component`, `enum` or `kind`, an item of `key` or `use` (JSON: every object key, and the same strings) | 255 bytes as written (`MaxNameLength`) | `limit` finding that says how many bytes and the limit. Stricter than the standard, which states no length |
| one finding's message | 1,024 bytes (`MaxMessageBytes`); a piece of the user's text in it, 255 (`MaxEchoBytes`) | cut, with a marker that says how long it was |
| syntax errors, non-literal tokens, or numbers and names over their limits shown for one file | 50 (`MaxSyntaxFindings`) | the rest are not listed; one finding says so |
| findings kept by one reader, one check and one run | 1,000 (`MaxFindings`) | the rest are counted, not kept: one more finding says how many were left out, with how many were errors and how many warnings. Errors are kept in preference to warnings (an error takes the place of a listed warning), so an error is dropped only when more than 1,000 errors were found, and then the finding is an error, so the exit code is what it would be with no limit. A `skipped-file` error (a module that was not checked) is kept ahead of every other finding, and if more than 1,000 were found the summary says how many of those not listed are skipped files. The line that counts what one reader or check left out stands for those findings: when a run's list drops it, or a `skipped-file` error takes its place, the summary counts all of them and not one. The output stays sorted. The summary line's counts (`N errors`) are of the findings listed, the last finding's text has the rest. The limit is in the library, so memory is bounded too |

**Output is bounded.** One run lists at most 1,001 findings (the 1,000 kept and the one that counts the
rest), each at most 1,024 bytes of message, plus its path, line number, severity and rule (at most 64 bytes
besides the path): at most 1,001 x (1,088 + the length of the path) bytes, about 1.1 MB, in the text format; in
`--format json` a byte can be written as six (`\u003c`), so at most about 6.5 MB. A test builds a model with
1,001 findings that echo the longest names and values and asserts the bound (it gives 395 KB).

Real models nest four or five levels in HCL and six to eight in JSON, and no query or pattern comes near a
thousand lines.

**Why strings and heredocs are linear.** The HCL parser joins the pieces of a string or heredoc one at a time,
copying the text and shifting the list of pieces each time, which takes time that grows with the square of
their number; and the lexer starts a new piece at each `$` and `%` (and, in a heredoc, at each line). A
`pattern` of 200,000 `$a` (400 KB) took 24 seconds, one of 1 MiB nearly three minutes, one of 4 MiB of `$${`
nearly seven. `modelspec` rewrites the `$` and `%` inside literal text before the parser sees it, so that they
stay inside their piece (in a quoted string as the escapes `\u0024` and `\u0025`, which the parser reads as the
same text; in a heredoc, which has no escapes, as two private-use characters that are turned back into `$`
and `%` in the value), and the pieces left are the lines of a heredoc, which are capped. A backslash and the
character after it in a quoted string are one unit that the rewrite does not touch, so `"\$"` reaches the
library as written and is refused (`Invalid escape sequence`) as it is without the rewrite; and in a heredoc a
carriage return right after a `$` or `%` stays with it, as the library's lexer takes it. A test compares the
CLI with the unmodified library in both directions over 800 generated strings and heredocs full of `$`, `%`,
their escapes, a backslash before every kind of character, carriage returns and the private-use characters
themselves: whatever the library refuses the CLI refuses, and whatever the library reads the CLI reads to the
same value (374 were read by both, with the same value, and 426 were refused by both); there is no
known input on which they differ. Over the corpus and 5.8 million valid files of an earlier review the values
were identical; that is a measurement, not a proof. A string of 520,000 `$a` (1 MiB) takes 0.3 seconds.

**The checker is linear too.** A reference used to be found by scanning every concept of the module, and an
entity's properties rebuilt for every `bind` that named it: valid models of 4 MB took minutes (see the table
below). Concepts are now indexed once for each module and the properties of an entity once for each entity (the
fields of a component are kept once, not copied into each entity that uses it).
Tests count the memory allocated at two sizes of each shape, which does not depend on the load of the machine,
and fail when eight times the model costs more than fourteen times the memory. A scan allocates nothing, so
other tests count steps: concepts visited to build a module's index (each exactly once, however many lookups
there are), members listed to build a set (once), and sets and names consulted to answer whether a name is a
property of an entity (bounded by twice the fields of the components it uses, however many lookups).

**Numbers and names.** The HCL library reads a number into 512 bits (about 154 digits) and takes time that
grows with the exponent: `values = [1e10000000]`, 37 bytes, took 12 seconds and lint passed it; an integer of
4.19 million digits took 12 seconds; two integers that differ after the 154th digit were read as equal; a name of
2 million characters was repeated in every finding that mentioned it. So `modelspec` bounds the token and
not the behaviour after it, in `precheck`, from the lexer's tokens, before the parser runs (and in the JSON reader
as it reads): a number of at most 40 characters and, written as digits (without trailing zeros) times a power of
ten, an exponent of at most 100 either way, so every number that is accepted is read exactly (40 digits need 133
bits); and a name of at most 255 bytes. The standard sets neither limit, and a
model that exceeds one is valid by the standard and refused here, with a `limit` finding that says what was
counted and the limit. Where databases limit an identifier it is between 63 and 128 bytes, so 255 leaves room; no number in a real model comes near
40 characters or 1e100. The JSON form has the same two limits (every object key and the same name strings are
names), so a model and its twin are refused alike. Text that is not a name (a `pattern`, a `format`, a `query`, a
`source`, an enum value) is limited only by the size of the file; whatever of it a message repeats is cut.

**One form for a number.** Writing a number with a fraction or a negative exponent used to cost 20 to 35
microseconds (the library prints a 512-bit value as the shortest decimal), so a megabyte of `1e-99,` took 5.7
seconds; and a number had no single form (`-0` was 0 in HCL and stayed `-0` in JSON, JSON kept `1e2` and `1.0` as
written, and `1e41` to `1e100` were exported as integers of 42 to 101 digits that `lint` then refused). One
function now turns the text of an accepted number into its canonical form by string arithmetic, with no floating
point, and both readers, the comparison of equal values (duplicate enum values), the comparison of a JSON copy
with its HCL and `export` all use it. A number is read as digits times a power of ten; the canonical form is
`0` for zero (no sign), the plain decimal when it has at most 40 characters, and otherwise the digits, an `e` and
the exponent: `1`, `1.0`, `1e0` and `10e-1` are `1`; `-0` is `0`; `1e+100` is `1e100`; `1e40` is `1e40` and
`1e39` is a 1 and 39 zeros; `0.5` is `0.5` and `15e-50` is `15e-50`. `export` writes that form, so a model
exported and read back is the same model and is within the limits it was read under. JSON numbers are the same:
`1e2` and `1.0` are the integers 100 and 1 there too. The one change this brings to what `export` wrote is for a
number of more than 40 characters as an integer or decimal, which it used to write in full and now writes with an
exponent; none in the corpus, the standard's examples or Chinook changes a byte.

The exponent limit is on that normal form, not on the exponent as written: `100e99` is refused (it is `1e101`, and the
message says so: "the number 100e99 is 1e101, whose exponent 101 is past the limit of 100"), `0.1e101` is accepted (it is
`1e100`), and so are `10e99` and `1e100`. Zero keeps the exponent it was written with, so `0e100` is accepted and `0e101`
and `0e2147483648` are refused alike.

**Time and memory, measured.** `modelspec lint` of the inputs below, every one within the 1 MiB limit, at the head of
this change, on a laptop shared with other work (load average 2.8 to 4.2, so the figures are upper bounds; the peak
is the process's resident memory). The first table is the shapes that were once slow, the second what the limits and
the reader refuse:

| Input (all within the limit) | Time | Peak memory |
| --- | --- | --- |
| `pattern = "` + `$a` x 200,000 + `"` (400 KB) | 0.11 s | 150 MiB |
| `$${` x 349,000 (1 MiB) | 0.17 s | 160 MiB |
| `$a` x 520,000 (1 MiB) | 0.29 s | 380 MiB |
| one `pattern` of 1,040,000 letters | 0.07 s | 23 MiB |
| one entity of 10,000 properties and a collection of 14,000 fields bound to them (1.0 MB, HCL) | 0.13 s | 168 MiB |
| the same in JSON, 15,000 properties and 20,000 binds (1.0 MB) | 0.02 s | 44 MiB |
| 15,000 components and a `use` list of 70,000 names (1.0 MB) | 0.16 s | 176 MiB |
| an enum of 200,000 repeats of one value (1.0 MB) | 0.25 s (1,001 findings, 91 KB) | 326 MiB |
| 520 heredocs of 999 lines (1.0 MB) | 0.22 s | 216 MiB |
| a label of 1 million `$` (1.0 MB), refused by the name limit | 0.10 s | 379 MiB |
| a name of 200,000 bytes and 200 properties of unknown type (208 KB), refused | 0.00 s, 134 bytes | 14 MiB |

A list of numbers, `enum "E" { values = [ ... ] }`, by size (seconds and peak memory; before is the previous head,
which printed each number as the shortest decimal of its 512 bits, after is the canonical form):

| The list holds | 250 KB | 500 KB | 1 MB |
| --- | --- | --- | --- |
| `1e-99,` before | 1.43 s, 59 MiB | 2.86 s, 102 MiB | 5.94 s, 198 MiB |
| `1e-99,` after | 0.06 s, 55 MiB | 0.13 s, 96 MiB | 0.28 s, 194 MiB |
| `0.1,` before | 1.04 s, 84 MiB | 2.06 s, 135 MiB | 4.32 s, 241 MiB |
| `0.1,` after | 0.06 s, 75 MiB | 0.12 s, 138 MiB | 0.24 s, 230 MiB |
| `9e99,` before | 0.34 s, 67 MiB | 0.68 s, 118 MiB | 1.35 s, 232 MiB |
| `9e99,` after | 0.07 s, 65 MiB | 0.14 s, 112 MiB | 0.30 s, 215 MiB |
| `1e100,` before | 0.29 s, 60 MiB | 0.55 s, 101 MiB | 1.21 s, 198 MiB |
| `1e100,` after | 0.06 s, 56 MiB | 0.13 s, 96 MiB | 0.27 s, 185 MiB |

The slowest input measured is 0.44 seconds and the largest peak 521 MiB, both for a megabyte of `1,` (an enum of
524,274 repeats of it, exactly 1 MiB; half the size peaks at 228 MiB), measured at a load average of 3.0; the review
measured 0.48 to 0.49 seconds and 564 to 577 MiB for it. Every other input in the tables is at most 0.30 seconds and
380 MiB. The lexer holds every token of a file at
once, so memory is linear in the number of tokens, about 420 to 500 MiB for each MB of one-byte tokens (the limit
of 1 MiB is what bounds that, and what bounded it at 1.7 to 1.9 GiB when it was 4 MiB). (`values` takes integers, so a list of fractions is a finding for each item, and each number is still read and
written, which is the cost measured.) These are measurements of the inputs
listed, not every input, and no time or memory is proved: the HCL library could have other paths that are slower
than linear, and a new version of it needs the fuzz run below and these inputs again. A directory of such files
takes the sum.

**What is and is not proved.** The checks are a test of the tokens, so nothing recursive in the HCL parser
is reachable except the nesting of braces and brackets, which is bounded. There is a test that runs
`ParseHCL` with the process stack limited to 4 MiB on 3,000 repeats of every construct that made the parser
recurse (it fails by overflowing the stack if the pre-parse refusal is removed), and one input for each
refused token. A new version of the `hcl` library, which could add tokens or recursion, needs
`scripts/fuzz.sh [seconds]` (fuzz targets for the HCL and the JSON readers in `scripts/fuzz/`; oracles: no
crash, publish refuses whatever the default profile refuses, a clean model exports to JSON that parses and
lints clean, and one input costs a bounded amount of work: at most 4 MiB plus 1,000 bytes allocated for each
byte of input, counted by the allocator and not by time, and a watchdog ends the process, which the fuzzer reports
as a failing input, if one input is still running after ten seconds: while it runs, not after it returns). `scripts/fuzz.sh`
runs the fuzzer with `-fuzzminimizetime 5s`: by default it minimises a new input for up to a minute, with no execution
reported (30 to 42 seconds of nothing, with two workers), which no judge can tell from a stall. A
whole run that stalls is judged by `scripts/fuzz.sh` through `cmd/fuzzjudge` (`internal/fuzzjudge`, with table tests
over synthetic logs): it fails when nothing ran for 30 seconds anywhere in the run, including its end, or when the
rate of the second half fell below 30% of the first. The fuzzer's own pauses (12 to 18 seconds with no execution)
and a rate that halves from one half to the other (seen in real runs) pass. A stall of one input in one worker of
several leaves most of the rate, so it is the watchdog that catches it, not the judge. The fuzz targets are skipped in `go test` and are not in the coverage
gate.

## Export

```sh
modelspec export model/chinook.modelspec.hcl \
  --module-id github.com/acme/chinook/model/chinook --module-name chinook --module-version 0.1.0 \
  --out model/chinook.modelspec.json

modelspec export --check model/chinook.modelspec.hcl model/chinook.modelspec.json
```

`export` lints the file first, as `lint` does under the default profile (so its module is checked
whole), and refuses a file with errors (exit 1, the findings for that file on standard error);
`--check` refuses an invalid model too, so a check cannot pass on one. It exports any `.hcl` file of a
layout module, whatever it is called, and refuses a JSON file given as the source. A file that refers to other modules needs them supplied with
`--module <name>=<path>` (they are used to resolve references and are not exported).

The JSON follows `spec/json-format.md`: `modelspec`, `module`, then components, enums,
entities, collections, recordsets (concepts in source order, attributes in source order,
recordset columns as an ordered array with `name` first). The output of `export` for
datatug/chinookdb's `model/chinook.modelspec.hcl` is byte-identical to its committed
`model/chinook.modelspec.json`.

Numbers are written in their canonical form (see "One form for a number"): `1.0` and `1e0` are `1`, `-0` is `0`, and
a number that would need more than 40 characters as a plain decimal is written as digits and an exponent (`1e41`),
which `lint` reads back; so are the numbers of a JSON file when it is read, and `--check` compares them in that form.

`--check` compares documents, not bytes: whitespace does not count, but **the order of keys in
objects and of items in arrays does** (the Directory compares a model with its registered copy
the same way). The module identity is read from the committed file unless `--module-*` flags are
given. `--out` cannot be combined with `--check`.

`--out` writes a regular file. The path is looked at without following a link, and one that exists and is not a
regular file is refused (exit 2, nothing written, and the message says what it is): a symbolic link (even to a
regular file, which would send the write elsewhere), a named pipe (which would wait for a reader), a directory, a
device. `/dev/null` is a device, so `--out /dev/null` is refused; write to standard output and redirect it
(`export ... > /dev/null`). A path that does not exist, and a regular file, are written as before.

Every file a command reads has the 1 MiB limit, the committed JSON that `--check` reads too (one over it is
refused without being read, exit 1), and every one is read through one reader that refuses what is not a regular
file (exit 2) and reads at most one byte over the limit. `export` refuses to write a JSON twin over the limit, since `lint` would refuse
it: valid HCL of 0.7 to 1.0 MB can export to 1.2 to 1.7 MB (1.3 to 1.7 times the size). It exits 1, writes nothing,
and says why.

### Open points

The standard does not settle these, so `modelspec` does not invent an answer (each is listed, with what
`modelspec` does today, in [specscore/modelspec#8](https://github.com/specscore/modelspec/issues/8)):

- **Module identity.** HCL has no place for `module.id`, `module.name` and `module.version`
  (`spec/hcl-authoring.md`, "Open Questions"), so `export` takes `--module-id` and
  `--module-version` (the format requires those two) and `--module-name` if you want one (it is written without being given when the model refers to its own
  module by name, so the JSON lints clean whatever file it is saved as).
- **The module short name outside a SpecScore layout.** `lint` takes it from the file name
  (`<name>.modelspec.hcl`), from `module.name` in JSON, or from `--module`.
- **`index`, `projection` and `migration` blocks.** `spec/core-model.md` shows `index`,
  decision 0009 lists `projection`, and `spec/migration-metadata.md` shows `migration`, but no
  document defines their HCL attributes or how they map to the JSON `projections` and
  `migrations` objects. A file with one lints clean and cannot be exported.
- **Several files in one module.** The JSON form is one document per module and nothing says
  how the files of a module merge, so a file that is one of several files of a module is not
  exported (the other files are named in the refusal). The module is linted whole whichever file is given.
- **Names.** Empty and blank names are errors (nothing could refer to them), names that differ only by
  case are warnings, and a property name with a dot is accepted although `bind` and migration renames
  use dots as separators.
- **Integer enum values** are accepted (decision 0013 mentions int properties); the format does
  not say what an enum's values may be.
- **Unknown top-level JSON fields** are accepted with a warning.
- **The published todo example**: `examples/todo.modelspec.hcl` names its recordset `task_summary`
  while `examples/todo.modelspec.json` names it `taskSummary`, so `export --check` reports that
  difference for the pair as published.

## Parity with the other readers

`go test` compares `modelspec lint` with committed verdicts of two other readers over the corpus
in `testdata/corpus` (**121 manifest items**; each is a file, a SpecScore-layout tree or a set of standalone files, or an
entry under `parts/` that is one file of another item given alone and must give the verdict of its whole module;
`testdata/corpus/manifest.json` is the expected verdict of each, under both profiles; a test fails when this
number is not the manifest's):

- `testdata/golden/specscore.json`: `specscore graph lint`, compared with the **default**
  profile, through the throwaway-project wrapper that datatug/chinookdb's `scripts/lint-modelspec.sh`
  uses for single files and on whole SpecScore projects for the layout trees. The file records the
  specscore version (0.54.2).
- `testdata/golden/directory.json`: `parseModelSpec` of the Directory's reader, compared with the
  **publish** profile. The file records the commit on the reader's `main` branch.

The test asserts that `modelspec lint` refuses everything they refuse, except where the manifest
names the difference and its reason, and that every recorded difference is real. In the other
direction `modelspec lint` is deliberately stricter in places (for example it requires entity keys
and checks types); the manifest says so item by item. To refresh the golden files (needs Node,
`git`, and `specscore` on `PATH` or `SPECSCORE=...`; the Directory clone must be at a commit on its
`main`):

```sh
node scripts/regen-golden.mjs specscore
DIRECTORY_DIR=/path/to/clone-of-openvaultdb-directory node scripts/regen-golden.mjs directory
```

## Releases

Every release is made by a push to `main`, and a release runs only after the coverage gate has passed
for that commit, in the same workflow run. `release.yml` (the only workflow that runs on a push) has two
jobs: `gate` calls `ci.yml` (formatting, vet, module tidiness, the tests with the race detector, the exact
coverage gate, and the GoReleaser configuration and snapshot build), and `release`, which has
`needs: gate` and `if: github.ref == 'refs/heads/main'`, calls the shared `strongo/cicd` release
workflow. That workflow tags the commit from its conventional commits (a push of only `docs:`, `chore:`
or `ci:` commits cuts nothing) and publishes the archives and the checksums file. `ci.yml` has no push
trigger (it runs on pull requests and when called), so a push to `main` runs the gate once, and there is
nothing for the shared workflow to wait for: its `require_workflow_success` option, which waits for a
workflow's run and continues without it after 180 seconds, is not used. There is no tag trigger and no
manual dispatch, so a hand-pushed tag releases nothing.

The shared workflow and every action named in `ci.yml` and `release.yml` are pinned to a full commit SHA with the
version in a comment (`strongo/cicd` v1.21.0 is `5d96b1f3fbb3`; `git ls-remote https://github.com/strongo/cicd
refs/tags/v1.21.0` shows it). That does not make every action that runs in a release a pinned one: the shared workflow
at that commit itself runs `actions/checkout@v7`, `actions/setup-go@v7`, `orhun/git-cliff-action@v4.9.1` and
`goreleaser/goreleaser-action@v7` by tag, in the job that holds `contents: write` (and `actions/setup-node@v7` only when
its `node_version` input is set, which it is not here). The pin fixes the shared workflow's own steps, not the code those
four tags point to at the time of a release; the fix is upstream, in `strongo/cicd`.

Recovery, **as read from the shared workflow's source at that commit and not exercised here**:

- *The gate is red* (`gate` failed in the push's run): `release` does not run, so there is no tag and
  nothing is published. Fix on `main`; the next push runs the gate and, if green, releases. Re-running
  the failed jobs of the same run is also possible when the cause was transient.
- *The release failed after the tag was pushed* (the tag is pushed before the archives are built): a
  re-run of the run finds the tag already on the commit, computes no bump and ends green with a
  notice, "No release published this run", without publishing. To retry that release, delete the tag
  on the remote by hand and re-run the run; or leave it and let the next `feat:` or `fix:` commit cut the
  next version.
- A push with only `docs:`, `chore:` or `ci:` commits since the last tag cuts no release, by design.

## Development

```sh
go test -race ./...
go test -race -covermode=atomic -coverpkg=./... -coverprofile=cover.out ./... && go run ./cmd/covergate cover.out
scripts/fuzz.sh 120    # HCL and JSON reader fuzzing, outside the default run and the gate
```

The coverage gate is exact: it fails unless every statement of every package, including `cmd/`,
is covered. It has no threshold, flag or environment variable to lower it. It also lists the module's
packages as `go list ./...` does (from the files, with no subprocess) and fails if a package that has
statements is absent from the profile or contributes none to it, so a package whose tests never ran (a
`TestMain` that exits 0) cannot vanish from the count. Everything the commands touch (filesystem, output
streams, build information, the update source) is injected, so the tests run in memory with no subprocess
and no network.

### What stops a release without the gate

The tests in `internal/covergate` parse `.github/workflows` and fail if:

- the gate job or its two steps become conditional or able to fail silently, stop running exactly the
  test and gate commands one after the other, run in a `container` or with `services`, or if anything
  touches the cover profile between them or sets `GOFLAGS`, `GITHUB_ENV` or `GITHUB_PATH`;
- `release.yml` stops being exactly two jobs, `gate` (calling `./.github/workflows/ci.yml`, with
  `contents: read`) and `release` (`needs: gate`, `if: github.ref == 'refs/heads/main'`, calling the shared
  workflow, with `contents: write`, the three inputs and the five optional secrets it has now, no
  `require_workflow_success`), or its concurrency stops being `cancel-in-progress: false`;
- the triggers are not exactly a push to `main` for `release.yml` and `pull_request` plus a bare
  `workflow_call` for `ci.yml` (no tag, dispatch, schedule or `paths` filter anywhere);
- either workflow gets a workflow-level `env` or `defaults`, or other `permissions` than `release.yml: {}` and
  `ci.yml: contents: read`;
- any job of `ci.yml` other than the two it has gets `permissions`, `uses`, `needs`, a `container` or
  `services`, runs anything that tags, pushes, calls `gh`, runs `goreleaser release` or mentions a
  secret, or the packaging job's steps differ in any way from the four it has (checkout, setup-go,
  `goreleaser check`, `goreleaser build --snapshot --clean`);
- any action, or the shared workflow, is not a full commit SHA from the table in the test, with its version in
  the comment after it;
- a second workflow is named `CI`, or any file other than `ci.yml` and `release.yml` is under
  `.github/workflows` (a new workflow has to be added to an explicit allow-list with its reason);
- any Go file has a build constraint, so nothing can hide from the gate (a test; the gate refuses one too, below).

The gate itself also reads every `.go` file of every package directory of the module, whatever its name or build
constraint (it does not rely on the file list of the Go tool, which has only the files this platform and these tags
build), and refuses a `TestMain` in any of them, including one behind `//go:build race` or in `x_linux_test.go`,
and any build constraint (a `//go:build` or `// +build` line, or a GOOS or GOARCH file-name suffix): a `TestMain`
can run the tests and then exit 0, which hides a failing test while every statement still counts as
covered, and a file with a constraint can be left out of a test run and so of the profile. If a package needs set-up or tear-down, do it in the tests that need it with `t.Cleanup`, or give the code a
seam (a parameter or a variable) that a test sets.

**These tests are a tripwire, not the control.** They run in the pull request that changes the workflows, so
a change that edits them, or a workflow they do not parse, passes its own tests. The real control is a branch
rule (a repository ruleset) on `main` that requires a pull request and the two checks of `ci.yml`,
`Test, vet, race, exact coverage` and `GoReleaser check and snapshot build`, up to date with `main`, and allows
no direct push, force push or deletion, with no bypass list. It is a repository setting, not a file, and nothing
in this repository can set it (nor was it set by the change that wrote this text). Until it is set, `main` is only
as safe as the people who can push to it. (On a push to `main` the same checks appear as `gate / …` inside the
release run; the rule needs the plain names, which pull requests report.)

## Licence

Apache-2.0, see `LICENSE`. `NOTICE` records the code derived from the SpecScore CLI.
