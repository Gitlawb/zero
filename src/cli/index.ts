import { loadProviderConfig } from '../config/provider';
import { createZeroProvider, resolveZeroProviderRuntime } from '../zero-provider-runtime';
import type { Provider } from '../providers/types';
import type { ZeroResolvedProviderRuntime } from '../zero-provider-runtime';
import { runAgent } from '../agent/loop';

export async function runHeadless(prompt: string) {
  const providerConfig = await loadProviderConfig();
  let runtime: ZeroResolvedProviderRuntime | undefined;
  let provider: Provider | undefined;

  try {
    runtime = resolveZeroProviderRuntime({
      provider: providerConfig.provider,
      apiKey: providerConfig.apiKey,
      baseURL: providerConfig.baseURL,
      model: providerConfig.model,
      profileName: providerConfig.profileName,
      source: providerConfig.source,
    });
    provider = createZeroProvider(runtime);
  } catch (err: any) {
    console.error(`[zero] ${err?.message ?? String(err)}`);
    if (runtime?.provider === 'anthropic' || runtime?.provider === 'google') {
      console.error(
        `[zero] ${runtime.provider} adapter is not yet implemented. ` +
        'Set provider: "openai-compatible" with a custom gateway or use an OpenAI model.'
      );
    }
    process.exit(1);
  }

  if (!runtime || !provider) return;

  console.log(`
   ███████╗ ███████╗ ██████╗   ██████╗ 
   ╚══███╔╝ ██╔════╝ ██╔══██╗ ██╔═══██╗
     ███╔╝  █████╗   ██████╔╝ ██║   ██║
    ███╔╝   ██╔══╝   ██╔══██╗ ██║   ██║
   ███████╗ ███████╗ ██║  ██║ ╚██████╔╝
   ╚══════╝ ╚══════╝ ╚═╝  ╚═╝  ╚═════╝ 
`);

  console.log(`[zero] Provider: ${runtime.profileName ? `profile: ${runtime.profileName}` : runtime.source}`);
  console.log(`[zero] Runtime: ${runtime.provider}`);
  console.log(`[zero] Model: ${runtime.modelId ?? runtime.requestedModel}`);
  console.log(`[zero] API model: ${runtime.apiModel}`);
  console.log(`[zero] Base URL: ${runtime.baseURL}`);
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
