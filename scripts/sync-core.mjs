import fs from 'node:fs';
import path from 'node:path';
import { spawnSync } from 'node:child_process';
import * as git from 'isomorphic-git';

export const DEFAULT_SYNC_OPTIONS = {
  db: './data/reposview.sqlite',
  roots: [process.env.HOME || '/'],
  excludeRegex: process.env.HOME
    ? `^${String(process.env.HOME).replace(/[.*+?^${}()|[\]\\]/g, '\\$&')}/\\.[^/]+(?:/|$)`
    : '^$',
  dryRun: false,
  verbose: false,
  scanner: 'auto'
};

export function parseSyncArgs(argv) {
  const opts = { ...DEFAULT_SYNC_OPTIONS };

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
    if (arg === '--dry-run') {
      opts.dryRun = true;
      continue;
    }
    if (arg === '--verbose') {
      opts.verbose = true;
      continue;
    }
    if (arg === '--help' || arg === '-h') {
      return { ...opts, help: true };
    }
    throw new Error(`Unknown argument: ${arg}`);
  }

  return opts;
}

export function printSyncHelp() {
  process.stdout.write('Usage: pnpm run sync:index -- [options]\n\n');
  process.stdout.write('Options:\n');
  process.stdout.write('  --db <path>             SQLite database path (default: ./data/reposview.sqlite)\n');
  process.stdout.write('  --root <path>           Single scan root (default: /)\n');
  process.stdout.write('  --roots <a,b,c>         Comma-separated scan roots\n');
  process.stdout.write('  --exclude-regex <expr>  Path exclusion regex\n');
  process.stdout.write('  --scanner <mode>        auto|fdfind|fd|node (default: auto)\n');
  process.stdout.write('  --dry-run               Discover only, no DB writes\n');
  process.stdout.write('  --verbose               Print skip reasons\n');
}

function commandExists(cmd) {
  const out = spawnSync('bash', ['-lc', `command -v ${cmd}`], { encoding: 'utf8' });
  return out.status === 0;
}

function discoverWithFd(command, roots, exclude) {
  const out = spawnSync(
    command,
    ['--hidden', '--no-ignore', '--type', 'd', '--glob', '*.git', ...roots],
    {
      encoding: 'utf8',
      maxBuffer: 256 * 1024 * 1024
    }
  );

  if (out.error || (out.status !== 0 && out.status !== 1)) {
    throw out.error || new Error(`${command} failed with status ${out.status}`);
  }

  const gitDirs = out.stdout
    .split('\n')
    .map((s) => s.trim())
    .filter(Boolean)
    .map((p) => path.resolve(p))
    .filter((p) => !exclude.test(p));

  return { gitDirs, scanner: command, scannedDirectories: null };
}

function discoverWithNodeWalk(roots, exclude, onProgress) {
  const found = [];
  const seen = new Set();
  const stack = roots.map((r) => path.resolve(r));
  let scannedDirectories = 0;

  while (stack.length > 0) {
    const dir = stack.pop();
    if (seen.has(dir)) continue;
    seen.add(dir);
    scannedDirectories += 1;

    if (scannedDirectories % 2000 === 0 && onProgress) {
      onProgress({ phase: 'scan', scannedDirectories, discoveredGitDirs: found.length });
    }

    let entries;
    try {
      entries = fs.readdirSync(dir, { withFileTypes: true });
    } catch {
      continue;
    }

    for (const entry of entries) {
      if (!entry.isDirectory()) continue;
      const full = path.join(dir, entry.name);
      if (exclude.test(full)) continue;

      if (entry.name.endsWith('.git')) {
        found.push(full);
        continue;
      }

      stack.push(full);
    }
  }

  return { gitDirs: found, scanner: 'node', scannedDirectories };
}

function discoverGitDirs(roots, exclude, scannerMode, onProgress) {
  const tryFd = scannerMode === 'auto' || scannerMode === 'fdfind' || scannerMode === 'fd';

  if (tryFd && (scannerMode === 'auto' || scannerMode === 'fdfind') && commandExists('fdfind')) {
    try {
      return discoverWithFd('fdfind', roots, exclude);
    } catch {
      if (scannerMode === 'fdfind') throw new Error('fdfind failed');
    }
  }

  if (tryFd && (scannerMode === 'auto' || scannerMode === 'fd') && commandExists('fd')) {
    try {
      return discoverWithFd('fd', roots, exclude);
    } catch {
      if (scannerMode === 'fd') throw new Error('fd failed');
    }
  }

  return discoverWithNodeWalk(roots, exclude, onProgress);
}

function toRepoInfoFromGitDir(gitDir) {
  const normalizedGitDir = gitDir.replace(/\/+$/, '');
  const base = path.basename(normalizedGitDir);

  if (base === '.git') {
    return {
      path: path.dirname(normalizedGitDir),
      gitDir: normalizedGitDir,
      ctx: { fs, dir: path.dirname(normalizedGitDir), gitdir: normalizedGitDir }
    };
  }

  return {
    path: normalizedGitDir,
    gitDir: normalizedGitDir,
    ctx: { fs, gitdir: normalizedGitDir }
  };
}

function isLikelyBareRepoDir(repoPath) {
  return fs.existsSync(path.join(repoPath, 'HEAD')) && fs.existsSync(path.join(repoPath, 'objects'));
}

function toRepoInfoFromPath(repoPath) {
  const resolved = path.resolve(repoPath);
  const dotGit = path.join(resolved, '.git');

  if (fs.existsSync(dotGit) && fs.statSync(dotGit).isDirectory()) {
    return toRepoInfoFromGitDir(dotGit);
  }

  if (isLikelyBareRepoDir(resolved)) {
    return toRepoInfoFromGitDir(resolved);
  }

  throw new Error(`path is not a git repository: ${resolved}`);
}

async function getRepoMetadata(repo, verbose = false) {
  void verbose;

  let isBare = 0;
  try {
    const bareCfg = await git.getConfig({ ...repo.ctx, path: 'core.bare' });
    isBare = String(bareCfg || '').trim() === 'true' ? 1 : 0;
  } catch {
    isBare = repo.path === repo.gitDir ? 1 : 0;
  }

  let origin = null;
  try {
    const cfg = await git.getConfig({ ...repo.ctx, path: 'remote.origin.url' });
    origin = cfg && String(cfg).trim().length > 0 ? String(cfg).trim() : null;
  } catch {
    origin = null;
  }

  let headOid = null;
  try {
    headOid = await git.resolveRef({ ...repo.ctx, ref: 'HEAD' });
  } catch {
    headOid = null;
  }

  let branch = null;
  try {
    branch = await git.currentBranch({ ...repo.ctx, fullname: false });
    if (!branch) {
      branch = headOid ? `DETACHED@${headOid.slice(0, 10)}` : null;
    }
  } catch {
    branch = null;
  }

  // Fast identifier rule:
  // 1) origin URL if available
  // 2) HEAD commit hash
  // 3) local:empty for repos without commits
  let lineage = origin;
  if (!lineage) {
    lineage = headOid || 'local:empty';
  }

  let lastCommitAuthor = null;
  if (headOid) {
    try {
      const commit = await git.readCommit({ ...repo.ctx, oid: headOid });
      const author = commit?.commit?.author;
      const name = String(author?.name || '').trim();
      const email = String(author?.email || '').trim();
      if (name && email) lastCommitAuthor = `${name} <${email}>`;
      else if (name) lastCommitAuthor = name;
      else if (email) lastCommitAuthor = email;
    } catch {
      lastCommitAuthor = null;
    }
  }

  return {
    path: repo.path,
    git_dir: repo.gitDir,
    lineage,
    origin,
    branch,
    last_commit_author: lastCommitAuthor,
    is_bare: isBare
  };
}

function ensureColumn(db, tableName, columnName, alterSql) {
  const rows = db.prepare(`PRAGMA table_info(${tableName});`).all();
  if (!rows.some((row) => row.name === columnName)) {
    db.exec(alterSql);
  }
}

function ensureSchema(dbPath, DatabaseSync) {
  fs.mkdirSync(path.dirname(dbPath), { recursive: true });
  const db = new DatabaseSync(dbPath);

  db.exec(`
CREATE TABLE IF NOT EXISTS repositories (
  path TEXT PRIMARY KEY,
  git_dir TEXT NOT NULL,
  lineage TEXT,
  origin TEXT,
  branch TEXT,
  last_commit_author TEXT,
  is_bare INTEGER NOT NULL DEFAULT 0,
  first_seen_at TEXT NOT NULL,
  last_seen_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_repositories_lineage ON repositories(lineage);
CREATE INDEX IF NOT EXISTS idx_repositories_origin ON repositories(origin);
`);

  ensureColumn(db, 'repositories', 'last_commit_author', 'ALTER TABLE repositories ADD COLUMN last_commit_author TEXT;');

  return db;
}

function prepareSyncStatements(db) {
  const upsert = db.prepare(`
INSERT INTO repositories (path, git_dir, lineage, origin, branch, last_commit_author, is_bare, first_seen_at, last_seen_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(path) DO UPDATE SET
  git_dir=excluded.git_dir,
  lineage=excluded.lineage,
  origin=excluded.origin,
  branch=excluded.branch,
  last_commit_author=excluded.last_commit_author,
  is_bare=excluded.is_bare,
  last_seen_at=excluded.last_seen_at;
`);

  const pruneMissing = db.prepare('DELETE FROM repositories WHERE last_seen_at < ?;');

  return { upsert, pruneMissing };
}

function flushRows(db, upsert, rows, now) {
  if (rows.length === 0) return 0;

  db.exec('BEGIN;');
  try {
    for (const row of rows) {
      upsert.run(
        row.path,
        row.git_dir,
        row.lineage,
        row.origin,
        row.branch,
        row.last_commit_author,
        row.is_bare,
        now,
        now
      );
    }
    db.exec('COMMIT;');
  } catch (err) {
    db.exec('ROLLBACK;');
    throw err;
  }

  return rows.length;
}

function finalizeSync(db, pruneMissing, now) {
  pruneMissing.run(now);
}

export async function runSync(rawOptions = {}) {
  const opts = { ...DEFAULT_SYNC_OPTIONS, ...rawOptions };
  const onProgress = typeof opts.onProgress === 'function' ? opts.onProgress : null;
  const exclude = new RegExp(opts.excludeRegex);
  const roots = opts.roots.map((r) => path.resolve(r));
  const dbPath = path.resolve(opts.db);
  const startedAtMs = Date.now();
  const now = new Date().toISOString();

  if (onProgress) {
    onProgress({ phase: 'scan', message: 'discovering repositories', roots, scanner: opts.scanner });
  }

  const discovery = discoverGitDirs(roots, exclude, opts.scanner, onProgress);
  const gitDirs = discovery.gitDirs;

  if (onProgress) {
    onProgress({
      phase: 'metadata',
      message: 'reading git metadata',
      scanner: discovery.scanner,
      scannedDirectories: discovery.scannedDirectories,
      discoveredGitDirs: gitDirs.length
    });
  }

  const dedup = new Set();
  const repos = [];
  const BATCH_SIZE = 25;
  let persistedRepos = 0;

  let db = null;
  let upsert = null;
  let pruneMissing = null;

  if (!opts.dryRun) {
    const { DatabaseSync } = await import('node:sqlite');
    db = ensureSchema(dbPath, DatabaseSync);
    const statements = prepareSyncStatements(db);
    upsert = statements.upsert;
    pruneMissing = statements.pruneMissing;
  }

  for (let i = 0; i < gitDirs.length; i += 1) {
    const gitDir = gitDirs[i];
    const repo = toRepoInfoFromGitDir(gitDir);
    if (exclude.test(repo.path)) continue;
    if (dedup.has(repo.path)) continue;
    dedup.add(repo.path);

    const metadata = await getRepoMetadata(repo, opts.verbose);
    if (metadata) {
      repos.push(metadata);
      if (!opts.dryRun && repos.length >= BATCH_SIZE) {
        persistedRepos += flushRows(db, upsert, repos, now);
        repos.length = 0;
      }
    }

    if (onProgress && (i === gitDirs.length - 1 || (i + 1) % 25 === 0)) {
      onProgress({
        phase: 'metadata',
        message: 'reading git metadata',
        scanner: discovery.scanner,
        scannedDirectories: discovery.scannedDirectories,
        discoveredGitDirs: gitDirs.length,
        processedGitDirs: i + 1,
        indexedRepos: opts.dryRun ? repos.length : persistedRepos + repos.length,
        persistedRepos
      });
    }
  }

  if (!opts.dryRun) {
    if (onProgress) {
      onProgress({
        phase: 'write',
        message: 'writing sqlite updates',
        discoveredGitDirs: gitDirs.length,
        indexedRepos: persistedRepos + repos.length,
        persistedRepos
      });
    }

    try {
      persistedRepos += flushRows(db, upsert, repos, now);
      repos.length = 0;
      finalizeSync(db, pruneMissing, now);
    } finally {
      db.close();
    }
  }

  return {
    scannedGitDirs: gitDirs.length,
    scannedDirectories: discovery.scannedDirectories,
    indexedRepos: opts.dryRun ? repos.length : persistedRepos,
    dbPath,
    at: now,
    scanner: discovery.scanner,
    durationMs: Date.now() - startedAtMs
  };
}

export async function reindexRepositoryByPath(dbPath, repoPath) {
  const resolvedDb = path.resolve(dbPath);
  const resolvedRepo = path.resolve(repoPath);
  const now = new Date().toISOString();
  const repo = toRepoInfoFromPath(resolvedRepo);
  const metadata = await getRepoMetadata(repo);

  const { DatabaseSync } = await import('node:sqlite');
  const db = ensureSchema(resolvedDb, DatabaseSync);

  try {
    const { upsert } = prepareSyncStatements(db);
    flushRows(db, upsert, [metadata], now);
  } finally {
    db.close();
  }

  return { ...metadata, last_seen_at: now };
}

export async function deleteRepositoryByPath(dbPath, repoPath) {
  const resolvedDb = path.resolve(dbPath);
  const resolvedRepo = path.resolve(repoPath);

  const { DatabaseSync } = await import('node:sqlite');
  const db = ensureSchema(resolvedDb, DatabaseSync);

  try {
    const stmt = db.prepare('DELETE FROM repositories WHERE path = ?;');
    const result = stmt.run(resolvedRepo);
    const changes = Number(result?.changes || 0);
    if (changes === 0) {
      return { deleted: false, path: resolvedRepo, error: 'path not indexed' };
    }
    return { deleted: true, path: resolvedRepo };
  } finally {
    db.close();
  }
}
