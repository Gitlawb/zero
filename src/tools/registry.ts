import type { Tool } from './types';

export class ToolRegistry {
  private tools = new Map<string, Tool>();

  register(tool: Tool) {
    this.tools.set(tool.name, tool);
  }

  get(name: string): Tool | undefined {
    return this.tools.get(name);
  }

  unregister(name: string): boolean {
    return this.tools.delete(name);
  }

  getAll(): Tool[] {
    return Array.from(this.tools.values());
  }

  async run(name: string, args: unknown, options: { permissionGranted?: boolean } = {}): Promise<string> {
    const tool = this.get(name);
    if (!tool) {
      return `Error: Unknown tool "${name}".`;
    }

    if (tool.safety.permission !== 'allow' && !options.permissionGranted) {
      return `Error: Permission required for ${name}: ${tool.safety.reason} ` +
        `The tool is marked "${tool.safety.permission}" and was not executed.`;
    }

    const parsed = tool.parameters.safeParse(args);
    if (!parsed.success) {
      return `Error: Invalid arguments for ${name}: ${parsed.error.message}`;
    }

    try {
      return await tool.execute(parsed.data);
    } catch (err: any) {
      return `Error executing ${name}: ${err?.message ?? String(err)}`;
    }
  }
}

export const toolRegistry = new ToolRegistry();
