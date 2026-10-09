// Package modelspec reads, checks and writes ModelSpec models
// (https://github.com/specscore/modelspec).
//
// A model is read from its HCL source (ParseHCL, files named
// *.modelspec.hcl) or from its JSON interchange form (ParseJSON, files named
// *.modelspec.json). Both produce the same typed Model plus a list of Findings.
// Check applies the semantic rules to a set of models, resolving
// module-qualified references among them, and Model.JSON writes the JSON
// interchange form.
//
// Check applies one of two profiles. The default implements the standard and
// nothing else; ProfilePublish adds what public catalogues require. A module is a
// set of files: Load assigns files to modules (SpecScore layout, file name, or an
// explicit Assignment), and Check resolves names across a module's files.
//
// The old spelling of the vocabulary (entity, property) is read, and is an error
// in a model that is being checked and a warning in one that is only referred to
// (Model.ReferenceOnly, OldSpellingSeverity); Model.JSON writes the vocabulary of
// the model's source.
//
// The package never exits the process, never touches the network and reads
// files only through the FS interface it is given.
//
// The HCL reader and the rules for name scopes, reserved names, duplicate
// concepts, enum values and reference resolution are derived from the
// ModelSpec support in the SpecScore CLI (https://github.com/specscore/specscore-cli,
// Apache-2.0); see the NOTICE file at the repository root.
package modelspec
