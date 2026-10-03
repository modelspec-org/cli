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
for any `*.hcl`). Hidden directories and `node_modules` are skipped. Symbolic links to
files are followed; symbolic links to directories are not. A file reached by two names
(a relative and an absolute path, a symbolic link) is read once. A dangling symbolic link
found in a search is a warning and the run goes on; named on the command line it is an
error.

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
| size of one source file | 4 MiB (`MaxInputBytes`) | refused before it is read (the size is from the file's metadata); not valid UTF-8 is refused before it is parsed |
| nesting of braces, brackets, quoted strings and heredocs (JSON: arrays and objects) | 64 levels (`MaxDepth`) | `limit` finding; counted from tokens, so brackets in strings and comments do not count |
| lines of one heredoc | 1,000 (`MaxHeredocLines`) | `limit` finding naming the number of lines; `$` and `%` in a query do not change the count |
| syntax errors or non-literal tokens shown for one file | 50 (`MaxSyntaxFindings`) | the rest are not listed; one finding says so |
| findings kept by one reader, one check and one run | 1,000 (`MaxFindings`) | the rest are counted, not kept: one more finding says how many were left out, with how many were errors and how many warnings. It is an error if any dropped one was, so the exit code is what it would be with no limit. The summary line's counts (`N errors`) are of the findings listed, the last finding's text has the rest. The limit is in the library, so memory is bounded too |

Real models nest four or five levels in HCL and six to eight in JSON, and no query or pattern comes near a
thousand lines.

**Why strings and heredocs are linear.** The HCL parser joins the pieces of a string or heredoc one at a time,
copying the text and shifting the list of pieces each time, which takes time that grows with the square of
their number; and the lexer starts a new piece at each `$` and `%` (and, in a heredoc, at each line). A
`pattern` of 200,000 `$a` (400 KB) took 24 seconds, one of 1 MiB nearly three minutes, one of 4 MiB of `$${`
nearly seven. `modelspec` rewrites the `$` and `%` inside literal text before the parser sees it, so that they
stay inside their piece (in a quoted string as the escapes `\u0024` and `\u0025`, which the parser reads as the
same text; in a heredoc, which has no escapes, as two private-use characters that are turned back into `$`
and `%` in the value), and the pieces left are the lines of a heredoc, which are capped. The values read are
the ones the HCL library reads from the original text (a test compares them over random strings and heredocs
full of `$`, `%`, their escapes and the private-use characters themselves). A string of 2 million `$a` (4 MiB)
now takes about one second.

**The checker is linear too.** A reference used to be found by scanning every concept of the module, and an
entity's properties rebuilt for every `bind` that named it: valid models of 4 MB took minutes (see the table
below). Concepts are now indexed once for each module and the properties of an entity once for each entity (the
fields of a component are kept once, not copied into each entity that uses it).
Tests count the memory allocated at two sizes of each shape, which does not depend on the load of the machine,
and fail when eight times the model costs more than fourteen times the memory.

**Time, measured.** `modelspec lint` of one file within the 4 MiB limit, valid or not, in HCL or JSON: every input of
the table below, and about 80 others of the shapes I could think of (long lists, many blocks, labels, strings,
heredocs, references, duplicate names), took 1.6 seconds or less, on a laptop that was shared with other work (load
average about 3.5), so the figures are upper bounds. Before and after, in seconds:

| Input (all within the limit) | Before | After |
| --- | --- | --- |
| `pattern = "` + `$a` x 200,000 + `"` (400 KB) | 25 | 0.5 |
| the same with `$${` x 200,000 (600 KB) | 7.9 | 0.1 |
| `$a` x 524,000 (1 MiB) | 162 | 0.3 |
| `$${` x 1,390,000 (4 MiB) | 412 | 0.7 |
| `$a` x 2,090,000 (4 MiB) | not finished in 450 | 1.2 |
| one entity of 40,000 properties and a collection of 55,000 fields bound to them (4.1 MB, HCL) | 83 | 0.5 |
| the same in JSON, 60,000 properties and 80,000 binds (4.1 MB) | 206 | 0.1 |
| 60,000 components and a `use` list of 280,000 names (4.1 MB) | 32 | 0.7 |
| an enum of 900,000 repeats of one value (3.6 MB) | 1.8, and 76 MB of findings | 1.1, and 1,001 findings |

The slowest input I could construct for the code as it is now is a block label of 4 million `$` (1.5 seconds); next,
4 MiB made of heredocs of the most lines allowed, empty or of one letter (1.3), and `$a` repeated to 4 MiB (1.2).
A directory of such files takes the sum.
No time is proved: the HCL library could have other paths that are slower than linear, and a new version of it needs
the fuzz run below and these inputs again.

**What is and is not proved.** The checks are a test of the tokens, so nothing recursive in the HCL parser
is reachable except the nesting of braces and brackets, which is bounded. There is a test that runs
`ParseHCL` with the process stack limited to 4 MiB on 5,000 repeats of every construct that made the parser
recurse (it fails by overflowing the stack if the pre-parse refusal is removed), and one input for each
refused token. A new version of the `hcl` library, which could add tokens or recursion, needs
`scripts/fuzz.sh [seconds]` (fuzz targets for the HCL and the JSON readers in `scripts/fuzz/`; oracles: no
crash, publish refuses whatever the default profile refuses, a clean model exports to JSON that parses and
lints clean). The fuzz targets are skipped in `go test` and are not in the coverage gate.

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

`--check` compares documents, not bytes: whitespace does not count, but **the order of keys in
objects and of items in arrays does** (the Directory compares a model with its registered copy
the same way). The module identity is read from the committed file unless `--module-*` flags are
given. `--out` cannot be combined with `--check`.

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
in `testdata/corpus` (**114 manifest items**; each is a file, a SpecScore-layout tree or a set of standalone files, or an
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
- any Go file has a build constraint, so nothing can hide from the gate.

The gate itself also refuses a `TestMain` anywhere in the module (it reads the test files of every package): a
`TestMain` can run the tests and then exit 0, which hides a failing test while every statement still counts as
covered. If a package needs set-up or tear-down, do it in the tests that need it with `t.Cleanup`, or give the code a
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
