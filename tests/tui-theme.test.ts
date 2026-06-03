import { describe, expect, it } from 'bun:test';
import { getAllThemes, normalizeHexColor } from '../src/tui/theme';

describe('TUI theme colors', () => {
  it('normalizes named and shorthand colors before mixing derived colors', () => {
    expect(normalizeHexColor('white')).toBe('#ffffff');
    expect(normalizeHexColor('#fff')).toBe('#ffffff');
    expect(normalizeHexColor('#458')).toBe('#445588');
  });

  it('exposes normalized 6-digit hex colors for every theme surface', () => {
    const hex = /^#[0-9a-fA-F]{6}$/;

    for (const theme of getAllThemes()) {
      expect(theme.colors.background.primary, theme.name).toMatch(hex);
      expect(theme.colors.background.message, theme.name).toMatch(hex);
      expect(theme.colors.background.input, theme.name).toMatch(hex);
      expect(theme.colors.text.primary, theme.name).toMatch(hex);
      expect(theme.colors.ui.gradient, theme.name).not.toHaveLength(0);
      for (const color of theme.colors.ui.gradient) {
        expect(color, theme.name).toMatch(hex);
      }
    }
  });
});
