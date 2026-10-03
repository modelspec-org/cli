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
| `limit` | the source is within the size and nesting limits (below) |
| `shape` | blocks, labels and JSON groups have the structure ModelSpec defines; no unknown block types |
| `literal` | HCL attribute values are literals: no expressions, references, functions or map-style containers (decisions 0007, 0009) |
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

### Limits

A source larger than 4 MiB (`MaxInputBytes`) is refused before it is read (the size comes from the
file's metadata), and one that is not valid UTF-8 is refused before it is parsed. Past those, the
limits are findings (rule `limit`, exit 1), so that a hostile file cannot crash the process: a stack
overflow in Go is fatal and cannot be recovered, and the HCL parser is recursive. They bound what
the parser recurses on, counted from the lexer's tokens (which does not recurse), not from the bytes,
so a bracket in a string, a comment or a heredoc counts for nothing and a valid file with a
`pattern = <<EOT … [[[ … EOT` is read normally:

| Limit | Applies to | Value |
| --- | --- | --- |
| nesting | brackets, braces, parentheses, quoted strings and heredocs, `${ }` and `%{ }`, and the `%{if}` and `%{for}` directives, in HCL; arrays and objects in JSON | 64 levels (`MaxDepth`) |
| unary operators | a run of `-` or `!` | 64 (`MaxOperatorRun`) |
| conditionals | `? :` operators in one file | 64 (`MaxConditionals`) |
| syntax findings | errors reported for one HCL file | 50, then one line saying that more follow |

The HCL parser recurses once per level of nesting, per unary operator and per conditional (a
conditional's branches are expressions of their own); binary chains, traversals, indexes and splats are
loops. That is what reading `hclsyntax` (`parser.go`, `parser_template.go`, hcl v2.24.0) found; there is a
test for each at ten times its limit. ModelSpec allows no expressions (decision 0009), so a valid file
needs none of the last two. The JSON reader reads token by token with its own depth count, and refuses the document at the limit. Real
models nest four or five levels in HCL and six to eight in JSON.

These limits are the ones found by reading, and not a proof: a parser that is recursive can be
overflowed by a construct nobody listed. `scripts/fuzz.sh [seconds]` runs Go fuzz targets for the HCL
and the JSON readers (`scripts/fuzz/`) with three oracles (it must not crash, publish must refuse
whatever the default profile refuses, and a model that lints clean must export to JSON that parses
and lints clean). They are skipped in
`go test` and are not in the coverage gate, so run them when `precheck.go` or the dependency
on `hcl` changes. A crash would be a bug to report.

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
  `--module-version` (the format requires those two) and `--module-name` if you want one.
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
in `testdata/corpus` (112 manifest items: 38 HCL files, 45 JSON files, 11 SpecScore-layout trees, 10
standalone module sets, and 8 `parts/` entries, which are one file of a tree given alone and so must
give the verdict of the whole module; `testdata/corpus/manifest.json` is the expected verdict of
each, under both profiles):

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
workflow (`release.yml`) calls the shared `strongo/cicd` release workflow (pinned at `v1.21.0`), which
tags the commit and publishes the archives and the checksums file. It has no other trigger: no tag
trigger and no manual dispatch, and `ci.yml` has none of those either, so a hand-pushed tag releases
nothing. The shared workflow's guard waits for the `CI` workflow's run for the commit being released and
refuses to tag or publish unless it succeeded; it continues without waiting only when it finds no run
for the commit after 180 seconds, and `ci.yml` runs on every push to `main` with no path filter, so a
push to `main` always has a run to wait for. Pushes to `main` are never cancelled by a newer push
(`ci.yml`'s concurrency group is per commit for them and per branch for pull requests), so every
released commit has a finished run.

Recovery, **as read from the shared workflow's source at `v1.21.0` and not exercised here**:

- *The gate is red* (`CI` failed for the commit): the release job fails at "Refusing to release",
  before any tag. Re-running the failed `CI` run clears it (the newest run for the commit decides);
  otherwise fix on `main` and the next push is released.
- *The release failed after the tag was pushed* (the tag is pushed before the archives are built): a
  re-run of the release run finds the tag already on the commit, computes no bump and ends green with a
  notice, "No release published this run", without publishing. To retry that release, delete the tag
  on the remote by hand and re-run the release run; or leave it and let the next `feat:` or `fix:`
  commit cut the next version.
- A push that has only `docs:`, `chore:` or `ci:` commits since the last tag cuts no release, by design.

`internal/covergate`'s tests pin the triggers of both workflows, as well as the gate itself.

## Development

```sh
go test -race ./...
go test -race -covermode=atomic -coverpkg=./... -coverprofile=cover.out ./... && go run ./cmd/covergate cover.out
scripts/fuzz.sh 120    # HCL and JSON reader fuzzing, outside the default run and the gate
```

The coverage gate is exact: it fails unless every statement of every package, including `cmd/`,
is covered. It has no threshold, flag or environment variable to lower it. Everything the commands
touch (filesystem, output streams, build information, the update source) is injected, so the tests run
in memory with no subprocess and no network.

### What stops a release without the gate

The tests in `internal/covergate` parse `.github/workflows` and fail if:

- the gate job or its two steps become conditional or able to fail silently, stop running exactly the
  test and gate commands one after the other, run in a `container` or with `services`, or if anything
  touches the cover profile between them or sets `GOFLAGS`, `GITHUB_ENV` or `GITHUB_PATH`;
- either workflow gets a workflow-level `env` or `defaults` (a default shell can turn every `run` step
  into a no-op), a trigger on a tag or a manual dispatch, or a `paths` filter on a push (`release.yml`
  has no trigger but a push to `main`);
- `release.yml` stops requiring the `CI` workflow, has a second job, or calls anything but the shared
  release workflow pinned to an exact version tag (not a branch; it is not pinned to a commit);
- a second workflow is named `CI`, or any file other than `ci.yml` and `release.yml` is under
  `.github/workflows` (a new workflow has to be added to an explicit allow-list with its reason);
- any Go file has a build constraint, so nothing can hide from the gate.

**These tests are a tripwire, not the control.** They run in the same pull request as the change they
guard, so a change that edits them, or a workflow they do not parse, passes its own tests. The real control
is a branch rule (a repository ruleset) on `main` that requires a pull request and the two checks of `ci.yml`,
`Test, vet, race, exact coverage` and `GoReleaser check and snapshot build`, up to date with `main`, and allows
no direct push, force push or deletion, with no bypass list. It is a repository setting, not a file, and nothing
in this repository can set it (nor was it set by the change that wrote this text). Until it is set, `main` is only
as safe as the people who can push to it.

## Licence

Apache-2.0, see `LICENSE`. `NOTICE` records the code derived from the SpecScore CLI.
