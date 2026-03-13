#!/usr/bin/env node

import path from 'node:path';
import { resolveRepoIdentifier } from './resolve-identifier.mjs';

function printHelp() {
  process.stdout.write('Usage: pnpm run resolve-identifier -- [repo-path] [--json]\n\n');
  process.stdout.write('Returns the effective repository identifier for a path.\n');
  process.stdout.write('If no path is provided, the current working directory is used.\n');
}

function parseArgs(argv) {
  const opts = {
    repoPath: process.cwd(),
    json: false
  };

  for (let i = 0; i < argv.length; i += 1) {
    const arg = argv[i];
    if (arg === '--') continue;
    if (arg === '--json') {
      opts.json = true;
      continue;
    }
    if (arg === '--help' || arg === '-h') {
      opts.help = true;
      continue;
    }
    if (arg.startsWith('-')) {
      throw new Error(`Unknown argument: ${arg}`);
    }
    opts.repoPath = arg;
  }

  return opts;
}

async function main() {
  const opts = parseArgs(process.argv.slice(2));

  if (opts.help) {
    printHelp();
    return;
  }

  const result = await resolveRepoIdentifier(path.resolve(opts.repoPath));

  if (opts.json) {
    process.stdout.write(`${JSON.stringify(result, null, 2)}\n`);
    return;
  }

  process.stdout.write(`${result.resolvedIdentifier}\n`);
}

main().catch((error) => {
  process.stderr.write(`${error instanceof Error ? error.message : String(error)}\n`);
  process.exit(1);
});
