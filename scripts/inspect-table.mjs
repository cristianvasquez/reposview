#!/usr/bin/env node

import { createServer } from 'node:http';
import fs from 'node:fs';
import path from 'node:path';
import { spawn } from 'node:child_process';
import { DatabaseSync } from 'node:sqlite';
import { DEFAULT_SYNC_OPTIONS, commandExists, runSync, reindexRepositoryByPath, deleteRepositoryByPath } from './sync-core.mjs';
import { identifierDisplayFromRow, identifierKeyFromRow } from './resolve-identifier.mjs';

function parseArgs(argv) {
  const opts = {
    db: DEFAULT_SYNC_OPTIONS.db,
    roots: DEFAULT_SYNC_OPTIONS.roots,
    excludeRegex: DEFAULT_SYNC_OPTIONS.excludeRegex,
    scanner: DEFAULT_SYNC_OPTIONS.scanner,
    host: '127.0.0.1',
    port: 8787
  };

  for (let i = 0; i < argv.length; i += 1) {
    const arg = argv[i];
    if (arg === '--') continue;
    if (arg === '--db') {
      opts.db = argv[++i];
      continue;
    }
    if (arg === '--root') {
      opts.roots = [argv[++i]];
      continue;
    }
    if (arg === '--roots') {
      opts.roots = argv[++i].split(',').map((v) => v.trim()).filter(Boolean);
      continue;
    }
    if (arg === '--exclude-regex') {
      opts.excludeRegex = argv[++i];
      continue;
    }
    if (arg === '--scanner') {
      opts.scanner = argv[++i];
      continue;
    }
    if (arg === '--host') {
      opts.host = argv[++i];
      continue;
    }
    if (arg === '--port') {
      opts.port = Number(argv[++i]);
      continue;
    }
    if (arg === '--help' || arg === '-h') {
      printHelp();
      process.exit(0);
    }
    throw new Error(`Unknown argument: ${arg}`);
  }

  return opts;
}

function printHelp() {
  process.stdout.write('Usage: pnpm run inspect -- [options]\n\n');
  process.stdout.write('Options:\n');
  process.stdout.write('  --db <path>             SQLite database path (default: ./data/reposview.sqlite)\n');
  process.stdout.write('  --root <path>           Single scan root for UI-triggered sync (default: /)\n');
  process.stdout.write('  --roots <a,b,c>         Comma-separated scan roots for UI-triggered sync\n');
  process.stdout.write('  --exclude-regex <expr>  Path exclusion regex for UI-triggered sync\n');
  process.stdout.write('  --scanner <mode>        auto|fdfind|fd|node (default: auto)\n');
  process.stdout.write('  --host <host>           Bind host (default: 127.0.0.1)\n');
  process.stdout.write('  --port <port>           Bind port (default: 8787)\n');
}

function qParam(url, key, fallback = '') {
  const value = url.searchParams.get(key);
  return value === null ? fallback : value;
}

function launchTerminalAtDir(dirPath) {
  if (!commandExists('yazi')) {
    return { ok: false, error: 'yazi command not found' };
  }

  const candidates = [
    ['ghostty', [`--working-directory=${dirPath}`, '--gtk-single-instance=false', '-e', 'yazi', dirPath]],
    ['gnome-terminal', [`--working-directory=${dirPath}`, '--', 'yazi', dirPath]]
  ];

  for (const [cmd, args] of candidates) {
    if (!commandExists(cmd)) continue;
    const child = spawn(cmd, args, { detached: true, stdio: 'ignore', cwd: dirPath });
    child.unref();
    return { ok: true, command: cmd };
  }

  return { ok: false, error: 'no supported terminal command found' };
}

async function readJsonBody(req, maxBytes = 64 * 1024) {
  const chunks = [];
  let total = 0;
  for await (const chunk of req) {
    const buf = Buffer.isBuffer(chunk) ? chunk : Buffer.from(chunk);
    total += buf.length;
    if (total > maxBytes) {
      throw new Error('request body too large');
    }
    chunks.push(buf);
  }
  if (chunks.length === 0) return {};
  return JSON.parse(Buffer.concat(chunks).toString('utf8'));
}

function walkMarkdownFiles(repoPath) {
  const ignoredDirs = new Set(['.git', 'node_modules', '.next', '.cache', 'dist', 'build', 'target', '.venv', 'venv']);
  const stack = [repoPath];
  const found = [];
  let scanned = 0;
  const maxEntries = 12000;

  while (stack.length > 0 && scanned < maxEntries) {
    const dir = stack.pop();
    let entries = [];
    try {
      entries = fs.readdirSync(dir, { withFileTypes: true });
    } catch {
      continue;
    }

    for (const entry of entries) {
      scanned += 1;
      if (scanned >= maxEntries) break;
      const full = path.join(dir, entry.name);
      if (entry.isDirectory()) {
        if (!ignoredDirs.has(entry.name)) stack.push(full);
        continue;
      }
      if (!entry.isFile()) continue;
      if (/\.(md|markdown)$/i.test(entry.name)) found.push(full);
    }
  }

  found.sort((a, b) => a.localeCompare(b));
  return found;
}

function findReadmeFile(repoPath) {
  const preferred = ['README.md', 'readme.md', 'CLAUDE.md', 'claude.md'].map((name) => path.join(repoPath, name));
  for (const candidate of preferred) {
    try {
      if (fs.existsSync(candidate) && fs.statSync(candidate).isFile()) return candidate;
    } catch {
      // ignore stat/access errors and continue
    }
  }

  const markdownFiles = walkMarkdownFiles(repoPath);
  if (markdownFiles.length === 0) return null;
  return markdownFiles[0];
}

function readRepoDetails(repoPath) {
  const resolved = path.resolve(repoPath);
  if (!path.isAbsolute(resolved)) {
    return { ok: false, error: 'path must be absolute' };
  }

  if (!fs.existsSync(resolved) || !fs.statSync(resolved).isDirectory()) {
    return { ok: false, error: 'repository path not found' };
  }

  const readmePath = findReadmeFile(resolved);
  if (!readmePath) {
    return {
      ok: true,
      path: resolved,
      readme: { exists: false, path: null, content: '', truncated: false }
    };
  }

  const maxBytes = 250 * 1024;
  const stat = fs.statSync(readmePath);
  const truncated = stat.size > maxBytes;
  const content = fs.readFileSync(readmePath, 'utf8').slice(0, maxBytes);

  return {
    ok: true,
    path: resolved,
    readme: {
      exists: true,
      path: readmePath,
      content,
      truncated
    }
  };
}

function bareFromQuery(url) {
  const value = qParam(url, 'bare', '').toLowerCase();
  if (value === 'bare' || value === 'nonbare') return value;
  return 'all';
}

function filtersFromUrl(url) {
  return {
    q: qParam(url, 'q', ''),
    sort: qParam(url, 'sort', 'path'),
    dir: qParam(url, 'dir', 'asc').toLowerCase() === 'desc' ? 'desc' : 'asc',
    bare: bareFromQuery(url),
    identifier: qParam(url, 'identifier', qParam(url, 'origin', '')),
    branch: qParam(url, 'branch', ''),
    author: qParam(url, 'author', ''),
    pathPrefix: qParam(url, 'path_prefix', ''),
    identifierPrefix: qParam(url, 'identifier_prefix', qParam(url, 'origin_prefix', ''))
  };
}

function normalizeLocalPath(raw) {
  if (!raw) return '';
  const value = String(raw).replaceAll('\\', '/').trim();
  if (!value) return '';
  if (value === '/') return '/';
  return value.replace(/\/+$/, '');
}

function pathHasPrefix(repoPath, prefix) {
  const pathValue = normalizeLocalPath(repoPath);
  const prefixValue = normalizeLocalPath(prefix);
  if (!prefixValue) return true;
  if (prefixValue === '/') return pathValue.startsWith('/');
  return pathValue === prefixValue || pathValue.startsWith(`${prefixValue}/`);
}

function identifierHasPrefix(row, prefix) {
  const key = identifierKeyFromRow(row);
  const filter = String(prefix || '').trim();
  if (!filter) return true;
  return key === filter || key.startsWith(`${filter}/`);
}

function nullableTextFilter(column, rawValue, whereParts, params) {
  if (!rawValue) return;
  if (rawValue === '__none__') {
    whereParts.push(`(${column} IS NULL OR ${column} = '')`);
    return;
  }
  whereParts.push(`${column} = ?`);
  params.push(rawValue);
}

function buildWhereClause(filters, options = {}) {
  const whereParts = [];
  const params = [];

  if (!options.ignoreBare) {
    if (filters.bare === 'bare') whereParts.push('is_bare = 1');
    else if (filters.bare === 'nonbare') whereParts.push('is_bare = 0');
  }

  if (filters.q.trim().length > 0) {
    whereParts.push('(path LIKE ? OR identifier LIKE ? OR branch LIKE ? OR lineage LIKE ? OR last_commit_author LIKE ? OR last_commit_at LIKE ?)');
    const like = `%${filters.q.trim()}%`;
    params.push(like, like, like, like, like, like);
  }

  if (!options.ignorePathPrefix && filters.pathPrefix) {
    const pathPrefix = normalizeLocalPath(filters.pathPrefix);
    if (pathPrefix === '/') {
      whereParts.push("path LIKE '/%'");
    } else if (pathPrefix) {
      whereParts.push('(path = ? OR path LIKE ?)');
      params.push(pathPrefix, `${pathPrefix}/%`);
    }
  }

  if (!options.ignoreIdentifier) nullableTextFilter('identifier', filters.identifier, whereParts, params);
  if (!options.ignoreBranch) nullableTextFilter('branch', filters.branch, whereParts, params);
  if (!options.ignoreAuthor) nullableTextFilter('last_commit_author', filters.author, whereParts, params);

  return {
    whereSql: whereParts.length > 0 ? `WHERE ${whereParts.join(' AND ')}` : '',
    params
  };
}

function aggregateFacetCounts(values) {
  const counts = new Map();
  for (const value of values) {
    const key = value === null || value === undefined || value === '' ? '__none__' : String(value);
    counts.set(key, (counts.get(key) || 0) + 1);
  }
  return [...counts.entries()]
    .map(([value, count]) => ({ value, label: value === '__none__' ? '(none)' : value, count }))
    .sort((a, b) => b.count - a.count || a.label.localeCompare(b.label));
}

function aggregateBinaryFacet(rows, extractor, labels) {
  const counts = new Map();
  for (const row of rows) {
    const key = extractor(row);
    counts.set(key, (counts.get(key) || 0) + 1);
  }
  return [...counts.entries()]
    .map(([value, count]) => ({ value, label: labels[value] || value, count }))
    .sort((a, b) => b.count - a.count || a.value.localeCompare(b.value));
}

function buildTreeFacet(rows, keySelector, options = {}) {
  const maxNodes = options.maxNodes || 500;
  const counts = new Map();
  const labels = new Map();
  const parents = new Map();
  const depths = new Map();

  for (const row of rows) {
    const key = keySelector(row);
    if (!key) continue;
    const segments = String(key).split('/').filter(Boolean);
    if (segments.length === 0) continue;

    let prefix = '';
    for (let i = 0; i < segments.length; i += 1) {
      const segment = segments[i];
      prefix = prefix ? `${prefix}/${segment}` : segment;
      counts.set(prefix, (counts.get(prefix) || 0) + 1);
      labels.set(prefix, segment);
      depths.set(prefix, i + 1);
      if (i > 0) {
        const parent = segments.slice(0, i).join('/');
        parents.set(prefix, parent);
      }
    }
  }

  return [...counts.entries()]
    .map(([prefix, count]) => ({
      prefix,
      label: labels.get(prefix) || prefix,
      parentPrefix: parents.get(prefix) || null,
      depth: depths.get(prefix) || 1,
      count
    }))
    .sort((a, b) => a.depth - b.depth || b.count - a.count || a.prefix.localeCompare(b.prefix))
    .slice(0, maxNodes);
}

function buildLocalPathTreeFacet(rows) {
  return buildTreeFacet(
    rows,
    (row) => {
      const normalized = normalizeLocalPath(row.path);
      if (!normalized || !normalized.startsWith('/')) return '';
      return normalized.slice(1);
    },
    { maxNodes: 650 }
  ).map((node) => ({
    ...node,
    prefix: `/${node.prefix}`,
    parentPrefix: node.parentPrefix ? `/${node.parentPrefix}` : null
  }));
}

function buildIdentifierTreeFacet(rows) {
  return buildTreeFacet(rows, (row) => identifierKeyFromRow(row), { maxNodes: 650 });
}

function fetchRowsForFilterSet(db, filters, options = {}) {
  const { whereSql, params } = buildWhereClause(filters, options);
  const sql = `
SELECT path, identifier, branch, lineage, last_commit_author, last_commit_at, is_bare, first_seen_at, last_seen_at
FROM repositories
${whereSql};
`;
  let rows = db.prepare(sql).all(...params);
  if (!options.ignoreIdentifierPrefix && filters.identifierPrefix) {
    rows = rows.filter((row) => identifierHasPrefix(row, filters.identifierPrefix));
  }
  return rows;
}

function compareNullable(a, b, dir) {
  const left = a === null || a === undefined ? '' : String(a);
  const right = b === null || b === undefined ? '' : String(b);
  const result = left.localeCompare(right);
  return dir === 'DESC' ? -result : result;
}

function sortRows(rows, safeSort, safeDir) {
  rows.sort((a, b) => compareNullable(a[safeSort], b[safeSort], safeDir) || compareNullable(a.path, b.path, safeDir));
}

function readRows(dbPath, filters) {
  const db = new DatabaseSync(dbPath, { readOnly: true });
  try {
    const dbTotalStmt = db.prepare('SELECT count(*) as c FROM repositories;');
    const legacySort = filters.sort === 'origin' ? 'identifier' : filters.sort;
    const validSort = new Set(['path', 'identifier', 'branch', 'last_commit_author', 'last_commit_at', 'last_seen_at']);
    const safeSort = validSort.has(legacySort) ? legacySort : 'path';
    const safeDir = filters.dir === 'desc' ? 'DESC' : 'ASC';
    const matchedRows = fetchRowsForFilterSet(db, filters);
    sortRows(matchedRows, safeSort, safeDir);

    const rows = matchedRows.slice(0, 1000).map((row) => ({ ...row, identifier: identifierDisplayFromRow(row) }));
    const totalCount = matchedRows.length;
    const databaseTotal = dbTotalStmt.get().c;

    const bareFacetRows = fetchRowsForFilterSet(db, filters, { ignoreBare: true });
    const branchFacetRows = fetchRowsForFilterSet(db, filters, { ignoreBranch: true });
    const localPathTreeRows = fetchRowsForFilterSet(db, filters, { ignorePathPrefix: true });
    const identifierTreeRows = fetchRowsForFilterSet(db, filters, { ignoreIdentifierPrefix: true });

    const facets = {
      bare: aggregateBinaryFacet(
        bareFacetRows,
        (row) => (row.is_bare ? 'bare' : 'nonbare'),
        { bare: 'bare', nonbare: 'nonbare' }
      ),
      branch: aggregateFacetCounts(branchFacetRows.map((row) => row.branch)).slice(0, 30),
      localPathTree: buildLocalPathTreeFacet(localPathTreeRows),
      identifierTree: buildIdentifierTreeFacet(identifierTreeRows)
    };

    return { rows, totalCount, databaseTotal, facets };
  } finally {
    db.close();
  }
}

async function main() {
  const opts = parseArgs(process.argv.slice(2));
  const dbPath = path.resolve(opts.db);
  const listeners = new Set();

  let syncState = {
    running: false,
    phase: null,
    message: null,
    scanner: null,
    discoveredGitDirs: 0,
    processedGitDirs: 0,
    persistedRepos: 0,
    lastRunAt: null,
    lastIndexed: 0,
    durationMs: null,
    error: null
  };

  function broadcast() {
    const payload = `data: ${JSON.stringify(syncState)}\n\n`;
    for (const res of listeners) {
      res.write(payload);
    }
  }

  function updateSync(patch) {
    syncState = { ...syncState, ...patch };
    broadcast();
  }

  async function startSync() {
    if (syncState.running) return false;

    updateSync({
      running: true,
      phase: 'queued',
      message: 'sync queued',
      error: null,
      processedGitDirs: 0,
      discoveredGitDirs: 0,
      persistedRepos: 0
    });

    runSync({
      db: dbPath,
      roots: opts.roots,
      excludeRegex: opts.excludeRegex,
      scanner: opts.scanner,
      onProgress: (event) => {
        updateSync({
          phase: event.phase || syncState.phase,
          message: event.message || syncState.message,
          scanner: event.scanner || syncState.scanner,
          discoveredGitDirs: event.discoveredGitDirs ?? syncState.discoveredGitDirs,
          processedGitDirs: event.processedGitDirs ?? syncState.processedGitDirs,
          persistedRepos: event.persistedRepos ?? syncState.persistedRepos
        });
      }
    })
      .then((result) => {
        updateSync({
          running: false,
          phase: 'done',
          message: 'sync completed',
          scanner: result.scanner,
          discoveredGitDirs: result.scannedGitDirs,
          processedGitDirs: result.scannedGitDirs,
          persistedRepos: result.indexedRepos,
          lastRunAt: result.at,
          lastIndexed: result.indexedRepos,
          durationMs: result.durationMs,
          error: null
        });
      })
      .catch((error) => {
        updateSync({
          running: false,
          phase: 'error',
          message: 'sync failed',
          error: error instanceof Error ? error.message : String(error)
        });
      });

    return true;
  }

  const server = createServer((req, res) => {
    handleRequest(req, res).catch((err) => {
      res.statusCode = 500;
      res.setHeader('Content-Type', 'text/plain; charset=utf-8');
      res.end(`internal error: ${err.message}`);
    });
  });

  async function handleRequest(req, res) {
    if (!req.url) {
      res.statusCode = 400;
      res.end('bad request');
      return;
    }

    const url = new URL(req.url, `http://${opts.host}:${opts.port}`);

    if (url.pathname === '/healthz') {
      res.statusCode = 200;
      res.setHeader('Content-Type', 'application/json; charset=utf-8');
      res.end('{"ok":true}');
      return;
    }

    if (url.pathname === '/events') {
      res.writeHead(200, {
        'Content-Type': 'text/event-stream; charset=utf-8',
        'Cache-Control': 'no-cache, no-transform',
        Connection: 'keep-alive'
      });
      res.flushHeaders?.();
      listeners.add(res);
      res.write(`data: ${JSON.stringify(syncState)}\n\n`);
      const heartbeat = setInterval(() => {
        res.write(': keepalive\n\n');
      }, 15000);
      req.on('close', () => listeners.delete(res));
      req.on('close', () => clearInterval(heartbeat));
      return;
    }

    if (url.pathname === '/sync-status') {
      res.statusCode = 200;
      res.setHeader('Content-Type', 'application/json; charset=utf-8');
      res.end(JSON.stringify(syncState));
      return;
    }

    if (url.pathname === '/rows') {
      const filters = filtersFromUrl(url);
      const data = readRows(dbPath, filters);
      res.statusCode = 200;
      res.setHeader('Content-Type', 'application/json; charset=utf-8');
      res.end(JSON.stringify({ rows: data.rows, totalCount: data.totalCount, databaseTotal: data.databaseTotal, facets: data.facets }));
      return;
    }

    if (url.pathname === '/repo-details') {
      const repoPath = qParam(url, 'path', '').trim();
      if (!repoPath) {
        res.statusCode = 400;
        res.setHeader('Content-Type', 'application/json; charset=utf-8');
        res.end(JSON.stringify({ ok: false, error: 'path query parameter is required' }));
        return;
      }

      const payload = readRepoDetails(repoPath);
      res.statusCode = payload.ok ? 200 : 404;
      res.setHeader('Content-Type', 'application/json; charset=utf-8');
      res.end(JSON.stringify(payload));
      return;
    }

    if (url.pathname === '/actions/open-terminal') {
      if (req.method !== 'POST') {
        res.statusCode = 405;
        res.end('method not allowed');
        return;
      }

      const body = await readJsonBody(req);
      const rawPath = String(body?.path || '').trim();
      const resolved = path.resolve(rawPath);

      if (!rawPath || !path.isAbsolute(resolved) || !fs.existsSync(resolved) || !fs.statSync(resolved).isDirectory()) {
        res.statusCode = 400;
        res.setHeader('Content-Type', 'application/json; charset=utf-8');
        res.end(JSON.stringify({ opened: false, error: 'invalid path' }));
        return;
      }

      const launched = launchTerminalAtDir(resolved);
      res.statusCode = launched.ok ? 200 : 500;
      res.setHeader('Content-Type', 'application/json; charset=utf-8');
      res.end(JSON.stringify({ opened: launched.ok, path: resolved, ...launched }));
      return;
    }

    if (url.pathname === '/actions/reindex-repo') {
      if (req.method !== 'POST') {
        res.statusCode = 405;
        res.end('method not allowed');
        return;
      }

      const body = await readJsonBody(req);
      const rawPath = String(body?.path || '').trim();
      const resolved = path.resolve(rawPath);

      if (!rawPath || !path.isAbsolute(resolved)) {
        res.statusCode = 400;
        res.setHeader('Content-Type', 'application/json; charset=utf-8');
        res.end(JSON.stringify({ reindexed: false, error: 'invalid path' }));
        return;
      }

      if (!fs.existsSync(resolved)) {
        const deleted = await deleteRepositoryByPath(dbPath, resolved);
        res.statusCode = deleted.deleted ? 200 : 404;
        res.setHeader('Content-Type', 'application/json; charset=utf-8');
        res.end(JSON.stringify({ reindexed: false, deleted: deleted.deleted, ...deleted }));
        return;
      }

      if (!fs.statSync(resolved).isDirectory()) {
        res.statusCode = 400;
        res.setHeader('Content-Type', 'application/json; charset=utf-8');
        res.end(JSON.stringify({ reindexed: false, error: 'path is not a directory' }));
        return;
      }

      try {
        const row = await reindexRepositoryByPath(dbPath, resolved);
        res.statusCode = 200;
        res.setHeader('Content-Type', 'application/json; charset=utf-8');
        res.end(JSON.stringify({ reindexed: true, row }));
      } catch (error) {
        res.statusCode = 500;
        res.setHeader('Content-Type', 'application/json; charset=utf-8');
        res.end(JSON.stringify({ reindexed: false, error: error instanceof Error ? error.message : String(error) }));
      }
      return;
    }

    if (url.pathname === '/sync') {
      if (req.method === 'POST' || req.method === 'GET') {
        const started = await startSync();
        res.statusCode = started ? 202 : 200;
        res.setHeader('Content-Type', 'application/json; charset=utf-8');
        res.end(JSON.stringify({ started, running: syncState.running }));
        return;
      }

      res.statusCode = 405;
      res.end('method not allowed');
      return;
    }

    res.statusCode = 404;
    res.setHeader('Content-Type', 'text/plain; charset=utf-8');
    res.end('not found');
  }

  server.listen(opts.port, opts.host, () => {
    process.stdout.write(`reposview dev server: http://${opts.host}:${opts.port} (db: ${dbPath})\n`);
  });

  server.on('error', (err) => {
    process.stderr.write(`server error: ${err.message}\n`);
    process.exit(1);
  });

  const shutdown = () => {
    for (const res of listeners) {
      res.end();
    }
    listeners.clear();

    server.close(() => {
      process.exit(0);
    });
  };

  process.on('SIGINT', shutdown);
  process.on('SIGTERM', shutdown);
}

main().catch((error) => {
  process.stderr.write(`${error instanceof Error ? error.message : String(error)}\n`);
  process.exit(1);
});
