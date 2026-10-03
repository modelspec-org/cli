#!/usr/bin/env node
// Regenerates the golden verdict files under testdata/golden/ by running the
// two other ModelSpec readers over the committed corpus (testdata/corpus/).
// This is evidence for parity tests, not part of the default test run: `go test`
// only compares `modelspec lint` with the files this script wrote, and needs
// neither Node, `specscore`, `git` nor the network.
//
//   node scripts/regen-golden.mjs specscore
//       Runs `specscore graph lint` on every corpus/hcl/*.modelspec.hcl, one
//       throwaway SpecScore project per file, built the way
//       datatug/chinookdb's scripts/lint-modelspec.sh builds it (module id =
//       the file name without .modelspec.hcl). Needs `specscore` and `git` on
//       PATH, or SPECSCORE=/path/to/specscore. Writes testdata/golden/specscore.json
//       with the specscore version in it.
//
//   DIRECTORY_DIR=/path/to/clone node scripts/regen-golden.mjs directory
//       Runs parseModelSpec from scripts/lib/modelspec.mjs of a clone of
//       https://github.com/openvaultdb/directory (use refs/pull/8/head) over
//       every corpus/json/*.modelspec.json. Writes testdata/golden/directory.json
//       with the clone's commit in it.
//
// Both files are deterministic for a given tool version and corpus.
import { execFileSync, spawnSync } from 'node:child_process';
import { cpSync, mkdirSync, mkdtempSync, readFileSync, readdirSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { dirname, join } from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';

const root = dirname(dirname(fileURLToPath(import.meta.url)));
const corpus = join(root, 'testdata', 'corpus');
const golden = join(root, 'testdata', 'golden');
const files = (dir, suffix) => readdirSync(join(corpus, dir)).filter((f) => f.endsWith(suffix)).sort();
const write = (name, doc) => writeFileSync(join(golden, name), `${JSON.stringify(doc, null, 2)}\n`);

function specscore() {
  const bin = process.env.SPECSCORE || 'specscore';
  const run = (cwd, ...args) => spawnSync(bin, [...args, '--no-telemetry'], { cwd, encoding: 'utf8' });
  const version = execFileSync(bin, ['--version'], { encoding: 'utf8' }).trim();
  const verdicts = {};
  for (const file of files('hcl', '.modelspec.hcl')) {
    const id = file.replace(/\.modelspec\.hcl$/, '');
    const work = mkdtempSync(join(tmpdir(), 'modelspec-golden-'));
    try {
      execFileSync('git', ['init', '-q'], { cwd: work });
      run(work, 'init', '--host', 'github.com', '--org', 'modelspec-org', '--repo', 'corpus', '--title', 'corpus');
      run(work, 'graph', 'new', 'module', '--id', id, '--name', id, '--summary', 'corpus item', '--bare');
      const models = join(work, 'spec', 'graph', 'modules', id, 'models');
      mkdirSync(models, { recursive: true });
      cpSync(join(corpus, 'hcl', file), join(models, file));
      const lint = run(work, 'graph', 'lint', '--severity', 'info');
      // Lines naming a rule, with the throwaway directory taken out.
      const findings = (lint.stdout || '').split('\n')
        .filter((line) => /\[(error|warning|info)\] /.test(line))
        .map((line) => line.replace(`spec/graph/modules/${id}/models/`, ''));
      verdicts[`hcl/${file}`] = { verdict: lint.status === 0 ? 'accept' : 'refuse', exit: lint.status, findings };
    } finally {
      rmSync(work, { recursive: true, force: true });
    }
  }
  write('specscore.json', {
    tool: 'specscore graph lint --severity info',
    specscore_version: version,
    wrapper: 'one throwaway project per file: specscore init, specscore graph new module --id <file stem> --bare, file copied to models/ (as datatug/chinookdb scripts/lint-modelspec.sh does)',
    verdicts,
  });
}

async function directory() {
  const dir = process.env.DIRECTORY_DIR;
  if (!dir) throw new Error('set DIRECTORY_DIR to a clone of openvaultdb/directory at refs/pull/8/head');
  const commit = execFileSync('git', ['-C', dir, 'rev-parse', 'HEAD'], { encoding: 'utf8' }).trim();
  const { parseModelSpec } = await import(pathToFileURL(join(dir, 'scripts', 'lib', 'modelspec.mjs')).href);
  const verdicts = {};
  for (const file of files('json', '.modelspec.json')) {
    const { problems } = parseModelSpec(readFileSync(join(corpus, 'json', file), 'utf8'));
    verdicts[`json/${file}`] = { verdict: problems.length === 0 ? 'accept' : 'refuse', problems };
  }
  write('directory.json', {
    tool: 'parseModelSpec in scripts/lib/modelspec.mjs of openvaultdb/directory',
    directory_commit: commit,
    node_version: process.version,
    verdicts,
  });
}

const which = process.argv[2];
if (which === 'specscore') specscore();
else if (which === 'directory') await directory();
else {
  console.error('usage: node scripts/regen-golden.mjs specscore|directory');
  process.exit(2);
}
