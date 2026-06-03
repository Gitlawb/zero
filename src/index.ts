import { Command } from 'commander';
import { runExec } from './cli';
import { configManager } from './config/manager';
import { DEFAULT_UPDATE_CHECK_TIMEOUT_MS, checkForUpdate, formatUpdateCheck } from './update/check';
import { ZERO_VERSION } from './version';
import { parseToolList } from './zero-runtime';
import {
  formatZeroConfigInspection,
  inspectZeroConfig,
  type ZeroConfigInspectionReport,
} from './zero-config-inspection';
import { formatZeroDoctorReport, runZeroDoctor, type ZeroDoctorReport } from './zero-doctor';
import { redactZeroErrorMessage, redactZeroSecrets } from './zero-redaction';
import { formatZeroSearchResult, searchZeroSessions } from './zero-search';

const program = new Command();

function getErrorMessage(err: unknown): string {
  return err instanceof Error ? err.message : String(err);
}

function parseNonNegativeIntegerOption(name: string, value: string | undefined): number | undefined {
  if (value === undefined) return undefined;

  const normalized = value.trim();
  if (!/^\d+$/.test(normalized)) {
    throw new Error(`Invalid ${name} value "${value}". Expected a non-negative integer.`);
  }

  return Number(normalized);
}

program
  .name('zero')
  .description('A clean terminal AI coding agent')
  .version(ZERO_VERSION);

program
  .option('-p, --prompt <prompt>', 'Run in headless mode with the given prompt')
  .action(async (options) => {
    if (options.prompt) {
      process.exitCode = await runExec({ prompt: options.prompt, outputFormat: 'text' });
    } else {
      const { startTUI } = await import('./tui');
      startTUI();
    }
  });

program
  .command('exec')
  .description('Run Zero headlessly for scripts, automation, or CI')
  .argument('[prompt...]', 'Prompt to send to the coding agent')
  .option('-f, --file <path>', 'Read the prompt from a file')
  .option('-m, --model <model>', 'Override the configured model for this run')
  .option('--profile <profile>', 'Use a model profile: fast, balanced, deep, or cheap')
  .option('-r, --reasoning-effort <effort>', 'Reasoning effort for models that support it')
  .option('--auto <level>', 'Autonomy level: low, medium, or high', 'low')
  .option('--enabled-tools <tools>', 'Only expose these tools, separated by commas or spaces')
  .option('--disabled-tools <tools>', 'Hide these tools, separated by commas or spaces')
  .option('--list-tools', 'List tools visible under the selected model/autonomy and exit')
  .option('--max-turns <number>', 'Maximum agent loop turns for this run')
  .option('-C, --cwd <path>', 'Run from a different working directory')
  .option('-i, --input-format <format>', 'Input format: text or stream-json', 'text')
  .option('-o, --output-format <format>', 'Output format: text, json, or stream-json', 'text')
  .option('--skip-permissions-unsafe', 'Allow prompt-gated tools for this run')
  .action(async (promptParts: string[] | undefined, options) => {
    let maxTurns: number | undefined;
    try {
      maxTurns = parseNonNegativeIntegerOption('--max-turns', options.maxTurns);
    } catch (err: unknown) {
      console.error(`[zero] ${getErrorMessage(err)}`);
      process.exitCode = 2;
      return;
    }

    process.exitCode = await runExec({
      prompt: (promptParts ?? []).join(' '),
      file: options.file,
      inputFormat: options.inputFormat,
      model: options.model,
      modelProfile: options.profile,
      reasoningEffort: options.reasoningEffort,
      autonomy: options.auto,
      enabledTools: parseToolList(options.enabledTools),
      disabledTools: parseToolList(options.disabledTools),
      listTools: Boolean(options.listTools),
      maxTurns,
      cwd: options.cwd,
      outputFormat: options.outputFormat,
      skipPermissionsUnsafe: Boolean(options.skipPermissionsUnsafe),
    });
  });

// Providers subcommand (temporary until we have a nice /provider in the TUI)
const providersCmd = program.command('providers');

providersCmd
  .command('list')
  .description('List all saved providers')
  .action(() => {
    const providers = configManager.listProviders();
    const active = configManager.getActiveProvider()?.name;

    if (providers.length === 0) {
      console.log('No providers configured yet.');
      console.log('Use the /provider command once the TUI is ready, or edit ~/.config/zero/config.json');
      return;
    }

    console.log('\nSaved Providers:\n');
    providers.forEach(p => {
      const isActive = p.name === active ? ' (active)' : '';
      console.log(`  ${p.name}${isActive}`);
      console.log(`    Model:   ${p.model}`);
      if (p.provider) console.log(`    Provider: ${p.provider}`);
      console.log(`    BaseURL: ${p.baseURL}`);
      if (p.description) console.log(`    Desc:    ${p.description}`);
      console.log('');
    });
  });

providersCmd
  .command('switch <name>')
  .description('Switch the active provider')
  .action((name: string) => {
    const success = configManager.setActiveProvider(name);
    if (success) {
      console.log(`Switched to provider: ${name}`);
    } else {
      console.error(`Provider "${name}" not found.`);
    }
  });

providersCmd
  .command('current')
  .description('Show the currently active provider')
  .action(() => {
    const active = configManager.getActiveProvider();
    if (active) {
      console.log(`Active provider: ${active.name}`);
      if (active.provider) console.log(`Provider: ${active.provider}`);
      console.log(`Model: ${active.model}`);
      console.log(`Base URL: ${active.baseURL}`);
    } else {
      console.log('No active provider set.');
    }
  });

program
  .command('search')
  .description('Search local Zero session events')
  .argument('<query...>', 'Search query')
  .option('--json', 'Print search results as JSON')
  .option('--limit <number>', 'Maximum number of matches to return', '20')
  .option('--context <chars>', 'Context characters around each match', '80')
  .option('--session <id>', 'Search one session id')
  .option('--type <eventType>', 'Search one event type')
  .action(async (queryParts: string[] | undefined, options) => {
    try {
      const query = (queryParts ?? []).join(' ');
      const result = await searchZeroSessions(query, {
        limit: parseNonNegativeIntegerOption('--limit', options.limit),
        contextChars: parseNonNegativeIntegerOption('--context', options.context),
        sessionId: options.session,
        type: options.type,
      });

      if (options.json) {
        console.log(JSON.stringify(redactZeroSecrets(result), null, 2));
      } else {
        console.log(formatZeroSearchResult(result));
      }
    } catch (err: unknown) {
      console.error(`[zero] ${getErrorMessage(err)}`);
      process.exitCode = 2;
    }
  });

program
  .command('update')
  .description('Check for Zero CLI updates')
  .option('--check', 'Check the latest GitHub release without installing')
  .option('--json', 'Print the update check result as JSON')
  .action(async (options: { check?: boolean; json?: boolean }) => {
    if (!options.check) {
      console.error('Only `zero update --check` is available right now.');
      process.exitCode = 1;
      return;
    }

    try {
      const result = await checkForUpdate({ timeoutMs: DEFAULT_UPDATE_CHECK_TIMEOUT_MS });

      if (options.json) {
        console.log(JSON.stringify(result, null, 2));
      } else {
        console.log(formatUpdateCheck(result));
      }
    } catch (err: unknown) {
      console.error(`[zero] Could not check for updates: ${getErrorMessage(err)}`);
      process.exitCode = 1;
    }
  });

program
  .command('doctor')
  .description('Run Zero health checks')
  .option('--json', 'Print health checks as JSON')
  .option('--connectivity', 'Probe the configured provider endpoint')
  .action(async (options: { json?: boolean; connectivity?: boolean }) => {
    try {
      const report = await runZeroDoctor({
        connectivity: Boolean(options.connectivity),
      });
      const safeReport = redactZeroSecrets(report) as ZeroDoctorReport;

      if (options.json) {
        console.log(JSON.stringify(safeReport, null, 2));
      } else {
        console.log(formatZeroDoctorReport(safeReport));
      }

      if (!report.ok) {
        process.exitCode = 1;
      }
    } catch (err: unknown) {
      console.error(`[zero] Doctor failed: ${redactZeroErrorMessage(err)}`);
      process.exitCode = 1;
    }
  });

program
  .command('config')
  .description('Inspect Zero configuration')
  .option('--json', 'Print config inspection as JSON')
  .action((options: { json?: boolean }) => {
    try {
      const report = inspectZeroConfig();
      const safeReport = redactZeroSecrets(report) as ZeroConfigInspectionReport;

      if (options.json) {
        console.log(JSON.stringify(safeReport, null, 2));
      } else {
        console.log(formatZeroConfigInspection(safeReport));
      }

      if (!report.ok) {
        process.exitCode = 1;
      }
    } catch (err: unknown) {
      console.error(`[zero] Config inspection failed: ${redactZeroErrorMessage(err)}`);
      process.exitCode = 1;
    }
  });

await program.parseAsync();
