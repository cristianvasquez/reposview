import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const SCRIPT_DIR = path.dirname(fileURLToPath(import.meta.url));
export const PROJECT_ROOT = path.resolve(SCRIPT_DIR, '..');
export const CONFIG_PATH = path.join(PROJECT_ROOT, 'config.yaml');

function escapeRegex(value) {
  return String(value).replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
}

function defaultConfig(env = process.env) {
  const home = env.HOME || '/';
  const escapedHome = escapeRegex(home);

  return {
    paths: {
      database: './data/reposview.sqlite'
    },
    scan: {
      roots: [home],
      exclude_regex: `^${escapedHome}/\\.[^/]+(?:/|$)`,
      scanner: 'auto'
    },
    api: {
      host: '127.0.0.1',
      port: 8787
    },
    web: {
      host: '127.0.0.1',
      port: 8790,
      api_origin: null
    },
    tui: {
      api_origin: '',
      spawn_api: false,
      scanner: 'auto',
      database: './data/reposview.sqlite'
    },
    operations: {
      open_terminal: {
        launchers: [
          {
            command: 'ghostty',
            args: ['--working-directory={dir}', '--gtk-single-instance=false'],
            unset_env: ['DBUS_SESSION_BUS_ADDRESS']
          },
          {
            command: 'gnome-terminal',
            args: ['--working-directory={dir}']
          }
        ]
      },
      open_repo: {
        requires: ['yazi'],
        launchers: [
          {
            command: 'ghostty',
            args: ['--working-directory={dir}', '--gtk-single-instance=false', '-e', 'yazi', '{dir}'],
            unset_env: ['DBUS_SESSION_BUS_ADDRESS']
          },
          {
            command: 'gnome-terminal',
            args: ['--working-directory={dir}', '--', 'yazi', '{dir}']
          }
        ]
      },
      open_web: {
        commands: ['xdg-open', 'open']
      }
    }
  };
}

function stripInlineComment(line) {
  let quote = null;
  for (let i = 0; i < line.length; i += 1) {
    const ch = line[i];
    if ((ch === '"' || ch === "'") && line[i - 1] !== '\\') {
      quote = quote === ch ? null : quote || ch;
      continue;
    }
    if (ch === '#' && !quote) {
      return line.slice(0, i).trimEnd();
    }
  }
  return line.trimEnd();
}

function tokenizeYAML(source) {
  return source
    .split(/\r?\n/)
    .map((raw, index) => ({ raw, line: index + 1 }))
    .map(({ raw, line }) => {
      const stripped = stripInlineComment(raw);
      const trimmed = stripped.trim();
      if (!trimmed) return null;
      const indent = stripped.match(/^ */)?.[0].length ?? 0;
      return { indent, text: trimmed, line };
    })
    .filter(Boolean);
}

function splitKeyValue(text) {
  let quote = null;
  for (let i = 0; i < text.length; i += 1) {
    const ch = text[i];
    if ((ch === '"' || ch === "'") && text[i - 1] !== '\\') {
      quote = quote === ch ? null : quote || ch;
      continue;
    }
    if (ch === ':' && !quote) {
      return [text.slice(0, i).trim(), text.slice(i + 1).trim()];
    }
  }
  return null;
}

function parseScalar(text) {
  if (text === 'null') return null;
  if (text === 'true') return true;
  if (text === 'false') return false;
  if (/^-?\d+$/.test(text)) return Number(text);
  if ((text.startsWith('"') && text.endsWith('"')) || (text.startsWith("'") && text.endsWith("'"))) {
    return text.slice(1, -1);
  }
  return text;
}

function parseBlock(lines, index, indent) {
  if (index >= lines.length) return [null, index];
  const line = lines[index];
  if (line.indent !== indent) {
    throw new Error(`Invalid indentation in ${CONFIG_PATH}:${line.line}`);
  }
  if (line.text.startsWith('- ')) {
    return parseSequence(lines, index, indent);
  }
  return parseMapping(lines, index, indent);
}

function parseMapping(lines, index, indent) {
  const out = {};

  while (index < lines.length) {
    const line = lines[index];
    if (line.indent < indent) break;
    if (line.indent > indent) {
      throw new Error(`Unexpected indentation in ${CONFIG_PATH}:${line.line}`);
    }
    if (line.text.startsWith('- ')) break;

    const pair = splitKeyValue(line.text);
    if (!pair || !pair[0]) {
      throw new Error(`Invalid mapping entry in ${CONFIG_PATH}:${line.line}`);
    }

    const [key, rest] = pair;
    index += 1;

    if (rest) {
      out[key] = parseScalar(rest);
      continue;
    }

    const next = lines[index];
    if (next && next.indent > indent) {
      let value;
      [value, index] = parseBlock(lines, index, next.indent);
      out[key] = value;
      continue;
    }

    out[key] = null;
  }

  return [out, index];
}

function parseSequence(lines, index, indent) {
  const out = [];

  while (index < lines.length) {
    const line = lines[index];
    if (line.indent < indent) break;
    if (line.indent > indent) {
      throw new Error(`Unexpected indentation in ${CONFIG_PATH}:${line.line}`);
    }
    if (!line.text.startsWith('- ')) break;

    const itemText = line.text.slice(2).trim();
    index += 1;

    if (!itemText) {
      const next = lines[index];
      if (next && next.indent > indent) {
        let value;
        [value, index] = parseBlock(lines, index, next.indent);
        out.push(value);
      } else {
        out.push(null);
      }
      continue;
    }

    const pair = splitKeyValue(itemText);
    if (!pair || !pair[0]) {
      out.push(parseScalar(itemText));
      continue;
    }

    const [key, rest] = pair;
    const item = {};

    if (rest) {
      item[key] = parseScalar(rest);
    } else {
      const next = lines[index];
      if (next && next.indent > indent) {
        let value;
        [value, index] = parseBlock(lines, index, next.indent);
        item[key] = value;
      } else {
        item[key] = null;
      }
    }

    const next = lines[index];
    if (next && next.indent > indent) {
      let extra;
      [extra, index] = parseBlock(lines, index, next.indent);
      if (!extra || Array.isArray(extra) || typeof extra !== 'object') {
        throw new Error(`Invalid sequence item in ${CONFIG_PATH}:${line.line}`);
      }
      Object.assign(item, extra);
    }

    out.push(item);
  }

  return [out, index];
}

function parseYAML(source) {
  const lines = tokenizeYAML(source);
  if (lines.length === 0) return {};
  const [value, index] = parseBlock(lines, 0, lines[0].indent);
  if (index !== lines.length) {
    const line = lines[index];
    throw new Error(`Unexpected trailing content in ${CONFIG_PATH}:${line.line}`);
  }
  if (!value || Array.isArray(value) || typeof value !== 'object') {
    throw new Error(`Root document in ${CONFIG_PATH} must be a mapping`);
  }
  return value;
}

function isPlainObject(value) {
  return Boolean(value) && !Array.isArray(value) && typeof value === 'object';
}

function deepMerge(base, override) {
  if (Array.isArray(base) && Array.isArray(override)) {
    return override.map((item) => deepMerge(undefined, item));
  }
  if (isPlainObject(base) && isPlainObject(override)) {
    const out = { ...base };
    for (const [key, value] of Object.entries(override)) {
      out[key] = key in base ? deepMerge(base[key], value) : deepMerge(undefined, value);
    }
    return out;
  }
  if (Array.isArray(override)) {
    return override.map((item) => deepMerge(undefined, item));
  }
  if (isPlainObject(override)) {
    const out = {};
    for (const [key, value] of Object.entries(override)) {
      out[key] = deepMerge(undefined, value);
    }
    return out;
  }
  return override === undefined ? base : override;
}

function interpolateString(value, vars) {
  return value.replace(/\$\{([A-Z0-9_]+)\}/g, (full, name) => (name in vars ? vars[name] : full));
}

function interpolateConfig(value, vars) {
  if (Array.isArray(value)) {
    return value.map((item) => interpolateConfig(item, vars));
  }
  if (isPlainObject(value)) {
    return Object.fromEntries(
      Object.entries(value).map(([key, entry]) => [key, interpolateConfig(entry, vars)])
    );
  }
  if (typeof value === 'string') {
    return interpolateString(value, vars);
  }
  return value;
}

function normalizePort(value, fallback) {
  const parsed = Number(value);
  return Number.isFinite(parsed) && parsed > 0 ? parsed : fallback;
}

export function loadConfigSync(options = {}) {
  const env = options.env || process.env;
  const configPath = options.configPath || CONFIG_PATH;
  const base = defaultConfig(env);

  let loaded = {};
  if (fs.existsSync(configPath)) {
    const source = fs.readFileSync(configPath, 'utf8');
    loaded = parseYAML(source);
  }

  const vars = {
    ...Object.fromEntries(Object.entries(env).map(([key, value]) => [key, String(value)])),
    HOME: String(env.HOME || '/'),
    HOME_ESCAPED: escapeRegex(env.HOME || '/')
  };

  const merged = interpolateConfig(deepMerge(base, loaded), vars);

  merged.api.port = normalizePort(merged.api?.port, base.api.port);
  merged.web.port = normalizePort(merged.web?.port, base.web.port);

  return merged;
}

export function getSyncConfig(config = loadConfigSync()) {
  return {
    db: config.paths?.database || './data/reposview.sqlite',
    roots: Array.isArray(config.scan?.roots) ? config.scan.roots : [process.env.HOME || '/'],
    excludeRegex: config.scan?.exclude_regex || '^$',
    dryRun: false,
    verbose: false,
    scanner: config.scan?.scanner || 'auto'
  };
}

export function getInspectConfig(config = loadConfigSync()) {
  const sync = getSyncConfig(config);
  return {
    db: sync.db,
    roots: sync.roots,
    excludeRegex: sync.excludeRegex,
    scanner: sync.scanner,
    host: config.api?.host || '127.0.0.1',
    port: normalizePort(config.api?.port, 8787)
  };
}

export function getDevConfig(config = loadConfigSync()) {
  const sync = getSyncConfig(config);
  return {
    db: sync.db,
    roots: sync.roots,
    excludeRegex: sync.excludeRegex,
    scanner: sync.scanner,
    apiHost: config.api?.host || '127.0.0.1',
    apiPort: normalizePort(config.api?.port, 8787),
    webHost: config.web?.host || '127.0.0.1',
    webPort: normalizePort(config.web?.port, 8790)
  };
}

export function getLauncherConfig(config = loadConfigSync()) {
  return {
    terminal: config.operations?.open_terminal || { launchers: [] },
    yazi: config.operations?.open_repo || { launchers: [] },
    browser: config.operations?.open_web || { commands: [] }
  };
}

export function getWebAPIOrigin(config = loadConfigSync()) {
  if (config.web?.api_origin) {
    return String(config.web.api_origin);
  }
  return `http://${config.api?.host || '127.0.0.1'}:${normalizePort(config.api?.port, 8787)}`;
}
