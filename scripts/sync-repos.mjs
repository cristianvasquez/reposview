#!/usr/bin/env node

import { parseSyncArgs, printSyncHelp, runSync } from './sync-core.mjs';

async function main() {
  const opts = parseSyncArgs(process.argv.slice(2));

  if (opts.help) {
    printSyncHelp();
    return;
  }

  const result = await runSync(opts);

  if (opts.dryRun) {
    process.stdout.write(`${JSON.stringify(result, null, 2)}\n`);
    return;
  }

  process.stdout.write(`Indexed ${result.indexedRepos} repositories into ${result.dbPath}\n`);
}

main().catch((error) => {
  process.stderr.write(`${error instanceof Error ? error.message : String(error)}\n`);
  process.exit(1);
});
