#!/usr/bin/env node

import { spawn } from 'node:child_process';
import { CONFIG_PATH, getDevConfig, loadConfigSync } from './config.mjs';

function parseArgs(argv) {
  const opts = getDevConfig(loadConfigSync());

  for (let i = 0; i < argv.length; i += 1) {
    const arg = argv[i];
    if (arg === '--') continue;
    if (arg === '--db') opts.db = argv[++i];
    else if (arg === '--root') opts.roots = [argv[++i]];
    else if (arg === '--roots') opts.roots = argv[++i].split(',').map((v) => v.trim()).filter(Boolean);
    else if (arg === '--exclude-regex') opts.excludeRegex = argv[++i];
    else if (arg === '--scanner') opts.scanner = argv[++i];
    else if (arg === '--host') opts.webHost = argv[++i];
    else if (arg === '--port') opts.webPort = Number(argv[++i]);
    else if (arg === '--api-host') opts.apiHost = argv[++i];
    else if (arg === '--api-port') opts.apiPort = Number(argv[++i]);
    else if (arg === '--help' || arg === '-h') {
      printHelp();
      process.exit(0);
    } else {
      throw new Error(`Unknown argument: ${arg}`);
    }
  }

  return opts;
}

function printHelp() {
  process.stdout.write('Usage: pnpm run dev -- [options]\n\n');
  process.stdout.write(`Defaults are loaded from ${CONFIG_PATH}\n\n`);
  process.stdout.write('Options:\n');
  process.stdout.write('  --db <path>             SQLite database path (default: config paths.database)\n');
  process.stdout.write('  --root <path>           Single scan root for sync\n');
  process.stdout.write('  --roots <a,b,c>         Comma-separated scan roots\n');
  process.stdout.write('  --exclude-regex <expr>  Path exclusion regex\n');
  process.stdout.write('  --scanner <mode>        auto|fdfind|fd|node\n');
  process.stdout.write('  --host <host>           Vite bind host (default: config web.host)\n');
  process.stdout.write('  --port <port>           Vite bind port (default: config web.port)\n');
  process.stdout.write('  --api-host <host>       API bind host (default: config api.host)\n');
  process.stdout.write('  --api-port <port>       API bind port (default: config api.port)\n');
}

function startProc(cmd, args, extraEnv = {}) {
  return spawn(cmd, args, {
    stdio: 'inherit',
    env: { ...process.env, ...extraEnv }
  });
}

function main() {
  const opts = parseArgs(process.argv.slice(2));

  const apiArgs = [
    'scripts/inspect-table.mjs',
    '--db',
    opts.db,
    '--host',
    opts.apiHost,
    '--port',
    String(opts.apiPort),
    '--exclude-regex',
    opts.excludeRegex,
    '--scanner',
    opts.scanner
  ];

  for (const root of opts.roots) {
    apiArgs.push('--root', root);
  }

  const api = startProc(process.execPath, apiArgs);

  const apiOrigin = `http://${opts.apiHost}:${opts.apiPort}`;
  const web = startProc(
    'pnpm',
    [
      'exec',
      'vite',
      '--config',
      'vite.config.mjs',
      '--host',
      opts.webHost,
      '--port',
      String(opts.webPort),
      '--strictPort'
    ],
    { REPOSVIEW_API_ORIGIN: apiOrigin }
  );

  process.stdout.write(`\nreposview web: http://${opts.webHost}:${opts.webPort}\n`);
  process.stdout.write(`reposview api: ${apiOrigin}\n\n`);

  let shuttingDown = false;
  const shutdown = (code = 0) => {
    if (shuttingDown) return;
    shuttingDown = true;

    if (!api.killed) api.kill('SIGTERM');
    if (!web.killed) web.kill('SIGTERM');

    setTimeout(() => process.exit(code), 200);
  };

  api.on('exit', (code) => shutdown(code || 0));
  web.on('exit', (code) => shutdown(code || 0));
  process.on('SIGINT', () => shutdown(0));
  process.on('SIGTERM', () => shutdown(0));
}

try {
  main();
} catch (error) {
  process.stderr.write(`${error instanceof Error ? error.message : String(error)}\n`);
  process.exit(1);
}
