import type { Tool } from './types';

export class ToolRegistry {
  private tools = new Map<string, Tool>();

  register(tool: Tool) {
    this.tools.set(tool.name, tool);
  }

  get(name: string): Tool | undefined {
    return this.tools.get(name);
  }

  getAll(): Tool[] {
    return Array.from(this.tools.values());
  }

  async run(name: string, args: unknown): Promise<string> {
    const tool = this.get(name);
    if (!tool) {
      return `Error: Unknown tool "${name}".`;
    }

    if (tool.run) {
      return tool.run(args);
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

  getDefinitionsForProvider() {
    return this.getAll().map(tool => ({
      name: tool.name,
      description: tool.description,
      parameters: tool.parameters.shape, // We'll convert properly later
    }));
  }
}

export const toolRegistry = new ToolRegistry();
