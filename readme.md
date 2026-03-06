# reposview

Currently I have all sorts of git clones scattered across my file system

some are work-related, some personal, some linked to agents' projects. While I have some conventions for organizing them, the increasing pace of agent interactions made this management too difficult.

I want a catalog of all repositories on my system, including their origins and other useful metadata.

## Goals

- Cataloging
	- Understand all repositories present on the system.
	- Identify repositories of interest quickly.
- Planning
	- Gain a clear view of the code-space.
	- Facilitate high-level system planning and modular component development.
- Maintenance and curation
	- Reorganize repository locations on the filesystem.
	- Remove or archive irrelevant repositories.

## repo-identifier

The **root commit hash** is git's closest thing to a birth certificate. Every clone of a repo shares it. You get it with:

```bash
git rev-list --max-parents=0 HEAD
```

A repo can have multiple roots (e.g. after merging unrelated histories). In that case, sort them lexicographically and concatenate with `+`.

```
# single root
lineage = a1b2c3d4...

# multiple roots (rare)
lineage = 1111aaaa... + 9999ffff...
```

Two repos with the same lineage were born from the same initial commit. This covers: clones, forks, mirrors, and copies.

## Initial indexer

Node.js sync command (no Python):

```bash
pnpm run sync:index -- --root / --db ./data/reposview.sqlite
```

Defaults:

- Scans from `$HOME`
- Excludes hidden top-level folders under `$HOME` using regex like:
  `^/home/your-user/\.[^/]+(?:/|$)`
- Uses a pure Node.js recursive directory walk (no Python, no shelling out to `git`)
- Upserts into SQLite with sync behavior:
  - updates `last_seen_at` for repos found in the current run
  - sets `missing_at` when a previously indexed repo is not found anymore

Useful options:

```bash
# Preview discovered repos without writing to DB
pnpm run sync:index -- --root / --dry-run

# Override exclusion
pnpm run sync:index -- --exclude-regex '^$'
```

## Inspect UI (dev)

Run the local UI:

```bash
pnpm run dev -- --db ./data/reposview.sqlite --port 8790 --api-port 8787
```

Then open:

```text
http://127.0.0.1:8790
```

From the UI, click `Sync now` to run a background index with live progress updates.

Performance note:

- Default scanner mode is `auto`:
  - uses `fdfind`/`fd` when available (faster)
  - falls back to Node recursive scan
- Restrict scan roots for faster runs, e.g. `--root "$HOME"`
