// Reading fonts shipped with the app (latin only: the books are English).
// A face is only downloaded once a page uses it.
import '@fontsource/literata/latin-400.css';
import '@fontsource/literata/latin-400-italic.css';
import '@fontsource/literata/latin-700.css';
import '@fontsource/merriweather/latin-400.css';
import '@fontsource/merriweather/latin-400-italic.css';
import '@fontsource/merriweather/latin-700.css';
import '@fontsource/eb-garamond/latin-400.css';
import '@fontsource/eb-garamond/latin-400-italic.css';
import '@fontsource/eb-garamond/latin-700.css';
import '@fontsource/atkinson-hyperlegible/latin-400.css';
import '@fontsource/atkinson-hyperlegible/latin-400-italic.css';
import '@fontsource/atkinson-hyperlegible/latin-700.css';
import '@fontsource/lexend/latin-400.css';
import '@fontsource/lexend/latin-700.css';
import '@fontsource/opendyslexic/latin-400.css';
import '@fontsource/opendyslexic/latin-400-italic.css';
import '@fontsource/opendyslexic/latin-700.css';

const SERIF = 'ui-serif, "Iowan Old Style", "Palatino Linotype", Georgia, Cambria, serif';

/** The fonts to read in: the system's own and the ones shipped. */
export const FONTS = {
  serif: { label: 'Serif', family: SERIF },
  sans: { label: 'Sans', family: 'var(--mantine-font-family)' },
  literata: { label: 'Literata', family: `Literata, ${SERIF}` },
  merriweather: { label: 'Merriweather', family: `Merriweather, ${SERIF}` },
  garamond: { label: 'EB Garamond', family: `"EB Garamond", ${SERIF}` },
  atkinson: {
    label: 'Atkinson Hyperlegible',
    family: '"Atkinson Hyperlegible", var(--mantine-font-family)',
  },
  lexend: { label: 'Lexend', family: 'Lexend, var(--mantine-font-family)' },
  dyslexic: { label: 'OpenDyslexic', family: 'OpenDyslexic, var(--mantine-font-family)' },
  mono: { label: 'Monospace', family: 'var(--mantine-font-family-monospace)' },
} as const;
export type Font = keyof typeof FONTS;

/** How wide the column of text may get (em of the text's size). */
export const WIDTHS = { narrow: 30, medium: 38, wide: 46, full: undefined } as const;
export type Width = keyof typeof WIDTHS;

/** Reading preferences (kept per browser). */
export type ReaderPrefs = {
  font: Font;
  /** Text size in px. */
  size: number;
  /** Line height (times the text size). */
  lineHeight: number;
  width: Width;
  /** Space to the left and right of the text in px. */
  margin: number;
  align: 'start' | 'justify';
  /** Paragraphs with a gap between them, or indented like a printed book. */
  paragraphs: 'spaced' | 'indented';
  paper: 'default' | 'sepia';
  /** What is marked while the audio plays. */
  highlight: 'word' | 'sentence' | 'off';
  /** Mark the position (where listening goes on) while paused. */
  showPosition: boolean;
};

export const SIZE = { min: 14, max: 32 };
export const LINE_HEIGHT = { min: 1.3, max: 2.2 };
export const MARGIN = { min: 0, max: 64 };

export const DEFAULT_PREFS: ReaderPrefs = {
  font: 'serif',
  size: 18,
  lineHeight: 1.75,
  width: 'medium',
  margin: 16,
  align: 'start',
  paragraphs: 'spaced',
  paper: 'default',
  highlight: 'word',
  showPosition: true,
};

// The sizes before they could be set freely.
const OLD_SIZES: Record<string, number> = { S: 16, M: 18, L: 21, XL: 24 };

const within = (v: unknown, { min, max }: { min: number; max: number }, fallback: number) =>
  typeof v === 'number' && Number.isFinite(v) ? Math.min(max, Math.max(min, v)) : fallback;
const oneOf = <T extends string>(v: unknown, values: readonly T[], fallback: T): T =>
  values.includes(v as T) ? (v as T) : fallback;

/** The stored preferences, with defaults for what is missing or invalid. */
export function parsePrefs(raw: string | null): ReaderPrefs {
  let p: Record<string, unknown> = {};
  try {
    const parsed: unknown = JSON.parse(raw ?? '{}');
    if (parsed && typeof parsed === 'object') p = parsed as Record<string, unknown>;
  } catch {
    // Unreadable: the defaults.
  }
  const d = DEFAULT_PREFS;
  const size = typeof p.size === 'string' ? OLD_SIZES[p.size] : p.size;
  return {
    font: oneOf(p.font, Object.keys(FONTS) as Font[], d.font),
    size: Math.round(within(size, SIZE, d.size)),
    lineHeight: within(p.lineHeight, LINE_HEIGHT, d.lineHeight),
    width: oneOf(p.width, Object.keys(WIDTHS) as Width[], d.width),
    margin: Math.round(within(p.margin, MARGIN, d.margin)),
    align: oneOf(p.align, ['start', 'justify'], d.align),
    paragraphs: oneOf(p.paragraphs, ['spaced', 'indented'], d.paragraphs),
    paper: oneOf(p.paper, ['default', 'sepia'], d.paper),
    highlight: oneOf(p.highlight, ['word', 'sentence', 'off'], d.highlight),
    showPosition: typeof p.showPosition === 'boolean' ? p.showPosition : d.showPosition,
  };
}
