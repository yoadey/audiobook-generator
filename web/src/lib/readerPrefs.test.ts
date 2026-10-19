import { describe, expect, it } from 'vitest';

import { DEFAULT_PREFS, parsePrefs } from './readerPrefs';

describe('parsePrefs', () => {
  it('falls back to the defaults', () => {
    expect(parsePrefs(null)).toEqual(DEFAULT_PREFS);
    expect(parsePrefs('not json')).toEqual(DEFAULT_PREFS);
    expect(parsePrefs('[1]')).toEqual(DEFAULT_PREFS);
  });

  it('keeps the earlier size and font', () => {
    expect(parsePrefs('{"size":"L","font":"sans"}')).toEqual({
      ...DEFAULT_PREFS,
      size: 21,
      font: 'sans',
    });
  });

  it('keeps values in range and drops unknown ones', () => {
    expect(
      parsePrefs(
        JSON.stringify({
          size: 99,
          lineHeight: 0.5,
          margin: -3,
          font: 'comic',
          width: 'wide',
          highlight: 'sentence',
          showPosition: false,
          paper: 'sepia',
        }),
      ),
    ).toEqual({
      ...DEFAULT_PREFS,
      size: 32,
      lineHeight: 1.3,
      margin: 0,
      width: 'wide',
      highlight: 'sentence',
      showPosition: false,
      paper: 'sepia',
    });
  });
});
