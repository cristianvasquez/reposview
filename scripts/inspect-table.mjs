#!/usr/bin/env node

import { createServer } from 'node:http';
import fs from 'node:fs';
import path from 'node:path';
import { spawn, spawnSync } from 'node:child_process';
import { DatabaseSync } from 'node:sqlite';
import { DEFAULT_SYNC_OPTIONS, runSync, reindexRepositoryByPath, deleteRepositoryByPath } from './sync-core.mjs';

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

function esc(value) {
  return String(value)
    .replaceAll('&', '&amp;')
    .replaceAll('<', '&lt;')
    .replaceAll('>', '&gt;')
    .replaceAll('"', '&quot;');
}

function qParam(url, key, fallback = '') {
  const value = url.searchParams.get(key);
  return value === null ? fallback : value;
}

function commandExists(cmd) {
  const out = spawnSync('bash', ['-lc', `command -v ${cmd}`], { encoding: 'utf8' });
  return out.status === 0;
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
    origin: qParam(url, 'origin', ''),
    branch: qParam(url, 'branch', ''),
    author: qParam(url, 'author', ''),
    pathPrefix: qParam(url, 'path_prefix', ''),
    originPrefix: qParam(url, 'origin_prefix', '')
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

function parseOriginToCanonicalKey(rawOrigin) {
  const raw = String(rawOrigin || '').trim();
  if (!raw) return '(unknown)';

  const scpLike = raw.match(/^[^@]+@([^:\/\s]+):(.+)$/);
  if (scpLike) {
    const host = scpLike[1].toLowerCase();
    const pathPart = scpLike[2].replace(/\.git$/i, '').replace(/^\/+/, '');
    const parts = pathPart.split('/').filter(Boolean);
    return [host, ...parts].join('/');
  }

  try {
    const url = new URL(raw);
    const host = (url.host || '').toLowerCase();
    const parts = url.pathname.replace(/\.git$/i, '').replace(/^\/+/, '').split('/').filter(Boolean);
    if (!host) return '(unknown)';
    return [host, ...parts].join('/');
  } catch {
    return '(unknown)';
  }
}

function originKeyFromRow(row) {
  const upstream = String(row.origin || '').trim();
  if (!upstream) {
    const lineage = String(row.lineage || '').trim();
    return lineage ? `local/${lineage}` : 'local/empty';
  }
  return parseOriginToCanonicalKey(upstream);
}

function originHasPrefix(row, prefix) {
  const key = originKeyFromRow(row);
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
    whereParts.push('(path LIKE ? OR origin LIKE ? OR branch LIKE ? OR lineage LIKE ? OR last_commit_author LIKE ?)');
    const like = `%${filters.q.trim()}%`;
    params.push(like, like, like, like, like);
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

  if (!options.ignoreOrigin) nullableTextFilter('origin', filters.origin, whereParts, params);
  if (!options.ignoreBranch) nullableTextFilter('branch', filters.branch, whereParts, params);
  if (!options.ignoreAuthor) nullableTextFilter('last_commit_author', filters.author, whereParts, params);

  return {
    whereSql: whereParts.length > 0 ? `WHERE ${whereParts.join(' AND ')}` : '',
    params
  };
}

function buildQuery(filters) {
  const { sort, dir } = filters;
  const validSort = new Set(['path', 'origin', 'branch', 'last_commit_author', 'last_seen_at']);
  const safeSort = validSort.has(sort) ? sort : 'path';
  const safeDir = dir === 'desc' ? 'DESC' : 'ASC';
  const { whereSql, params } = buildWhereClause(filters);
  const sql = `
SELECT path, origin, branch, lineage, last_commit_author, is_bare, first_seen_at, last_seen_at
FROM repositories
${whereSql}
ORDER BY ${safeSort} ${safeDir}
LIMIT 1000;
`;

  return { sql, params, safeSort, safeDir };
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

function buildOriginTreeFacet(rows) {
  return buildTreeFacet(rows, (row) => originKeyFromRow(row), { maxNodes: 650 });
}

function fetchRowsForFilterSet(db, filters, options = {}) {
  const { whereSql, params } = buildWhereClause(filters, options);
  const sql = `
SELECT path, origin, branch, lineage, last_commit_author, is_bare, first_seen_at, last_seen_at
FROM repositories
${whereSql};
`;
  let rows = db.prepare(sql).all(...params);
  if (!options.ignoreOriginPrefix && filters.originPrefix) {
    rows = rows.filter((row) => originHasPrefix(row, filters.originPrefix));
  }
  if (!options.ignorePathPrefix && filters.pathPrefix) {
    rows = rows.filter((row) => pathHasPrefix(row.path, filters.pathPrefix));
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

function originDisplay(row) {
  const upstream = String(row.origin || '').trim();
  if (upstream) return upstream;
  const lineage = String(row.lineage || '').trim();
  if (lineage.startsWith('local:')) return lineage;
  if (lineage) return `local:${lineage}`;
  return 'local:empty';
}

function readRows(dbPath, filters) {
  const db = new DatabaseSync(dbPath, { readOnly: true });
  try {
    const dbTotalStmt = db.prepare('SELECT count(*) as c FROM repositories;');
    const query = buildQuery(filters);
    const matchedRows = fetchRowsForFilterSet(db, filters);
    sortRows(matchedRows, query.safeSort, query.safeDir);

    const rows = matchedRows.slice(0, 1000).map((row) => ({ ...row, origin: originDisplay(row) }));
    const totalCount = matchedRows.length;
    const databaseTotal = dbTotalStmt.get().c;

    const bareFacetRows = fetchRowsForFilterSet(db, filters, { ignoreBare: true });
    const branchFacetRows = fetchRowsForFilterSet(db, filters, { ignoreBranch: true });
    const localPathTreeRows = fetchRowsForFilterSet(db, filters, { ignorePathPrefix: true });
    const originTreeRows = fetchRowsForFilterSet(db, filters, { ignoreOriginPrefix: true });

    const facets = {
      bare: aggregateBinaryFacet(
        bareFacetRows,
        (row) => (row.is_bare ? 'bare' : 'nonbare'),
        { bare: 'bare', nonbare: 'nonbare' }
      ),
      branch: aggregateFacetCounts(branchFacetRows.map((row) => row.branch)).slice(0, 30),
      localPathTree: buildLocalPathTreeFacet(localPathTreeRows),
      originTree: buildOriginTreeFacet(originTreeRows)
    };

    return { rows, totalCount, databaseTotal, query, facets };
  } finally {
    db.close();
  }
}

function renderPage({ rows, totalCount, q, sort, dir, syncState }) {
  const oppositeDir = dir === 'asc' ? 'desc' : 'asc';

  const links = ['path', 'origin', 'branch', 'last_commit_author', 'last_seen_at']
    .map((key) => {
      const href = `/?q=${encodeURIComponent(q)}&sort=${encodeURIComponent(key)}&dir=${encodeURIComponent(
        sort === key ? oppositeDir : 'asc'
      )}`;
      const marker = sort === key ? (dir === 'asc' ? ' ▲' : ' ▼') : '';
      return `<a href="${href}">${esc(key)}${marker}</a>`;
    })
    .join(' | ');

  let statusText = 'idle';
  if (syncState.running) {
    statusText = `${syncState.phase || 'sync'}: ${syncState.message || 'working'}${
      syncState.processedGitDirs ? ` (${syncState.processedGitDirs}/${syncState.discoveredGitDirs || '?'})` : ''
    }`;
  } else if (syncState.error) {
    statusText = `error: ${syncState.error}`;
  } else if (syncState.lastRunAt) {
    statusText = `last sync ${syncState.lastRunAt} using ${syncState.scanner || 'unknown'} indexed ${syncState.lastIndexed || 0} repos in ${syncState.durationMs || 0}ms`;
  }

  const body = rows
    .map((r) => {
      return `<tr>
<td><code>${esc(r.path)}</code></td>
<td>${r.origin ? `<code>${esc(r.origin)}</code>` : ''}</td>
<td>${r.branch ? `<code>${esc(r.branch)}</code>` : ''}</td>
<td>${r.last_commit_author ? `<code>${esc(r.last_commit_author)}</code>` : ''}</td>
<td>${esc(r.last_seen_at || '')}</td>
</tr>`;
    })
    .join('\n');

  return `<!doctype html>
<html>
<head>
  <meta charset="utf-8" />
  <meta name="viewport" content="width=device-width, initial-scale=1" />
  <title>reposview inspector</title>
  <style>
    :root { color-scheme: light; }
    body { font-family: ui-sans-serif, -apple-system, Segoe UI, Helvetica, Arial, sans-serif; margin: 1rem; }
    h1 { margin: 0 0 0.75rem; }
    form { display: flex; gap: 0.5rem; flex-wrap: wrap; margin-bottom: 0.75rem; }
    input[type="text"] { min-width: 28rem; max-width: 100%; padding: 0.35rem; }
    table { width: 100%; border-collapse: collapse; font-size: 0.9rem; }
    th, td { border: 1px solid #ddd; padding: 0.4rem; vertical-align: top; text-align: left; }
    tr:nth-child(even) { background: #fafafa; }
    code { white-space: nowrap; }
    .meta { margin: 0.5rem 0; color: #333; }
    .links { margin-bottom: 0.5rem; }
    .toolbar { display: flex; align-items: center; gap: 0.75rem; margin-bottom: 0.4rem; }
    .sync-btn { padding: 0.4rem 0.65rem; border: 1px solid #222; background: #fff; color: #111; text-decoration: none; }
    .sync-btn[disabled] { opacity: 0.55; pointer-events: none; }
    #sync-status { font-family: ui-monospace, SFMono-Regular, Menlo, monospace; }
  </style>
</head>
<body>
  <h1>reposview inspector</h1>
  <div class="toolbar">
    <button id="sync-btn" class="sync-btn" type="button" ${syncState.running ? 'disabled' : ''}>Sync now</button>
    <span id="sync-status">${esc(statusText)}</span>
  </div>
  <form method="get" action="/">
    <input type="text" name="q" value="${esc(q)}" placeholder="search path/origin/branch/last-author" />
    <input type="hidden" name="sort" value="${esc(sort)}" />
    <input type="hidden" name="dir" value="${esc(dir)}" />
    <button type="submit">Filter</button>
  </form>
  <div id="rows-meta" class="meta">rows: ${rows.length} / total: ${totalCount} (limit 1000)</div>
  <div class="links">sort: ${links}</div>
  <table>
    <thead>
      <tr>
        <th>path</th>
        <th>origin</th>
        <th>branch</th>
        <th>last_commit_author</th>
        <th>last_seen_at</th>
      </tr>
    </thead>
    <tbody id="rows-body">
      ${body}
    </tbody>
  </table>
  <script>
    const syncBtn = document.getElementById('sync-btn');
    const syncStatus = document.getElementById('sync-status');
    const rowsBody = document.getElementById('rows-body');
    const rowsMeta = document.getElementById('rows-meta');
    let lastRunAt = ${JSON.stringify(syncState.lastRunAt || null)};
    let pollTimer = null;
    let rowsTimer = null;

    function escHtml(value) {
      return String(value)
        .replaceAll('&', '&amp;')
        .replaceAll('<', '&lt;')
        .replaceAll('>', '&gt;')
        .replaceAll('"', '&quot;');
    }

    function renderRows(rows, totalCount) {
      if (!rowsBody || !rowsMeta) return;
      rowsMeta.textContent = 'rows: ' + rows.length + ' / total: ' + totalCount + ' (limit 1000)';

      rowsBody.innerHTML = rows.map((r) => {
        const origin = r.origin ? '<code>' + escHtml(r.origin) + '</code>' : '';
        const branch = r.branch ? '<code>' + escHtml(r.branch) + '</code>' : '';
        const lastCommitAuthor = r.last_commit_author ? '<code>' + escHtml(r.last_commit_author) + '</code>' : '';
        const path = '<code>' + escHtml(r.path) + '</code>';
        const lastSeen = escHtml(r.last_seen_at || '');
        return '<tr>' +
          '<td>' + path + '</td>' +
          '<td>' + origin + '</td>' +
          '<td>' + branch + '</td>' +
          '<td>' + lastCommitAuthor + '</td>' +
          '<td>' + lastSeen + '</td>' +
          '</tr>';
      }).join('');
    }

    function renderStatus(state) {
      if (state.running) {
        const progress = ' (' + (state.processedGitDirs || 0) + '/' + (state.discoveredGitDirs || '?') + ')';
        const persisted = state.persistedRepos ? ' saved=' + state.persistedRepos : '';
        syncStatus.textContent = (state.phase || 'sync') + ': ' + (state.message || 'working') + progress;
        syncStatus.textContent += persisted;
        syncBtn.disabled = true;
        return;
      }

      if (state.error) {
        syncStatus.textContent = 'error: ' + state.error;
        syncBtn.disabled = false;
        return;
      }

      if (state.lastRunAt) {
        syncStatus.textContent = 'last sync ' + state.lastRunAt + ' using ' + (state.scanner || 'unknown') + ' indexed ' + (state.lastIndexed || 0) + ' repos in ' + (state.durationMs || 0) + 'ms';
      } else {
        syncStatus.textContent = 'idle';
      }
      syncBtn.disabled = false;
    }

    async function fetchStatus() {
      try {
        const res = await fetch('/sync-status');
        if (!res.ok) return null;
        return await res.json();
      } catch {
        return null;
      }
    }

    function currentRowsUrl() {
      const params = new URLSearchParams(window.location.search);
      return '/rows?' + params.toString();
    }

    async function fetchRows() {
      try {
        const res = await fetch(currentRowsUrl());
        if (!res.ok) return null;
        return await res.json();
      } catch {
        return null;
      }
    }

    function startRowsPolling() {
      if (rowsTimer) return;
      rowsTimer = setInterval(async () => {
        const data = await fetchRows();
        if (!data) return;
        renderRows(data.rows || [], data.totalCount || 0);
      }, 1000);
    }

    function stopRowsPolling() {
      if (!rowsTimer) return;
      clearInterval(rowsTimer);
      rowsTimer = null;
    }

    function startPolling() {
      if (pollTimer) return;
      pollTimer = setInterval(async () => {
        const state = await fetchStatus();
        if (!state) return;
        renderStatus(state);
        if (state.running) {
          startRowsPolling();
        } else {
          stopRowsPolling();
        }
        if (!state.running && state.lastRunAt && state.lastRunAt !== lastRunAt) {
          lastRunAt = state.lastRunAt;
          const data = await fetchRows();
          if (data) {
            renderRows(data.rows || [], data.totalCount || 0);
          } else {
            setTimeout(() => window.location.reload(), 250);
          }
        }
      }, 1000);
    }

    async function triggerSync() {
      if (!syncBtn || syncBtn.disabled) return;
      syncBtn.disabled = true;
      syncStatus.textContent = 'starting sync...';
      try {
        const res = await fetch('/sync', { method: 'POST' });
        if (!res.ok) {
          syncStatus.textContent = 'failed to start sync';
          syncBtn.disabled = false;
          return;
        }
        syncStatus.textContent = 'sync requested...';
        startPolling();
      } catch {
        syncStatus.textContent = 'failed to start sync';
        syncBtn.disabled = false;
      }
    }

    if (syncBtn) syncBtn.addEventListener('click', triggerSync);

    const stream = new EventSource('/events');
    stream.onmessage = (event) => {
      try {
        const state = JSON.parse(event.data);
        renderStatus(state);
        if (state.running) {
          startRowsPolling();
        } else {
          stopRowsPolling();
        }
        if (state.lastRunAt && state.lastRunAt !== lastRunAt) {
          lastRunAt = state.lastRunAt;
          fetchRows().then((data) => {
            if (data) {
              renderRows(data.rows || [], data.totalCount || 0);
            } else {
              setTimeout(() => window.location.reload(), 250);
            }
          });
        }
      } catch {
        // Ignore parse errors.
      }
    };

    stream.onerror = () => {
      syncStatus.textContent = 'status stream disconnected';
      startPolling();
    };
  </script>
</body>
</html>`;
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

    if (url.pathname !== '/') {
      res.statusCode = 404;
      res.setHeader('Content-Type', 'text/plain; charset=utf-8');
      res.end('not found');
      return;
    }

    const filters = filtersFromUrl(url);
    const data = readRows(dbPath, filters);
    const html = renderPage({
      rows: data.rows,
      totalCount: data.totalCount,
      q: filters.q,
      sort: data.query.safeSort,
      dir: data.query.safeDir.toLowerCase(),
      syncState
    });

    res.statusCode = 200;
    res.setHeader('Content-Type', 'text/html; charset=utf-8');
    res.end(html);
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
