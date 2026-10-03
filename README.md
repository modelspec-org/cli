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

`--format json` prints one object: `files`, `errors`, `warnings`, and `findings`, each
with `file`, `line` (omitted when unknown), `rule`, `severity` and `message`.

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
   file given or found.
3. **Standalone.** `<name>.modelspec.hcl` is module `<name>` by itself. A JSON file is module
   `module.name`, or its file name without `.modelspec.json` when it has none.

`X.modelspec.json` beside `X.modelspec.hcl` is the interchange copy of the same module, not a
second module (this is the layout `export --out` produces). Both are linted, references into
the module resolve to the HCL, and the pair is never "ambiguous". Two genuinely different sources
claiming one module name are an error where the module is referenced.

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
| `limit` | the source is within the size and nesting limits (below) |
| `shape` | blocks, labels and JSON groups have the structure ModelSpec defines; no unknown block types |
| `literal` | HCL attribute values are literals: no expressions, references, functions or map-style containers (decisions 0007, 0009) |
| `reference` | `entity`, `component`, `enum`, `use`, collection `source` and `bind` resolve, with the right kind, including module-qualified names (decisions 0013, 0014) |
| `reserved-name` | no concept is named `entities`, `components`, `enums`, `collections` or `recordsets` (decision 0015) |
| `duplicate-name` | names are unique per scope in a module: entity, component and enum share one scope; collections and recordsets have their own (decision 0015); property and field names are unique; JSON object keys are never repeated |
| `name-form` | a concept name contains no dot (decision 0014). Nothing else is required of a name: `Order-Item` is valid |
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

### Limits

A source larger than 16 MiB (`MaxInputBytes`), not valid UTF-8, or nesting brackets more than 64 levels
deep (`MaxDepth`; strings and, in HCL, comments are skipped) is a finding before it is parsed,
so a hostile file cannot crash the process (a stack overflow in Go is fatal). Real models nest
four or five levels.

## Export

```sh
modelspec export model/chinook.modelspec.hcl \
  --module-id github.com/acme/chinook/model/chinook --module-name chinook --module-version 0.1.0 \
  --out model/chinook.modelspec.json

modelspec export --check model/chinook.modelspec.hcl model/chinook.modelspec.json
```

`export` lints the file first, as `lint` does under the default profile, and refuses a file
with errors (exit 1, the findings on standard error); `--check` refuses an invalid model too, so a
check cannot pass on one. A file that refers to other modules needs them supplied with
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
  `--module-version` (the format requires those two) and `--module-name` if you want one.
- **The module short name outside a SpecScore layout.** `lint` takes it from the file name
  (`<name>.modelspec.hcl`), from `module.name` in JSON, or from `--module`.
- **`index`, `projection` and `migration` blocks.** `spec/core-model.md` shows `index`,
  decision 0009 lists `projection`, and `spec/migration-metadata.md` shows `migration`, but no
  document defines their HCL attributes or how they map to the JSON `projections` and
  `migrations` objects. A file with one lints clean and cannot be exported.
- **Several files in one module.** The JSON form is one document per module and nothing says
  how the files of a module merge, so a file that is one of several files of a SpecScore layout
  module is not exported.
- **Integer enum values** are accepted (decision 0013 mentions int properties); the format does
  not say what an enum's values may be.
- **Unknown top-level JSON fields** are accepted with a warning.
- **The published todo example**: `examples/todo.modelspec.hcl` names its recordset `task_summary`
  while `examples/todo.modelspec.json` names it `taskSummary`, so `export --check` reports that
  difference for the pair as published.

## Parity with the other readers

`go test` compares `modelspec lint` with committed verdicts of two other readers over the corpus
in `testdata/corpus` (92 items: 34 HCL files, 43 JSON files, 8 SpecScore-layout trees, 7
standalone module sets; `testdata/corpus/manifest.json` is the expected verdict of each, under
both profiles):

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

Every release is made by a push to `main`, and a release waits for the coverage gate. The release
workflow (`release.yml`) calls the shared `strongo/cicd` release workflow, which tags the commit and
publishes the archives and the checksums file. It has no other trigger: no tag trigger and no manual
dispatch, so a hand-pushed tag releases nothing. The shared workflow's guard waits for the `CI`
workflow's run for the commit being released and refuses to tag or publish unless it succeeded; it
continues without waiting only when it finds no run for the commit after 180 seconds, and `ci.yml`
runs on every push to `main` with no path filter, so a push to `main` always has a run to wait for.
`internal/covergate`'s tests pin the triggers of both workflows, as well as the gate itself.

## Development

```sh
go test -race ./...
go test -race -covermode=atomic -coverpkg=./... -coverprofile=cover.out ./... && go run ./cmd/covergate cover.out
```

The coverage gate is exact: it fails unless every statement of every package, including `cmd/`,
is covered. It has no threshold, flag or environment variable to lower it. The tests in
`internal/covergate` parse `ci.yml` and `release.yml` and fail if the gate job or its two steps
become conditional or able to fail silently, stop running exactly the test and gate commands one
after the other, if anything touches the cover profile between them, or if the release stops
requiring the `CI` workflow; and they refuse any Go file with a build constraint, so nothing can
hide from the gate. Everything the commands touch (filesystem, output streams, build information,
the update source) is injected, so the tests run in memory with no subprocess and no network.

## Licence

Apache-2.0, see `LICENSE`. `NOTICE` records the code derived from the SpecScore CLI.
