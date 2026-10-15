import { describe, expect, it } from 'vitest';

import { formatListeningTime, formatNumber } from './format';

describe('format', () => {
  it('formats numbers', () => {
    expect(formatNumber(1234567)).toBe('1,234,567');
  });
  it('estimates listening time at 155 words per minute', () => {
    expect(formatListeningTime(50)).toBe('<1 min');
    expect(formatListeningTime(155 * 41)).toBe('41 min');
    expect(formatListeningTime(155 * 120)).toBe('2 h');
    expect(formatListeningTime(155 * 125)).toBe('2 h 5 min');
    expect(formatListeningTime(5_000_000)).toBe('537 h 38 min');
  });
});
