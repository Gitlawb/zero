export const tuiTheme = {
  colors: {
    brand: 'cyan',
    accent: 'green',
    text: 'white',
    muted: 'gray',
    subtle: 'gray',
    model: 'magenta',
    warning: 'yellow',
    danger: 'red',
    success: 'green',
    border: 'gray',
  },
  marks: {
    prompt: '>',
    cursor: '|',
    user: 'you',
    assistant: 'zero',
    tool: 'tool',
    note: 'note',
  },
} as const;

export type TuiColor = (typeof tuiTheme.colors)[keyof typeof tuiTheme.colors];
