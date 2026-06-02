import { Command } from 'commander';
import { runHeadless } from './cli';
import { configManager } from './config/manager';
import { startTUI } from './tui';

const program = new Command();

program
  .name('zero')
  .description('A clean terminal AI coding agent')
  .version('0.1.0');

program
  .option('-p, --prompt <prompt>', 'Run in headless mode with the given prompt')
  .action(async (options) => {
    if (options.prompt) {
      await runHeadless(options.prompt);
    } else {
      // Launch the interactive TUI (Grok Build style)
      startTUI();
    }
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

program.parse();
