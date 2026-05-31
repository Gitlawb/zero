/** @jsxImportSource @opentui/solid */
import { createCliRenderer } from '@opentui/core';
import { render } from '@opentui/solid';
import { App } from './App';

export function startTUI() {
  void start();
}

async function start() {
  const renderer = await createCliRenderer({
    externalOutputMode: 'passthrough',
    targetFps: 60,
    gatherStats: false,
    exitOnCtrlC: false,
    useMouse: true,
    useKittyKeyboard: {},
    openConsoleOnError: false,
  });

  const exit = () => {
    if (!renderer.isDestroyed) {
      renderer.setTerminalTitle('');
      renderer.destroy();
    }
  };

  process.once('SIGINT', exit);
  renderer.once('destroy', () => {
    process.off('SIGINT', exit);
  });

  await render(() => <App onExit={exit} />, renderer);
}
