import { toolRegistry } from './registry';
import { readFileTool } from './read_file';
import { writeFileTool } from './write_file';
import { editFileTool } from './edit_file';
import { bashTool } from './bash';
import { planTool } from './plan';
import { listDirectoryTool } from './list-directory';
import { grepTool } from './grep';

toolRegistry.register(readFileTool);
toolRegistry.register(writeFileTool);
toolRegistry.register(editFileTool);
toolRegistry.register(bashTool);
toolRegistry.register(planTool);
toolRegistry.register(listDirectoryTool);
toolRegistry.register(grepTool);

// Compatibility re-exports (kebab-case filenames still resolve)
export { readFileTool as readFileToolKebab } from './read_file';
export { editFileTool as editFileToolKebab } from './edit_file';

export { toolRegistry };
export * from './types';
export { getCurrentPlan, clearPlan } from './plan';
export type { PlanItem } from './plan';
