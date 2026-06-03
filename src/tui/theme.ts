export interface SemanticColors {
  text: {
    primary: string;
    secondary: string;
    link: string;
    accent: string;
    response: string;
  };
  background: {
    primary: string;
    message: string;
    input: string;
    focus: string;
    diff: {
      added: string;
      removed: string;
    };
  };
  border: {
    default: string;
  };
  ui: {
    comment: string;
    symbol: string;
    active: string;
    dark: string;
    focus: string;
    gradient: string[];
  };
  status: {
    error: string;
    success: string;
    warning: string;
  };
  agent: {
    red: string;
    blue: string;
    green: string;
    yellow: string;
    purple: string;
    orange: string;
    pink: string;
    cyan: string;
    badgeText: string;
  };
}

export interface ThemeDefinition {
  name: string;
  type: 'light' | 'dark';
  colors: SemanticColors;
}

const NAMED_COLORS: Record<string, string> = {
  black: '#000000',
  white: '#ffffff',
};

export function normalizeHexColor(color: string): string {
  const raw = color.trim().toLowerCase();
  const named = NAMED_COLORS[raw] ?? raw;
  const expanded = /^#[0-9a-f]{3}$/.test(named)
    ? `#${named[1]}${named[1]}${named[2]}${named[2]}${named[3]}${named[3]}`
    : named;

  if (!/^#[0-9a-f]{6}$/.test(expanded)) {
    throw new Error(`Invalid theme color: ${color}`);
  }

  return expanded;
}

function mix(a: string, b: string, t: number): string {
  const ah = parseInt(normalizeHexColor(a).slice(1), 16);
  const bh = parseInt(normalizeHexColor(b).slice(1), 16);
  const ar = (ah >> 16) & 0xff, ag = (ah >> 8) & 0xff, ab = ah & 0xff;
  const br = (bh >> 16) & 0xff, bg = (bh >> 8) & 0xff, bb = bh & 0xff;
  const rr = Math.round(ar + (br - ar) * t);
  const rg = Math.round(ag + (bg - ag) * t);
  const rb = Math.round(ab + (bb - ab) * t);
  return `#${((rr << 16) | (rg << 8) | rb).toString(16).padStart(6, '0')}`;
}

interface RawColors {
  type: 'light' | 'dark';
  Background: string;
  Foreground: string;
  AccentBlue: string;
  AccentPurple: string;
  AccentCyan: string;
  AccentGreen: string;
  AccentYellow: string;
  AccentRed: string;
  DiffAdded: string;
  DiffRemoved: string;
  Comment: string;
  Gray: string;
  DarkGray: string;
  GradientColors: string[];
  LightBlue?: string;
  MessageBackground?: string;
  InputBackground?: string;
  FocusBackground?: string;
  FocusColor?: string;
}

function rawToSemantic(raw: RawColors, overrides?: Partial<SemanticColors>): SemanticColors {
  const c = normalizeHexColor;
  const base: SemanticColors = {
    text: {
      primary: c(raw.Foreground),
      secondary: c(raw.Gray),
      link: c(raw.AccentBlue),
      accent: c(raw.AccentPurple),
      response: c(raw.Foreground),
    },
    background: {
      primary: c(raw.Background),
      message: raw.MessageBackground ? c(raw.MessageBackground) : mix(raw.Background, raw.Gray, 0.24),
      input: raw.InputBackground ? c(raw.InputBackground) : mix(raw.Background, raw.Gray, 0.24),
      focus: raw.FocusBackground ? c(raw.FocusBackground) : mix(raw.Background, raw.FocusColor ?? raw.AccentGreen, 0.2),
      diff: { added: c(raw.DiffAdded), removed: c(raw.DiffRemoved) },
    },
    border: { default: c(raw.DarkGray) },
    ui: {
      comment: c(raw.Comment),
      symbol: c(raw.AccentCyan),
      active: c(raw.AccentBlue),
      dark: c(raw.DarkGray),
      focus: c(raw.FocusColor ?? raw.AccentGreen),
      gradient: raw.GradientColors.map(c),
    },
    status: {
      error: c(raw.AccentRed),
      success: c(raw.AccentGreen),
      warning: c(raw.AccentYellow),
    },
    agent: {
      red: '#EA4335',
      blue: '#669DF6',
      green: '#34A853',
      yellow: '#FBBC04',
      purple: '#AF5CF7',
      orange: '#E8710A',
      pink: '#FF63B8',
      cyan: '#24C1E0',
      badgeText: '#000000',
    },
  };
  if (raw.type === 'light') {
    base.agent = {
      red: '#D93025',
      blue: '#1A73E8',
      green: '#188038',
      yellow: '#E37400',
      purple: '#8430CE',
      orange: '#C5621C',
      pink: '#D01884',
      cyan: '#007B83',
      badgeText: '#FFFFFF',
    };
  }
  if (overrides) {
    return { ...base, ...overrides };
  }
  return base;
}

const darkTheme: ThemeDefinition = {
  name: 'Default',
  type: 'dark',
  colors: {
    text: {
      primary: '#EEEEEB',
      secondary: '#95958F',
      link: '#6AD86C',
      accent: '#957CAD',
      response: '#D4D4CF',
    },
    background: {
      primary: '#000000',
      message: '#31312D',
      input: '#3B3A35',
      focus: '#16391C',
      diff: { added: '#153123', removed: '#270D09' },
    },
    border: { default: '#484743' },
    ui: {
      comment: '#7B7B75',
      symbol: '#55B5A6',
      active: '#2ADB5C',
      dark: '#2A2926',
      focus: '#2ADB5C',
      gradient: ['#4796E4', '#847ACE', '#C3677F'],
    },
    status: {
      error: '#EC5B56',
      success: '#73B78E',
      warning: '#EFB041',
    },
    agent: {
      red: '#EA4335',
      blue: '#669DF6',
      green: '#34A853',
      yellow: '#FBBC04',
      purple: '#AF5CF7',
      orange: '#E8710A',
      pink: '#FF63B8',
      cyan: '#24C1E0',
      badgeText: '#000000',
    },
  },
};

const lightTheme: ThemeDefinition = {
  name: 'Default Light',
  type: 'light',
  colors: {
    text: {
      primary: '#1A1917',
      secondary: '#6B6A65',
      link: '#278035',
      accent: '#7D6793',
      response: '#2E2D2A',
    },
    background: {
      primary: '#FFFFFF',
      message: '#F5F5F2',
      input: '#EEEDEA',
      focus: '#D8F3D7',
      diff: { added: '#DCFCE7', removed: '#FEE2E2' },
    },
    border: { default: '#B8B7B3' },
    ui: {
      comment: '#878580',
      symbol: '#0A8070',
      active: '#14802E',
      dark: '#DAD9D6',
      focus: '#14802E',
      gradient: ['#4796E4', '#847ACE', '#C3677F'],
    },
    status: {
      error: '#D93025',
      success: '#3B7D5E',
      warning: '#9F6500',
    },
    agent: {
      red: '#D93025',
      blue: '#1A73E8',
      green: '#188038',
      yellow: '#E37400',
      purple: '#8430CE',
      orange: '#C5621C',
      pink: '#D01884',
      cyan: '#007B83',
      badgeText: '#FFFFFF',
    },
  },
};

const ayuTheme: ThemeDefinition = {
  name: 'Ayu',
  type: 'dark',
  colors: rawToSemantic({
    type: 'dark',
    Background: '#0b0e14',
    Foreground: '#aeaca6',
    AccentBlue: '#39BAE6',
    AccentPurple: '#D2A6FF',
    AccentCyan: '#95E6CB',
    AccentGreen: '#AAD94C',
    AccentYellow: '#FFB454',
    AccentRed: '#F26D78',
    DiffAdded: '#293022',
    DiffRemoved: '#3D1215',
    Comment: '#646A71',
    Gray: '#3D4149',
    DarkGray: '#24282F',
    GradientColors: ['#FFB454', '#F26D78'],
  }),
};

const ayuLightTheme: ThemeDefinition = {
  name: 'Ayu Light',
  type: 'light',
  colors: rawToSemantic({
    type: 'light',
    Background: '#f8f9fa',
    Foreground: '#5c6166',
    AccentBlue: '#399ee6',
    AccentPurple: '#a37acc',
    AccentCyan: '#4cbf99',
    AccentGreen: '#86b300',
    AccentYellow: '#f2ae49',
    AccentRed: '#f07171',
    DiffAdded: '#C6EAD8',
    DiffRemoved: '#FFCCCC',
    Comment: '#ABADB1',
    Gray: '#a6aaaf',
    DarkGray: '#CFD2D5',
    GradientColors: ['#399ee6', '#86b300'],
  }),
};

const atomOneTheme: ThemeDefinition = {
  name: 'Atom One',
  type: 'dark',
  colors: rawToSemantic({
    type: 'dark',
    Background: '#282c34',
    Foreground: '#abb2bf',
    AccentBlue: '#61aeee',
    AccentPurple: '#c678dd',
    AccentCyan: '#56b6c2',
    AccentGreen: '#98c379',
    AccentYellow: '#e6c07b',
    AccentRed: '#e06c75',
    DiffAdded: '#39544E',
    DiffRemoved: '#562B2F',
    Comment: '#5c6370',
    Gray: '#5c6370',
    DarkGray: '#424852',
    GradientColors: ['#61aeee', '#98c379'],
  }),
};

const draculaTheme: ThemeDefinition = {
  name: 'Dracula',
  type: 'dark',
  colors: rawToSemantic({
    type: 'dark',
    Background: '#282a36',
    Foreground: '#a3afb7',
    AccentBlue: '#8be9fd',
    AccentPurple: '#ff79c6',
    AccentCyan: '#8be9fd',
    AccentGreen: '#50fa7b',
    AccentYellow: '#fff783',
    AccentRed: '#ff5555',
    DiffAdded: '#11431d',
    DiffRemoved: '#6e1818',
    Comment: '#6272a4',
    Gray: '#6272a4',
    DarkGray: '#454E6D',
    GradientColors: ['#ff79c6', '#8be9fd'],
  }),
};

const githubTheme: ThemeDefinition = {
  name: 'GitHub',
  type: 'dark',
  colors: rawToSemantic({
    type: 'dark',
    Background: '#24292e',
    Foreground: '#c0c4c8',
    AccentBlue: '#79B8FF',
    AccentPurple: '#B392F0',
    AccentCyan: '#9ECBFF',
    AccentGreen: '#85E89D',
    AccentYellow: '#FFAB70',
    AccentRed: '#F97583',
    DiffAdded: '#3C4636',
    DiffRemoved: '#502125',
    Comment: '#6A737D',
    Gray: '#6A737D',
    DarkGray: '#474E56',
    GradientColors: ['#79B8FF', '#85E89D'],
  }),
};

const githubLightTheme: ThemeDefinition = {
  name: 'GitHub Light',
  type: 'light',
  colors: rawToSemantic({
    type: 'light',
    Background: '#f8f8f8',
    Foreground: '#24292E',
    AccentBlue: '#458',
    AccentPurple: '#900',
    AccentCyan: '#009926',
    AccentGreen: '#008080',
    AccentYellow: '#990073',
    AccentRed: '#d14',
    DiffAdded: '#C6EAD8',
    DiffRemoved: '#FFCCCC',
    Comment: '#998',
    Gray: '#999',
    DarkGray: '#C9C9C9',
    FocusColor: '#458',
    GradientColors: ['#458', '#008080'],
  }),
};

const googleCodeTheme: ThemeDefinition = {
  name: 'Google Code',
  type: 'light',
  colors: rawToSemantic({
    type: 'light',
    Background: 'white',
    Foreground: '#444',
    AccentBlue: '#008',
    AccentPurple: '#606',
    AccentCyan: '#066',
    AccentGreen: '#080',
    AccentYellow: '#660',
    AccentRed: '#800',
    DiffAdded: '#C6EAD8',
    DiffRemoved: '#FEDEDE',
    Comment: '#5f6368',
    Gray: '#5F5F5F',
    DarkGray: '#AFAFAF',
    GradientColors: ['#066', '#606'],
  }),
};

const holidayTheme: ThemeDefinition = {
  name: 'Holiday',
  type: 'dark',
  colors: rawToSemantic({
    type: 'dark',
    Background: '#00210e',
    Foreground: '#F0F8FF',
    AccentBlue: '#3CB371',
    AccentPurple: '#FF9999',
    AccentCyan: '#33F9FF',
    AccentGreen: '#3CB371',
    AccentYellow: '#FFEE8C',
    AccentRed: '#FF6347',
    DiffAdded: '#2E8B57',
    DiffRemoved: '#CD5C5C',
    Comment: '#8FBC8F',
    Gray: '#D7F5D3',
    DarkGray: '#768876',
    FocusColor: '#33F9FF',
    GradientColors: ['#FF0000', '#FFFFFF', '#008000'],
  }),
};

const shadesOfPurpleTheme: ThemeDefinition = {
  name: 'Shades Of Purple',
  type: 'dark',
  colors: rawToSemantic({
    type: 'dark',
    Background: '#1e1e3f',
    Foreground: '#e3dfff',
    AccentBlue: '#a599e9',
    AccentPurple: '#ac65ff',
    AccentCyan: '#a1feff',
    AccentGreen: '#A5FF90',
    AccentYellow: '#fad000',
    AccentRed: '#ff628c',
    DiffAdded: '#383E45',
    DiffRemoved: '#572244',
    Comment: '#B362FF',
    Gray: '#726c86',
    DarkGray: '#504C6F',
    GradientColors: ['#4d21fc', '#847ace', '#ff628c'],
  }),
};

const solarizedDarkTheme: ThemeDefinition = {
  name: 'Solarized Dark',
  type: 'dark',
  colors: {
    text: {
      primary: '#839496',
      secondary: '#586e75',
      link: '#268bd2',
      accent: '#268bd2',
      response: '#839496',
    },
    background: {
      primary: '#002b36',
      message: '#073642',
      input: '#073642',
      focus: mix('#002b36', '#859900', 0.2),
      diff: { added: '#00382f', removed: '#3d0115' },
    },
    border: { default: '#073642' },
    ui: {
      comment: '#586e75',
      symbol: '#93a1a1',
      active: '#268bd2',
      dark: '#073642',
      focus: '#859900',
      gradient: ['#268bd2', '#2aa198', '#859900'],
    },
    status: {
      success: '#859900',
      warning: '#d0b000',
      error: '#dc322f',
    },
    agent: {
      red: '#EA4335',
      blue: '#669DF6',
      green: '#34A853',
      yellow: '#FBBC04',
      purple: '#AF5CF7',
      orange: '#E8710A',
      pink: '#FF63B8',
      cyan: '#24C1E0',
      badgeText: '#000000',
    },
  },
};

const solarizedLightTheme: ThemeDefinition = {
  name: 'Solarized Light',
  type: 'light',
  colors: {
    text: {
      primary: '#657b83',
      secondary: '#93a1a1',
      link: '#268bd2',
      accent: '#268bd2',
      response: '#657b83',
    },
    background: {
      primary: '#fdf6e3',
      message: '#eee8d5',
      input: '#eee8d5',
      focus: mix('#fdf6e3', '#859900', 0.08),
      diff: { added: '#d7f2d7', removed: '#f2d7d7' },
    },
    border: { default: '#eee8d5' },
    ui: {
      comment: '#93a1a1',
      symbol: '#586e75',
      active: '#268bd2',
      dark: '#eee8d5',
      focus: '#859900',
      gradient: ['#268bd2', '#2aa198', '#859900'],
    },
    status: {
      success: '#859900',
      warning: '#d0b000',
      error: '#dc322f',
    },
    agent: {
      red: '#D93025',
      blue: '#1A73E8',
      green: '#188038',
      yellow: '#E37400',
      purple: '#8430CE',
      orange: '#C5621C',
      pink: '#D01884',
      cyan: '#007B83',
      badgeText: '#FFFFFF',
    },
  },
};

const xcodeTheme: ThemeDefinition = {
  name: 'Xcode',
  type: 'light',
  colors: rawToSemantic({
    type: 'light',
    Background: '#fff',
    Foreground: '#444',
    AccentBlue: '#1c00cf',
    AccentPurple: '#aa0d91',
    AccentCyan: '#3F6E74',
    AccentGreen: '#007400',
    AccentYellow: '#836C28',
    AccentRed: '#c41a16',
    DiffAdded: '#C6EAD8',
    DiffRemoved: '#FEDEDE',
    Comment: '#007400',
    Gray: '#c0c0c0',
    DarkGray: '#E0E0E0',
    FocusColor: '#1c00cf',
    GradientColors: ['#1c00cf', '#007400'],
  }),
};

const tokyoNightTheme: ThemeDefinition = {
  name: 'Tokyo Night',
  type: 'dark',
  colors: rawToSemantic({
    type: 'dark',
    Background: '#1a1b26',
    Foreground: '#c0caf5',
    AccentBlue: '#bb9af7',
    AccentPurple: '#7aa2f7',
    AccentCyan: '#7dcfff',
    AccentGreen: '#1abc9c',
    AccentYellow: '#e0af68',
    AccentRed: '#db4b4b',
    DiffAdded: '#243e4a',
    DiffRemoved: '#4a272f',
    Comment: '#565f89',
    Gray: '#a9b1d6',
    DarkGray: '#3b4261',
    FocusColor: '#7aa2f7',
    GradientColors: ['#7aa2f7', '#bb9af7', '#7dcfff'],
  }),
};

const ansiTheme: ThemeDefinition = {
  name: 'ANSI',
  type: 'dark',
  colors: {
    text: {
      primary: '#EEEEEB',
      secondary: '#95958F',
      link: '#6AD86C',
      accent: '#957CAD',
      response: '#EEEEEB',
    },
    background: {
      primary: '#000000',
      message: '#31312D',
      input: '#3B3A35',
      focus: '#16391C',
      diff: { added: '#153123', removed: '#270D09' },
    },
    border: { default: '#484743' },
    ui: {
      comment: '#7B7B75',
      symbol: '#95958F',
      active: '#6AD86C',
      dark: '#484743',
      focus: '#73B78E',
      gradient: ['#4796E4', '#847ACE', '#C3677F'],
    },
    status: {
      error: '#EC5B56',
      success: '#73B78E',
      warning: '#EFB041',
    },
    agent: {
      red: '#EA4335',
      blue: '#669DF6',
      green: '#34A853',
      yellow: '#FBBC04',
      purple: '#AF5CF7',
      orange: '#E8710A',
      pink: '#FF63B8',
      cyan: '#24C1E0',
      badgeText: '#000000',
    },
  },
};

const ansiLightTheme: ThemeDefinition = {
  name: 'ANSI Light',
  type: 'light',
  colors: {
    text: {
      primary: '#000000',
      secondary: '#5F5F5F',
      link: '#005FAF',
      accent: '#5F00FF',
      response: '#000000',
    },
    background: {
      primary: '#FFFFFF',
      message: '#FAFAFA',
      input: '#E4E4E4',
      focus: '#D7FFD7',
      diff: { added: '#D7FFD7', removed: '#FFD7D7' },
    },
    border: { default: '#5F5F5F' },
    ui: {
      comment: '#008700',
      symbol: '#5F5F5F',
      active: '#005FAF',
      dark: '#5F5F5F',
      focus: '#006400',
      gradient: ['#4796E4', '#847ACE', '#C3677F'],
    },
    status: {
      error: '#AF0000',
      success: '#006400',
      warning: '#875F00',
    },
    agent: {
      red: '#D93025',
      blue: '#1A73E8',
      green: '#188038',
      yellow: '#E37400',
      purple: '#8430CE',
      orange: '#C5621C',
      pink: '#D01884',
      cyan: '#007B83',
      badgeText: '#FFFFFF',
    },
  },
};

const allThemes: ThemeDefinition[] = [
  darkTheme,
  lightTheme,
  ayuTheme,
  ayuLightTheme,
  atomOneTheme,
  draculaTheme,
  githubTheme,
  githubLightTheme,
  googleCodeTheme,
  holidayTheme,
  shadesOfPurpleTheme,
  solarizedDarkTheme,
  solarizedLightTheme,
  xcodeTheme,
  tokyoNightTheme,
  ansiTheme,
  ansiLightTheme,
];

let activeTheme: ThemeDefinition = darkTheme;

export function getTheme(): ThemeDefinition {
  return activeTheme;
}

export function setTheme(name: string): boolean {
  const found = allThemes.find(t => t.name.toLowerCase() === name.toLowerCase());
  if (found) {
    activeTheme = found;
    return true;
  }
  return false;
}

export function getAllThemes(): ThemeDefinition[] {
  return allThemes;
}

export const theme = new Proxy<SemanticColors>({} as SemanticColors, {
  get(_target, prop: keyof SemanticColors) {
    return activeTheme.colors[prop];
  },
});

export const tuiTheme = {
  colors: {
    get background() { return theme.background.primary; },
    get brand() { return theme.ui.active; },
    get brandStrong() { return theme.ui.focus; },
    get accent() { return theme.text.accent; },
    get text() { return theme.text.primary; },
    get muted() { return theme.text.secondary; },
    get subtle() { return theme.ui.comment; },
    get panel() { return theme.background.input; },
    get panelAlt() { return theme.background.primary; },
    get userBg() { return theme.background.message; },
    get userSymbol() { return theme.text.accent; },
    get model() { return theme.ui.active; },
    get warning() { return theme.status.warning; },
    get danger() { return theme.status.error; },
    get success() { return theme.status.success; },
    get border() { return theme.border.default; },
    get strongBorder() { return theme.ui.focus; },
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

export type TuiColor = string;
