import { readdir, readFile } from 'fs/promises';
import { homedir } from 'os';
import { isAbsolute, join, resolve } from 'path';
import { ZodError } from 'zod';
import { parseZeroPluginManifest } from './manifest';
import type {
  ZeroLoadedPlugin,
  ZeroPluginDiagnostic,
  ZeroPluginLoadResult,
  ZeroPluginRoot,
} from './types';

export interface ResolveZeroPluginRootOptions {
  cwd?: string;
  env?: NodeJS.ProcessEnv;
}

export interface LoadZeroPluginsOptions extends ResolveZeroPluginRootOptions {
  roots?: ZeroPluginRoot[];
}

export function resolveZeroPluginRoots(options: ResolveZeroPluginRootOptions = {}): ZeroPluginRoot[] {
  const env = options.env ?? process.env;
  const cwd = resolve(options.cwd ?? process.cwd());
  const configHome = normalizeRoot(env.XDG_CONFIG_HOME, cwd) ?? join(getHomeDir(env), '.config');

  return [
    { source: 'user', path: join(configHome, 'zero', 'plugins') },
    { source: 'project', path: join(cwd, '.zero', 'plugins') },
  ];
}

export async function loadZeroPlugins(options: LoadZeroPluginsOptions = {}): Promise<ZeroPluginLoadResult> {
  const roots = options.roots ?? resolveZeroPluginRoots(options);
  const diagnostics: ZeroPluginDiagnostic[] = [];
  const discovered: ZeroLoadedPlugin[] = [];

  for (const root of roots) {
    const rootPath = resolve(root.path);
    let entries;
    try {
      entries = await readdir(rootPath, { withFileTypes: true });
    } catch (err: any) {
      if (err?.code === 'ENOENT') continue;
      diagnostics.push({
        kind: 'io',
        source: root.source,
        root: rootPath,
        message: err instanceof Error ? err.message : String(err),
      });
      continue;
    }

    for (const entry of entries) {
      if (!entry.isDirectory()) continue;

      const pluginDir = join(rootPath, entry.name);
      const manifestPath = join(pluginDir, 'plugin.json');
      try {
        const text = await readFile(manifestPath, 'utf-8');
        let parsed: unknown;
        try {
          parsed = JSON.parse(text);
        } catch (err: unknown) {
          diagnostics.push({
            kind: 'json',
            source: root.source,
            root: rootPath,
            pluginPath: pluginDir,
            manifestPath,
            message: err instanceof Error ? err.message : String(err),
          });
          continue;
        }

        discovered.push(parseZeroPluginManifest(parsed, {
          source: root.source,
          root: rootPath,
          pluginDir,
          manifestPath,
        }));
      } catch (err: any) {
        if (err?.code === 'ENOENT') continue;
        diagnostics.push(toPluginDiagnostic(err, root, rootPath, pluginDir, manifestPath));
      }
    }
  }

  const plugins = mergePluginsById(discovered, diagnostics);
  return { roots, plugins, diagnostics };
}

function mergePluginsById(
  discovered: ZeroLoadedPlugin[],
  diagnostics: ZeroPluginDiagnostic[]
): ZeroLoadedPlugin[] {
  const byId = new Map<string, ZeroLoadedPlugin>();

  for (const plugin of discovered) {
    const previous = byId.get(plugin.id);
    if (previous) {
      diagnostics.push({
        kind: 'duplicate',
        pluginId: plugin.id,
        source: plugin.source,
        root: plugin.root,
        pluginPath: plugin.pluginDir,
        manifestPath: plugin.manifestPath,
        message: `Plugin "${plugin.id}" from ${plugin.source} overrides ${previous.source} plugin at ${previous.pluginDir}.`,
      });
    }
    byId.set(plugin.id, plugin);
  }

  return Array.from(byId.values()).sort((left, right) => left.id.localeCompare(right.id));
}

function toPluginDiagnostic(
  err: unknown,
  root: ZeroPluginRoot,
  rootPath: string,
  pluginDir: string,
  manifestPath: string
): ZeroPluginDiagnostic {
  if (err instanceof ZodError) {
    const issue = err.issues[0];
    return {
      kind: 'schema',
      source: root.source,
      root: rootPath,
      pluginPath: pluginDir,
      manifestPath,
      fieldPath: issue?.path.map(String).join('.'),
      message: issue?.message ?? err.message,
    };
  }

  return {
    kind: 'schema',
    source: root.source,
    root: rootPath,
    pluginPath: pluginDir,
    manifestPath,
    message: err instanceof Error ? err.message : String(err),
  };
}

function normalizeRoot(value: string | undefined, cwd: string): string | undefined {
  const trimmed = value?.trim();
  if (!trimmed) return undefined;
  return isAbsolute(trimmed) ? trimmed : resolve(cwd, trimmed);
}

function getHomeDir(env: NodeJS.ProcessEnv): string {
  return env.HOME || env.USERPROFILE || homedir();
}
