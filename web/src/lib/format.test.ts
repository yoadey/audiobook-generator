import { describe, expect, it } from 'vitest';

import {
  formatCompact,
  formatListeningTime,
  formatNumber,
  formatReadingTime,
  formatUntil,
} from './format';

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
    expect(formatReadingTime(250 * 90)).toBe('1 h 30 min');
  });
  it('formats time until a moment', () => {
    const now = Date.parse('2026-10-15T12:00:00Z');
    expect(formatUntil('2026-10-15T11:59:00Z', now)).toBe('now');
    expect(formatUntil('2026-10-15T12:00:45Z', now)).toBe('in 45 s');
    expect(formatUntil('2026-10-15T12:12:00Z', now)).toBe('in 12 min');
    expect(formatUntil('2026-10-15T15:00:00Z', now)).toBe('in 3 h');
    expect(formatUntil('2026-10-15T15:05:00Z', now)).toBe('in 3 h 5 min');
  });
  it('formats compact counts', () => {
    expect(formatCompact(950)).toBe('950');
    expect(formatCompact(12_345)).toBe('12.3K');
    expect(formatCompact(1_250_000)).toBe('1.3M');
  });
});
