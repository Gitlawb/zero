/**
 * Small color theme for the startup splash.
 *
 * Kept self-contained so the splash components stay reusable. Values are
 * Ink/chalk color names for broad terminal support (readable even on
 * 16-color terminals).
 */
export const theme = {
  accent: 'cyan', // ZERO wordmark, borders, prompt sigil, chips
  accentBright: 'cyanBright',
  ok: 'green', // status: READY
  okBright: 'greenBright',
  label: 'gray', // dim labels: "cwd:", "project:", "status:"
  value: 'white', // values + typed input
  muted: 'gray', // subtitle, placeholder, shortcut labels
  border: 'cyan',
} as const;

export type Theme = typeof theme;
