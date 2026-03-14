#!/usr/bin/env node

import { parseSyncArgs, printSyncHelp, runSync } from './sync-core.mjs';

function emit(event) {
  process.stdout.write(`${JSON.stringify(event)}\n`);
}

async function main() {
  const opts = parseSyncArgs(process.argv.slice(2));

  if (opts.help) {
    printSyncHelp();
    return;
  }

  const result = await runSync({
    ...opts,
    onProgress: (event) => emit({ type: 'progress', ...event })
  });

  emit({ type: 'result', ...result });
}

main().catch((error) => {
  emit({ type: 'error', error: error instanceof Error ? error.message : String(error) });
  process.exit(1);
});
