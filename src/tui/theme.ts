export const tuiTheme = {
  colors: {
    brand: '#67e8f9',
    accent: '#86efac',
    text: '#f8fafc',
    muted: '#94a3b8',
    subtle: '#475569',
    panel: '#111827',
    panelAlt: '#0f172a',
    userBg: '#10251f',
    userSymbol: '#34d399',
    model: '#bfdbfe',
    warning: '#fde68a',
    danger: '#fca5a5',
    success: '#86efac',
    border: '#334155',
    strongBorder: '#64748b',
  },
  marks: {
    prompt: '>',
    cursor: ' ',
    user: '>',
    assistant: 'zero',
    tool: 'tool',
    note: 'sys',
  },
} as const;

export type TuiColor = (typeof tuiTheme.colors)[keyof typeof tuiTheme.colors];
