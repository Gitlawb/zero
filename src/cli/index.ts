import { loadProviderConfig } from '../config/provider';
import { configManager } from '../config/manager';
import { createProvider, resolveProviderType } from '../providers/factory';
import { runAgent } from '../agent/loop';

export async function runHeadless(prompt: string) {
  const providerConfig = await loadProviderConfig();
  const activeProfile = configManager.getActiveProvider();
  const providerType = resolveProviderType(providerConfig);
  const provider = createProvider(providerConfig);

  const source = activeProfile 
    ? `profile: ${activeProfile.name}`
    : process.env.ZERO_PROVIDER_COMMAND 
      ? 'provider-command'
      : 'environment';

  console.log(`
   ███████╗ ███████╗ ██████╗   ██████╗ 
   ╚══███╔╝ ██╔════╝ ██╔══██╗ ██╔═══██╗
     ███╔╝  █████╗   ██████╔╝ ██║   ██║
    ███╔╝   ██╔══╝   ██╔══██╗ ██║   ██║
   ███████╗ ███████╗ ██║  ██║ ╚██████╔╝
   ╚══════╝ ╚══════╝ ╚═╝  ╚═╝  ╚═════╝ 
`);

  console.log(`[zero] Provider: ${source}`);
  console.log(`[zero] Provider Type: ${providerType}`);
  console.log(`[zero] Model: ${providerConfig.model}`);
  console.log(`[zero] Base URL: ${providerConfig.baseURL}`);
  console.log(`\n> ${prompt}\n`);

  const finalAnswer = await runAgent(prompt, provider, {
    onText: (text) => process.stdout.write(text),
    onToolCall: (tc) => {
      console.log(`\n[tool] ${tc.name}(${tc.arguments})`);
    },
    onToolResult: (result) => {
      console.log(`[result] ${result.result.slice(0, 200)}${result.result.length > 200 ? '...' : ''}`);
    },
  });

  if (finalAnswer) {
    console.log(`\n\n${finalAnswer}`);
  }
}
