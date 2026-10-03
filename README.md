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
modelspec lint --format json models/ | jq .
```

Each path is a file or a directory. A directory is searched recursively for
`*.modelspec.hcl` and `*.modelspec.json`; hidden directories and `node_modules` are
skipped. Files given together are checked together: a module-qualified reference such
as `entity = "core.Space"` resolves against the other files by module name, which is
the file name without `.modelspec.hcl`, or `module.name` in JSON. Linting one file that
refers to another module on its own reports that module as unknown.

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
| 1 | At least one finding at error severity; for `export`, a model that cannot be exported or a committed JSON file that has drifted |
| 2 | Usage or I/O error: unknown flag, unreadable or missing file, no model files found |
| 10 | `self-update --check` found a newer release |

`modelspec` makes no network request except `self-update`, and sends no telemetry.

### What it checks

Both forms, on the same typed model:

| Rule | Checks |
| --- | --- |
| `syntax` | HCL syntax, with the real HCL parser; JSON syntax |
| `shape` | blocks, labels and JSON groups have the structure ModelSpec defines; no unknown block types or top-level fields |
| `literal` | HCL attribute values are literals: no expressions, references, functions or map-style containers (decisions 0007, 0009) |
| `reference` | `entity`, `component`, `enum`, `use`, collection `source` and `bind` resolve, with the right kind, including module-qualified names (decisions 0013, 0014) |
| `reserved-name` | no concept is named `entities`, `components`, `enums`, `collections` or `recordsets` (decision 0015) |
| `duplicate-name` | names are unique per scope: entity, component and enum share one scope; collections and recordsets have their own (decision 0015); property and field names are unique |
| `name-form` | concept, property and field names are identifiers and contain no dot |
| `enum-values` | an enum has at least one value and no repeats; so does an inline `enum = [...]` |
| `unknown-type` | `type` is one of the ModelSpec types |
| `attribute` | only supported attributes, with values of the right type |
| `member-kind` | a property or component field has exactly one of `type`, `entity`, `component` |
| `key` | an entity has a non-empty `key` naming its properties (or fields of components it uses); a recordset key names its columns |
| `collection` | `kind` is `editable` or `computed`; a computed collection without a `query` is a warning |

Only for JSON, what the JSON readers in the ModelSpec ecosystem refuse: `modelspec-version`
(`"modelspec"` is `"1.0-draft"`), `module` (`module.id`, `module.name` an identifier,
`module.version`), and `entities` (there are entities, and each has properties).

One warning, `unsupported-by-consumers`: a property that uses a `component` is valid
ModelSpec, but a reader that accepts only scalar and entity-reference properties refuses
the model.

### What it does not check

`index` and `projection` blocks are read for syntax only (the specification does not
define their JSON form, see Export). The contents of `projections` and `migrations` in
JSON, `query` text, `pattern` regular expressions, and recordset `source`
expressions are not interpreted. A module-qualified reference is resolved only against
the files linted together, not against other repositories. `modelspec` does not check a
model against data, or against a SpecScore project's `dependsOn`.

## Export

```sh
modelspec export model/chinook.modelspec.hcl \
  --module-id github.com/acme/chinook/model/chinook --module-name chinook --module-version 0.1.0 \
  --out model/chinook.modelspec.json

modelspec export --check model/chinook.modelspec.hcl model/chinook.modelspec.json
```

The JSON follows `spec/json-format.md`: `modelspec`, `module`, then components, enums,
entities, collections, recordsets (concepts in source order, attributes in source order,
recordset columns as an ordered array with `name` first). The output of `export` for
datatug/chinookdb's `model/chinook.modelspec.hcl` is byte-identical to its committed
`model/chinook.modelspec.json`.

`--check` compares documents, not bytes: key and array order count, whitespace does not.
It takes the module identity from the committed file unless `--module-*` flags are
given.

Two things the specification leaves open are refused rather than invented:

- **Module identity.** HCL has no place for `module.id`, `module.name` and
  `module.version` (`spec/hcl-authoring.md`, "Open Questions"), so `export` needs them as
  flags.
- **`index` and `projection` blocks.** `spec/core-model.md` shows `index` blocks and
  decision 0009 lists `projection` blocks, but no document defines their HCL attributes or
  how they map to the JSON `projections` object, so a file that has one lints clean and
  cannot be exported.

`examples/todo.modelspec.hcl` in the ModelSpec repository names its recordset
`task_summary` while `examples/todo.modelspec.json` names it `taskSummary`, and nothing
defines a renaming, so `export --check` reports that difference for the pair as published.

## Parity with the other readers

`go test` compares `modelspec lint` with committed verdicts of two other readers over the
corpus in `testdata/corpus` (59 files, both forms; `testdata/corpus/manifest.json` is the
expected verdict of each):

- `testdata/golden/specscore.json`: `specscore graph lint`, through the throwaway
  project wrapper that datatug/chinookdb's `scripts/lint-modelspec.sh` uses. The file
  records the specscore version.
- `testdata/golden/directory.json`: `parseModelSpec` of
  [openvaultdb/directory](https://github.com/openvaultdb/directory). The file records its
  commit.

The test asserts that `modelspec lint` refuses everything they refuse, except where the
manifest names the difference and its reason. `modelspec lint` is deliberately stricter in
the other direction (for example it requires entity keys and checks types), and the manifest
says so item by item.

The default tests need only Go. To refresh the golden files (needs Node, `git`, and
`specscore` on `PATH` or `SPECSCORE=...`):

```sh
node scripts/regen-golden.mjs specscore
DIRECTORY_DIR=/path/to/clone-of-openvaultdb-directory node scripts/regen-golden.mjs directory
```

## Development

```sh
go test -race ./...
go test -race -covermode=atomic -coverpkg=./... -coverprofile=cover.out ./... && go run ./cmd/covergate cover.out
```

The coverage gate is exact: it fails unless every statement of every package, including
`cmd/`, is covered. It has no threshold, flag or environment variable to lower; the tests of
`internal/covergate` fail if CI runs it any other way. Everything the commands touch
(filesystem, output streams, build information, the update source) is injected, so the
tests run in memory with no subprocess and no network.

## Licence

Apache-2.0, see `LICENSE`. `NOTICE` records the code derived from the SpecScore CLI.
