import 'tabulator-tables/dist/css/tabulator.min.css';
import { TabulatorFull as Tabulator } from 'tabulator-tables';
import { marked } from 'marked';

const syncBtn = document.getElementById('sync-btn');
const syncStatus = document.getElementById('sync-status');
const rowsMeta = document.getElementById('rows-meta');
const rowsTable = document.getElementById('rows-table');
const filters = document.getElementById('filters');
const qInput = document.getElementById('q');
const activeFilters = document.getElementById('active-filters');
const pathTree = document.getElementById('path-tree');
const originTree = document.getElementById('origin-tree');
let repoDetails = document.getElementById('repo-details');

if (!repoDetails) {
  const results = document.querySelector('.results');
  if (results) {
    repoDetails = document.createElement('section');
    repoDetails.id = 'repo-details';
    repoDetails.className = 'repo-details is-hidden';
    repoDetails.setAttribute('aria-live', 'polite');
    if (rowsTable && rowsTable.parentElement === results) {
      results.insertBefore(repoDetails, rowsTable);
    } else {
      results.appendChild(repoDetails);
    }
  }
}

const sortKeys = ['path', 'origin', 'branch', 'last_commit_author', 'last_commit_at', 'last_seen_at'];
let pollTimer = null;
let rowsTimer = null;
let lastRunAt = null;
let lastFacets = {};
let suppressSortSync = false;
let repoDetailsRequestId = 0;
let pendingTreeFocus = null;

const collapsedTreeState = {
  path: new Set(),
  origin: new Set()
};

const treeNodeIndex = {
  path: new Map(),
  origin: new Map()
};

function esc(value) {
  return String(value)
    .replaceAll('&', '&amp;')
    .replaceAll('<', '&lt;')
    .replaceAll('>', '&gt;')
    .replaceAll('"', '&quot;');
}

function stateFromUrl() {
  const params = new URLSearchParams(window.location.search);
  const state = params.get('state');
  const bare = params.get('bare');

  return {
    q: params.get('q') || '',
    state: state === 'active' || state === 'missing' || state === 'all' ? state : params.get('missing') === '1' ? 'all' : 'active',
    bare: bare === 'bare' || bare === 'nonbare' || bare === 'all' ? bare : 'all',
    branch: params.get('branch') || '',
    author: params.get('author') || '',
    pathPrefix: params.get('path_prefix') || '',
    originPrefix: params.get('origin_prefix') || '',
    sort: sortKeys.includes(params.get('sort') || '') ? params.get('sort') : 'path',
    dir: params.get('dir') === 'desc' ? 'desc' : 'asc'
  };
}

function writeUrlState(state) {
  const params = new URLSearchParams();
  if (state.q) params.set('q', state.q);
  if (state.state !== 'active') params.set('state', state.state);
  if (state.bare !== 'all') params.set('bare', state.bare);
  if (state.branch) params.set('branch', state.branch);
  if (state.author) params.set('author', state.author);
  if (state.pathPrefix) params.set('path_prefix', state.pathPrefix);
  if (state.originPrefix) params.set('origin_prefix', state.originPrefix);
  params.set('sort', state.sort);
  params.set('dir', state.dir);
  const query = params.toString();
  history.replaceState(null, '', query ? `?${query}` : '/');
}

function codeFormatter(value) {
  if (!value) return '';
  return `<code>${esc(value)}</code>`;
}

function pathToGhosttyHref(pathValue) {
  const raw = String(pathValue || '').trim();
  if (!raw) return null;
  if (raw.startsWith('/')) return `ghostty://${encodeURI(raw)}`;
  return `ghostty:///${encodeURI(raw)}`;
}

function pathFormatter(cell) {
  const value = cell.getValue();
  if (!value) return '';
  return `<button type="button" class="path-open-btn" data-path="${esc(value)}"><code>${esc(value)}</code></button>`;
}

function originToHref(origin) {
  const raw = String(origin || '').trim();
  if (!raw || raw.startsWith('local:')) return null;

  if (raw.startsWith('http://') || raw.startsWith('https://')) {
    return raw;
  }

  const scpLike = raw.match(/^[^@]+@([^:\/\s]+):(.+)$/);
  if (scpLike) {
    const host = scpLike[1];
    const path = scpLike[2].replace(/\.git$/i, '');
    return `https://${host}/${path}`;
  }

  try {
    const parsed = new URL(raw);
    if (parsed.protocol === 'ssh:' || parsed.protocol === 'git:') {
      const path = parsed.pathname.replace(/^\//, '').replace(/\.git$/i, '');
      return `https://${parsed.hostname}/${path}`;
    }
  } catch {
    return null;
  }

  return null;
}

function originFormatter(cell) {
  const value = cell.getValue();
  if (!value) return '';
  const href = originToHref(value);
  if (!href) return `<code>${esc(value)}</code>`;
  return `<a class="origin-link" href="${esc(href)}" target="_blank" rel="noopener noreferrer"><code>${esc(value)}</code></a>`;
}

function authorFormatter(cell) {
  const value = String(cell.getValue() || '').trim();
  if (!value) return '';
  return `<button type="button" class="author-filter-btn" data-author="${esc(value)}"><code>${esc(value)}</code></button>`;
}

function reindexFormatter(cell) {
  const value = String(cell.getValue() || '').trim();
  const row = cell.getRow().getData();
  const repoPath = String(row?.path || '').trim();
  if (!value) return '';
  if (!repoPath) return esc(value);
  return `<button type="button" class="reindex-btn" data-path="${esc(repoPath)}">${esc(value)}</button>`;
}

const table = new Tabulator(rowsTable, {
  index: 'path',
  layout: 'fitColumns',
  responsiveLayout: false,
  height: '100%',
  placeholder: 'No repositories match the current filters',
  movableColumns: true,
  resizableColumns: true,
  headerSortElement: '<span>↕</span>',
  initialSort: [{ column: 'path', dir: 'asc' }],
  columns: [
    { title: 'path', field: 'path', sorter: 'string', formatter: pathFormatter },
    { title: 'origin', field: 'origin', sorter: 'string', formatter: originFormatter },
    { title: 'branch', field: 'branch', sorter: 'string', formatter: (cell) => codeFormatter(cell.getValue()) },
    {
      title: 'last author',
      field: 'last_commit_author',
      sorter: 'string',
      minWidth: 220,
      widthGrow: 2,
      formatter: authorFormatter
    },
    { title: 'last commit', field: 'last_commit_at', sorter: 'string', minWidth: 170 },
    { title: 'last_seen_at', field: 'last_seen_at', sorter: 'string', formatter: reindexFormatter }
  ]
});

rowsTable.tabIndex = 0;

table.on('dataSorted', (sorters) => {
  if (suppressSortSync) return;
  const next = stateFromUrl();
  const first = sorters?.[0];
  if (first && sortKeys.includes(first.field)) {
    next.sort = first.field;
    next.dir = first.dir === 'desc' ? 'desc' : 'asc';
  } else {
    next.sort = 'path';
    next.dir = 'asc';
  }
  writeUrlState(next);
  void refreshRows();
});

function syncTabulatorSort(state) {
  table.setSort([{ column: state.sort, dir: state.dir }]);
}

function labelForFacetValue(value) {
  if (value === '__none__') return '(none)';
  if (!value) return '(empty)';
  return value;
}

function applyStateToControls(state) {
  qInput.value = state.q;
}

function renderRows(rows, totalCount, databaseTotal, state) {
  rowsMeta.textContent = `rows: ${rows.length} / matched: ${totalCount} / total: ${databaseTotal} (limit 1000)`;
  suppressSortSync = true;
  syncTabulatorSort(state);
  Promise.resolve(table.replaceData(rows)).finally(() => {
    setTimeout(() => {
      suppressSortSync = false;
    }, 0);
  });
}

function detailsValue(value, { code = false } = {}) {
  const text = String(value || '').trim();
  if (!text) return '<span class="repo-details-empty">(none)</span>';
  if (code) return `<code>${esc(text)}</code>`;
  return esc(text);
}

function buildRowCard(row) {
  return `<div class="repo-row-card">
    <div class="repo-row-item"><span class="repo-row-key">path</span><span class="repo-row-val"><button type="button" class="path-open-btn" data-path="${esc(row.path || '')}"><code>${esc(row.path || '')}</code></button></span></div>
    <div class="repo-row-item"><span class="repo-row-key">origin</span><span class="repo-row-val">${originFormatter({ getValue: () => row.origin })}</span></div>
    <div class="repo-row-item"><span class="repo-row-key">branch</span><span class="repo-row-val">${detailsValue(row.branch, { code: true })}</span></div>
    <div class="repo-row-item"><span class="repo-row-key">last author</span><span class="repo-row-val">${authorFormatter({ getValue: () => row.last_commit_author })}</span></div>
    <div class="repo-row-item"><span class="repo-row-key">last commit</span><span class="repo-row-val">${detailsValue(row.last_commit_at)}</span></div>
    <div class="repo-row-item"><span class="repo-row-key">last seen</span><span class="repo-row-val">${detailsValue(row.last_seen_at)}</span></div>
  </div>`;
}

function renderRepoDetailsLoading(row) {
  if (!repoDetails) return;
  repoDetails.classList.remove('is-hidden');
  repoDetails.innerHTML = `<h3>Repository details</h3>
    ${buildRowCard(row)}
    <div class="repo-details-empty">loading README...</div>`;
}

function renderRepoDetails(row, payload) {
  if (!repoDetails) return;
  const readme = payload?.readme || { exists: false, content: '', truncated: false, path: null };
  const readmeHtml = readme.exists ? marked.parse(String(readme.content || ''), { gfm: true, breaks: true }) : '';
  const readmeBody = readme.exists
    ? `<h3>README ${readme.path ? `<code>${esc(readme.path)}</code>` : ''}</h3>
       <div class="repo-markdown">${readmeHtml}</div>
       ${readme.truncated ? '<div class="repo-details-empty">README preview is truncated.</div>' : ''}`
    : '<div class="repo-details-empty">README not found in this repository root.</div>';

  repoDetails.classList.remove('is-hidden');
  repoDetails.innerHTML = `<h3>Repository details</h3>
    ${buildRowCard(row)}
    ${readmeBody}`;
}

async function refreshRepoDetails(rows) {
  const requestId = ++repoDetailsRequestId;
  if (!repoDetails) return;

  if (rows.length !== 1) {
    repoDetails.classList.add('is-hidden');
    repoDetails.innerHTML = '';
    return;
  }

  const row = rows[0];
  renderRepoDetailsLoading(row);

  try {
    const params = new URLSearchParams({ path: row.path || '' });
    const res = await fetch(`/repo-details?${params.toString()}`);
    const payload = await res.json().catch(() => ({}));
    if (requestId !== repoDetailsRequestId) return;
    if (!res.ok) throw new Error(payload?.error || 'failed to load repository details');
    renderRepoDetails(row, payload);
  } catch (error) {
    if (requestId !== repoDetailsRequestId) return;
    repoDetails.classList.remove('is-hidden');
    repoDetails.innerHTML = `<h3>Repository details</h3>
      <div class="repo-details-empty">${esc(error instanceof Error ? error.message : String(error))}</div>`;
  }
}

function sortTreeNodes(nodes) {
  nodes.sort((a, b) => String(a.label || '').localeCompare(String(b.label || '')));
  for (const node of nodes) sortTreeNodes(node.children);
}

function buildHierarchy(nodes) {
  const byPrefix = new Map();
  for (const node of nodes) byPrefix.set(node.prefix, { ...node, children: [] });

  const roots = [];
  for (const node of byPrefix.values()) {
    const parent = node.parentPrefix ? byPrefix.get(node.parentPrefix) : null;
    if (parent) parent.children.push(node);
    else roots.push(node);
  }

  sortTreeNodes(roots);
  return roots;
}

function syncCollapsedState(nodes, collapsedSet) {
  const valid = new Set(nodes.map((node) => node.prefix));
  for (const prefix of [...collapsedSet]) {
    if (!valid.has(prefix)) collapsedSet.delete(prefix);
  }
}

function indexTree(facetName, nodes) {
  const map = new Map();
  for (const node of nodes) {
    map.set(node.prefix, {
      parentPrefix: node.parentPrefix || null,
      children: node.children.map((child) => child.prefix)
    });
  }
  treeNodeIndex[facetName] = map;
}

function renderTreeBranch(nodes, selectedPrefix, facetName, collapsedSet) {
  return `<ul class="tree-branch">${nodes
    .map((node) => {
      const hasChildren = node.children.length > 0;
      const isCollapsed = hasChildren && collapsedSet.has(node.prefix);
      const isSelected = node.prefix === selectedPrefix;
      const rowClass = isSelected ? 'tree-row is-active' : 'tree-row';
      const label = esc(node.label || node.prefix);
      const toggleGlyph = hasChildren ? (isCollapsed ? '▸' : '▾') : '·';
      const children = hasChildren && !isCollapsed ? renderTreeBranch(node.children, selectedPrefix, facetName, collapsedSet) : '';

      return `<li class="tree-item">
        <div class="${rowClass}">
          <button type="button" class="tree-toggle" data-action="toggle" data-tree="${facetName}" data-prefix="${esc(node.prefix)}" ${
            hasChildren ? '' : 'disabled'
          } tabindex="-1" ${hasChildren ? '' : 'aria-hidden="true"'}>${toggleGlyph}</button>
          <button type="button" class="tree-select" data-action="select" data-tree="${facetName}" data-prefix="${esc(node.prefix)}">${label}</button>
          <span class="tree-count">${node.count}</span>
        </div>
        ${children}
      </li>`;
    })
    .join('')}</ul>`;
}

function renderTree(el, nodes, selectedPrefix, facetName) {
  if (!nodes?.length) {
    el.innerHTML = '<div class="facet-empty">no options</div>';
    return;
  }

  const collapsedSet = collapsedTreeState[facetName];
  syncCollapsedState(nodes, collapsedSet);
  const roots = buildHierarchy(nodes);
  indexTree(
    facetName,
    [...(function flatten(list) {
      const out = [];
      for (const node of list) {
        out.push(node);
        out.push(...flatten(node.children));
      }
      return out;
    })(roots)]
  );
  el.innerHTML = renderTreeBranch(roots, selectedPrefix, facetName, collapsedSet);
}

function scrollSelectedTreeNodeIntoView(treeName, selectedPrefix) {
  if (!selectedPrefix) return;
  const container = treeElementByName(treeName);
  if (!container) return;
  const selected = container.querySelector(`button.tree-select[data-tree="${treeName}"][data-prefix="${CSS.escape(selectedPrefix)}"]`);
  if (!selected) return;
  selected.scrollIntoView({ block: 'nearest', inline: 'nearest' });
}

function renderActiveFilters(state) {
  const chips = [];

  if (state.q) chips.push({ key: 'q', value: `search: ${state.q}` });
  if (state.state !== 'active') chips.push({ key: 'state', value: `state: ${state.state}` });
  if (state.bare !== 'all') chips.push({ key: 'bare', value: `bare: ${state.bare}` });
  if (state.branch) chips.push({ key: 'branch', value: `branch: ${labelForFacetValue(state.branch)}` });
  if (state.author) chips.push({ key: 'author', value: `author: ${labelForFacetValue(state.author)}` });
  if (state.pathPrefix) chips.push({ key: 'pathPrefix', value: `path: ${state.pathPrefix}` });
  if (state.originPrefix) chips.push({ key: 'originPrefix', value: `origin: ${state.originPrefix}` });

  if (chips.length === 0) {
    activeFilters.innerHTML = '<span class="chip-muted">no active filters</span>';
    return;
  }

  activeFilters.innerHTML = chips
    .map((chip) => `<button type="button" class="chip" data-clear="${chip.key}">${esc(chip.value)} ✕</button>`)
    .join('');
}

function renderFacets(facets, state) {
  lastFacets = facets || {};
  renderTree(pathTree, facets?.localPathTree || [], state.pathPrefix, 'path');
  renderTree(originTree, facets?.originTree || [], state.originPrefix, 'origin');
  renderActiveFilters(state);
  requestAnimationFrame(() => {
    scrollSelectedTreeNodeIntoView('origin', state.originPrefix);
    scrollSelectedTreeNodeIntoView('path', state.pathPrefix);
    if (pendingTreeFocus?.treeName && pendingTreeFocus?.prefix) {
      focusTreeNode(pendingTreeFocus.treeName, pendingTreeFocus.prefix);
      pendingTreeFocus = null;
    }
  });
}

function applyTreeClick(event) {
  const toggle = event.target.closest('button[data-action="toggle"][data-tree][data-prefix]');
  if (toggle) {
    const treeName = toggle.getAttribute('data-tree');
    const prefix = toggle.getAttribute('data-prefix') || '';
    const collapsedSet = collapsedTreeState[treeName];
    if (!collapsedSet) return;
    if (collapsedSet.has(prefix)) collapsedSet.delete(prefix);
    else collapsedSet.add(prefix);
    renderFacets(lastFacets, stateFromUrl());
    return;
  }

  const select = event.target.closest('button[data-action="select"][data-tree][data-prefix]');
  if (!select) return;

  const next = stateFromUrl();
  const treeName = select.getAttribute('data-tree');
  const prefix = select.getAttribute('data-prefix') || '';

  if (treeName === 'path') next.pathPrefix = next.pathPrefix === prefix ? '' : prefix;
  if (treeName === 'origin') next.originPrefix = next.originPrefix === prefix ? '' : prefix;
  pendingTreeFocus = { treeName, prefix };

  writeUrlState(next);
  applyStateToControls(next);
  void refreshRows();
}

function treeElementByName(treeName) {
  return treeName === 'path' ? pathTree : originTree;
}

function getVisibleTreeSelectButtons(treeName) {
  const el = treeElementByName(treeName);
  return [...el.querySelectorAll(`button.tree-select[data-tree="${treeName}"]`)];
}

function focusTreeNode(treeName, prefix) {
  const buttons = getVisibleTreeSelectButtons(treeName);
  const target = buttons.find((button) => (button.getAttribute('data-prefix') || '') === prefix);
  if (target) target.focus();
}

function focusTreeDefault(treeName) {
  const state = stateFromUrl();
  const selectedPrefix = treeName === 'path' ? state.pathPrefix : state.originPrefix;
  const buttons = getVisibleTreeSelectButtons(treeName);
  if (!buttons.length) return;
  const selected = selectedPrefix
    ? buttons.find((button) => (button.getAttribute('data-prefix') || '') === selectedPrefix)
    : null;
  (selected || buttons[0]).focus();
}

function focusResultsSection() {
  const headerButton = rowsTable.querySelector('.tabulator-col[tabindex="0"]');
  if (headerButton) {
    headerButton.focus();
    return;
  }
  rowsTable.focus();
}

function focusSectionByOrder(currentSection, direction) {
  const order = ['filters', 'origin', 'path', 'results'];
  const idx = order.indexOf(currentSection);
  const nextIdx = Math.max(0, Math.min(order.length - 1, idx + direction));
  const section = order[nextIdx];

  if (section === 'filters') qInput.focus();
  if (section === 'path') focusTreeDefault('path');
  if (section === 'origin') focusTreeDefault('origin');
  if (section === 'results') focusResultsSection();
}

function applyTreeKeyboard(event) {
  const select = event.target.closest('button.tree-select[data-tree][data-prefix]');
  if (!select) return;

  const treeName = select.getAttribute('data-tree');
  const prefix = select.getAttribute('data-prefix') || '';
  const buttons = getVisibleTreeSelectButtons(treeName);
  const index = buttons.indexOf(select);
  const nodeMeta = treeNodeIndex[treeName].get(prefix);
  const collapsedSet = collapsedTreeState[treeName];

  if (event.key === 'ArrowDown') {
    event.preventDefault();
    if (index >= 0 && index < buttons.length - 1) buttons[index + 1].focus();
    return;
  }

  if (event.key === 'ArrowUp') {
    event.preventDefault();
    if (index > 0) buttons[index - 1].focus();
    return;
  }

  if (event.key === 'ArrowRight') {
    event.preventDefault();
    if (!nodeMeta || !nodeMeta.children.length) return;
    if (collapsedSet.has(prefix)) {
      collapsedSet.delete(prefix);
      renderFacets(lastFacets, stateFromUrl());
      focusTreeNode(treeName, prefix);
      return;
    }
    focusTreeNode(treeName, nodeMeta.children[0]);
    return;
  }

  if (event.key === 'ArrowLeft') {
    event.preventDefault();
    if (nodeMeta && nodeMeta.children.length && !collapsedSet.has(prefix)) {
      collapsedSet.add(prefix);
      renderFacets(lastFacets, stateFromUrl());
      focusTreeNode(treeName, prefix);
      return;
    }
    if (nodeMeta?.parentPrefix) focusTreeNode(treeName, nodeMeta.parentPrefix);
    return;
  }

  if (event.key === 'Enter' || event.key === ' ') {
    event.preventDefault();
    select.click();
    return;
  }

  if (event.key === 'Tab') {
    event.preventDefault();
    focusSectionByOrder(treeName === 'path' ? 'path' : 'origin', event.shiftKey ? -1 : 1);
  }
}

function applyChipClear(event) {
  const button = event.target.closest('button[data-clear]');
  if (!button) return;

  const key = button.getAttribute('data-clear');
  const next = stateFromUrl();

  if (key === 'q') next.q = '';
  if (key === 'state') next.state = 'active';
  if (key === 'bare') next.bare = 'all';
  if (key === 'branch') next.branch = '';
  if (key === 'author') next.author = '';
  if (key === 'pathPrefix') next.pathPrefix = '';
  if (key === 'originPrefix') next.originPrefix = '';

  writeUrlState(next);
  applyStateToControls(next);
  void refreshRows();
}

function applyAuthorClick(event) {
  const button = event.target.closest('button.author-filter-btn[data-author]');
  if (!button) return;
  event.preventDefault();

  const author = button.getAttribute('data-author') || '';
  const next = stateFromUrl();
  next.author = next.author === author ? '' : author;
  writeUrlState(next);
  void refreshRows();
}

async function applyPathOpenClick(event) {
  const button = event.target.closest('button.path-open-btn[data-path]');
  if (!button) return;
  event.preventDefault();

  const pathValue = button.getAttribute('data-path') || '';
  try {
    const res = await fetch('/actions/open-terminal', {
      method: 'POST',
      headers: { 'content-type': 'application/json' },
      body: JSON.stringify({ path: pathValue })
    });
    if (res.ok) return;
  } catch {
    // fallback below
  }

  const fallbackHref = pathToGhosttyHref(pathValue);
  if (fallbackHref) window.location.assign(fallbackHref);
}

async function applyReindexClick(event) {
  const button = event.target.closest('button.reindex-btn[data-path]');
  if (!button) return;
  event.preventDefault();

  const repoPath = button.getAttribute('data-path') || '';
  if (!repoPath) return;

  button.disabled = true;
  const prevStatus = syncStatus.textContent;
  syncStatus.textContent = `reindexing ${repoPath}...`;

  try {
    const res = await fetch('/actions/reindex-repo', {
      method: 'POST',
      headers: { 'content-type': 'application/json' },
      body: JSON.stringify({ path: repoPath })
    });

    if (!res.ok) {
      const payload = await res.json().catch(() => ({}));
      throw new Error(payload?.error || 'reindex failed');
    }

    const payload = await res.json().catch(() => ({}));
    if (payload?.deleted) syncStatus.textContent = `deleted from index ${repoPath}`;
    else syncStatus.textContent = `reindexed ${repoPath}`;
    await refreshRows();
  } catch (error) {
    syncStatus.textContent = `reindex failed: ${error instanceof Error ? error.message : String(error)}`;
  } finally {
    button.disabled = false;
    setTimeout(() => {
      if (syncStatus.textContent?.startsWith('reindex')) {
        syncStatus.textContent = prevStatus;
      }
    }, 1800);
  }
}

async function fetchRows() {
  const state = stateFromUrl();
  const params = new URLSearchParams();
  if (state.q) params.set('q', state.q);
  if (state.state !== 'active') params.set('state', state.state);
  if (state.bare !== 'all') params.set('bare', state.bare);
  if (state.branch) params.set('branch', state.branch);
  if (state.author) params.set('author', state.author);
  if (state.pathPrefix) params.set('path_prefix', state.pathPrefix);
  if (state.originPrefix) params.set('origin_prefix', state.originPrefix);
  params.set('sort', state.sort);
  params.set('dir', state.dir);

  const res = await fetch(`/rows?${params.toString()}`);
  if (!res.ok) throw new Error('rows fetch failed');
  return res.json();
}

async function refreshRows() {
  const data = await fetchRows();
  const state = stateFromUrl();
  let rows = data.rows || [];
  let totalCount = data.totalCount || 0;

  // Client-side fallback so author filtering still works even if API is stale.
  if (state.author) {
    rows = rows.filter((row) => String(row.last_commit_author || '').trim() === state.author);
    totalCount = rows.length;
  }

  lastFacets = data.facets || {};
  renderRows(rows, totalCount, data.databaseTotal || 0, state);
  void refreshRepoDetails(rows);
  renderFacets(lastFacets, state);
}

function renderStatus(state) {
  if (state.running) {
    const progress = ` (${state.processedGitDirs || 0}/${state.discoveredGitDirs || '?'})`;
    const persisted = state.persistedRepos ? ` saved=${state.persistedRepos}` : '';
    syncStatus.textContent = `${state.phase || 'sync'}: ${state.message || 'working'}${progress}${persisted}`;
    syncBtn.disabled = true;
    return;
  }

  if (state.error) {
    syncStatus.textContent = `error: ${state.error}`;
    syncBtn.disabled = false;
    return;
  }

  if (state.lastRunAt) {
    syncStatus.textContent = `last sync ${state.lastRunAt} using ${state.scanner || 'unknown'} indexed ${state.lastIndexed || 0} repos in ${state.durationMs || 0}ms`;
  } else {
    syncStatus.textContent = 'idle';
  }
  syncBtn.disabled = false;
}

async function fetchStatus() {
  const res = await fetch('/sync-status');
  if (!res.ok) throw new Error('status fetch failed');
  return res.json();
}

function startRowsPolling() {
  if (rowsTimer) return;
  rowsTimer = setInterval(() => {
    void refreshRows().catch(() => {});
  }, 1000);
}

function stopRowsPolling() {
  if (!rowsTimer) return;
  clearInterval(rowsTimer);
  rowsTimer = null;
}

function startStatusPolling() {
  if (pollTimer) return;
  pollTimer = setInterval(async () => {
    try {
      const state = await fetchStatus();
      renderStatus(state);
      if (state.running) startRowsPolling();
      else stopRowsPolling();

      if (!state.running && state.lastRunAt && state.lastRunAt !== lastRunAt) {
        lastRunAt = state.lastRunAt;
        await refreshRows();
      }
    } catch {
      // ignore
    }
  }, 1000);
}

async function triggerSync() {
  if (syncBtn.disabled) return;
  syncBtn.disabled = true;
  syncStatus.textContent = 'starting sync...';

  try {
    const res = await fetch('/sync', { method: 'POST' });
    if (!res.ok) throw new Error('sync start failed');
    syncStatus.textContent = 'sync requested...';
    startStatusPolling();
    startRowsPolling();
  } catch {
    syncStatus.textContent = 'failed to start sync';
    syncBtn.disabled = false;
  }
}

function clearAllFilters() {
  const next = stateFromUrl();
  next.q = '';
  next.author = '';
  writeUrlState(next);
  applyStateToControls(next);
  void refreshRows();
}

function bindInteractiveHandlers(container) {
  if (!container) return;
  container.addEventListener('click', applyPathOpenClick);
  container.addEventListener('click', applyAuthorClick);
  container.addEventListener('click', applyReindexClick);
}

async function init() {
  const state = stateFromUrl();
  applyStateToControls(state);
  renderActiveFilters(state);

  filters.addEventListener('submit', (event) => {
    event.preventDefault();
    const next = stateFromUrl();
    next.q = qInput.value.trim();
    writeUrlState(next);
    void refreshRows();
  });

  qInput.addEventListener('keydown', (event) => {
    if (event.key === 'Escape') {
      event.preventDefault();
      clearAllFilters();
    }
  });
  pathTree.addEventListener('click', applyTreeClick);
  originTree.addEventListener('click', applyTreeClick);
  pathTree.addEventListener('keydown', applyTreeKeyboard);
  originTree.addEventListener('keydown', applyTreeKeyboard);
  activeFilters.addEventListener('click', applyChipClear);
  bindInteractiveHandlers(rowsTable);
  bindInteractiveHandlers(repoDetails);
  rowsTable.addEventListener('keydown', (event) => {
    if (event.key === 'Tab' && event.shiftKey) {
      event.preventDefault();
      focusSectionByOrder('results', -1);
    }
  });

  syncBtn.addEventListener('click', () => {
    void triggerSync();
  });

  try {
    await refreshRows();
    const status = await fetchStatus();
    renderStatus(status);
    if (status.running) {
      startRowsPolling();
      startStatusPolling();
    }
    lastRunAt = status.lastRunAt || null;
  } catch {
    syncStatus.textContent = 'failed to load initial data';
  }

  const stream = new EventSource('/events');
  stream.onmessage = (event) => {
    try {
      const stateEvent = JSON.parse(event.data);
      renderStatus(stateEvent);
      if (stateEvent.running) startRowsPolling();
      else stopRowsPolling();
      if (stateEvent.lastRunAt && stateEvent.lastRunAt !== lastRunAt) {
        lastRunAt = stateEvent.lastRunAt;
        void refreshRows();
      }
    } catch {
      // ignore parse errors
    }
  };

  stream.onerror = () => {
    startStatusPolling();
  };
}

void init();
