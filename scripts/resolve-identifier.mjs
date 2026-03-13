import fs from 'node:fs';
import path from 'node:path';
import * as git from 'isomorphic-git';

export async function getRootHash(ctx, headOid) {
  if (!headOid) return null;

  const seen = new Set();
  const queue = [headOid];
  let qHead = 0;
  const roots = [];

  while (qHead < queue.length) {
    const oid = queue[qHead++];
    if (seen.has(oid)) continue;
    seen.add(oid);

    let commit;
    try {
      commit = await git.readCommit({ ...ctx, oid });
    } catch {
      continue;
    }

    const parents = commit.commit.parent || [];
    if (parents.length === 0) {
      roots.push(oid);
    } else {
      for (const parent of parents) {
        if (!seen.has(parent)) queue.push(parent);
      }
    }
  }

  if (roots.length === 0) return null;
  roots.sort();
  return roots.join(' + ');
}

export async function resolveIdentifier(ctx, headOid = null) {
  let identifier = null;
  try {
    const cfg = await git.getConfig({ ...ctx, path: 'remote.origin.url' });
    identifier = cfg && String(cfg).trim().length > 0 ? String(cfg).trim() : null;
  } catch {
    identifier = null;
  }

  let lineage = identifier;
  if (!lineage) {
    const rootHash = await getRootHash(ctx, headOid);
    lineage = rootHash ? `local:${rootHash}` : 'local:none';
  }

  return { identifier, lineage };
}

export function toRepoInfoFromGitDir(gitDir) {
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

export function toRepoInfoFromGitFile(repoPath, gitFile) {
  const content = fs.readFileSync(gitFile, 'utf8');
  const match = content.match(/^gitdir:\s*(.+)\s*$/im);
  if (!match) {
    throw new Error(`invalid .git file: ${gitFile}`);
  }

  const gitDir = path.resolve(repoPath, match[1].trim());
  return {
    path: repoPath,
    gitDir,
    ctx: { fs, dir: repoPath, gitdir: gitDir }
  };
}

export function isLikelyBareRepoDir(repoPath) {
  return fs.existsSync(path.join(repoPath, 'HEAD')) && fs.existsSync(path.join(repoPath, 'objects'));
}

export function findRepoInfoFromPath(repoPath) {
  let current = path.resolve(repoPath);

  while (true) {
    const dotGit = path.join(current, '.git');
    if (fs.existsSync(dotGit)) {
      const stat = fs.statSync(dotGit);
      if (stat.isDirectory()) return toRepoInfoFromGitDir(dotGit);
      if (stat.isFile()) return toRepoInfoFromGitFile(current, dotGit);
    }

    if (isLikelyBareRepoDir(current)) {
      return toRepoInfoFromGitDir(current);
    }

    const parent = path.dirname(current);
    if (parent === current) break;
    current = parent;
  }

  return null;
}

export function toRepoInfoFromPath(repoPath) {
  const repo = findRepoInfoFromPath(repoPath);
  if (repo) return repo;
  return {
    path: path.resolve(repoPath),
    gitDir: null,
    ctx: null
  };
}

export async function resolveRepoIdentifier(repoPath) {
  const repo = toRepoInfoFromPath(repoPath);

  if (!repo.ctx) {
    return {
      path: repo.path,
      gitDir: null,
      headOid: null,
      identifier: null,
      lineage: 'local:none',
      resolvedIdentifier: 'local:none'
    };
  }

  let headOid = null;
  try {
    headOid = await git.resolveRef({ ...repo.ctx, ref: 'HEAD' });
  } catch {
    headOid = null;
  }

  const { identifier, lineage } = await resolveIdentifier(repo.ctx, headOid);
  return {
    path: repo.path,
    gitDir: repo.gitDir,
    headOid,
    identifier,
    lineage,
    resolvedIdentifier: identifierDisplayFromParts(identifier, lineage)
  };
}

export function parseIdentifierToCanonicalKey(rawIdentifier) {
  const raw = String(rawIdentifier || '').trim();
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

export function identifierKeyFromParts(identifier, lineage) {
  const upstream = String(identifier || '').trim();
  if (!upstream) {
    const lineageValue = String(lineage || '').trim();
    return lineageValue ? `local/${lineageValue}` : 'local/none';
  }
  return parseIdentifierToCanonicalKey(upstream);
}

export function identifierKeyFromRow(row) {
  return identifierKeyFromParts(row?.identifier, row?.lineage);
}

export function identifierDisplayFromParts(identifier, lineage) {
  const upstream = String(identifier || '').trim();
  if (upstream) return upstream;

  const lineageValue = String(lineage || '').trim();
  if (lineageValue.startsWith('local:')) return lineageValue;
  if (lineageValue) return `local:${lineageValue}`;
  return 'local:none';
}

export function identifierDisplayFromRow(row) {
  return identifierDisplayFromParts(row?.identifier, row?.lineage);
}
