export type TuiCommandGroup = 'session' | 'model' | 'runtime' | 'tools' | 'meta';

export interface TuiCommandDefinition {
  name: string;
  aliases?: readonly string[];
  usage: string;
  group: TuiCommandGroup;
  description: string;
}

export const TUI_COMMANDS: readonly TuiCommandDefinition[] = [
  {
    name: '/provider',
    usage: '/provider',
    group: 'runtime',
    description: 'Manage saved providers and switch the active provider.',
  },
  {
    name: '/model',
    usage: '/model [list|profiles|profile|model]',
    group: 'model',
    description: 'Browse models, list profiles, or set the model for this session.',
  },
  {
    name: '/plan',
    usage: '/plan',
    group: 'runtime',
    description: 'Toggle planning behavior before making changes.',
  },
  {
    name: '/permissions',
    usage: '/permissions',
    group: 'tools',
    description: 'Show the current tool permission mode and session grants.',
  },
  {
    name: '/tools',
    usage: '/tools [on|off]',
    group: 'tools',
    description: 'Enable or disable tool calling for this session.',
  },
  {
    name: '/context',
    usage: '/context',
    group: 'session',
    description: 'Show current model, provider, token estimate, cost, and active file.',
  },
  {
    name: '/clear',
    usage: '/clear',
    group: 'session',
    description: 'Clear the visible transcript and return to the startup splash.',
  },
  {
    name: '/search',
    usage: '/search <query>',
    group: 'session',
    description: 'Search local Zero session events from the TUI.',
  },
  {
    name: '/doctor',
    usage: '/doctor [--connectivity]',
    group: 'meta',
    description: 'Run Zero health checks with secret redaction.',
  },
  {
    name: '/config',
    usage: '/config',
    group: 'meta',
    description: 'Inspect layered Zero configuration with redacted provider details.',
  },
  {
    name: '/debug',
    aliases: ['/debug-mode'],
    usage: '/debug [true|false]',
    group: 'meta',
    description: 'Toggle provider payload debugging and error details.',
  },
  {
    name: '/help',
    usage: '/help',
    group: 'meta',
    description: 'Show this command registry.',
  },
  {
    name: '/exit',
    aliases: ['/quit'],
    usage: '/exit',
    group: 'session',
    description: 'Quit Zero.',
  },
];

export function listTuiCommands(): readonly TuiCommandDefinition[] {
  return TUI_COMMANDS;
}

export function listTuiCommandNames(): string[] {
  return TUI_COMMANDS.flatMap((command) => [command.name, ...(command.aliases ?? [])]);
}

export function resolveTuiCommand(name: string): TuiCommandDefinition | undefined {
  const normalized = name.trim().toLowerCase();
  return TUI_COMMANDS.find((command) =>
    command.name === normalized || command.aliases?.includes(normalized)
  );
}

export function formatTuiHelpLines(): string[] {
  return TUI_COMMANDS.map((command) =>
    `${command.usage.padEnd(28)} ${command.description}`
  );
}
