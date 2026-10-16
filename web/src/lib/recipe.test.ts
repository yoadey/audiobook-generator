import { describe, expect, it } from 'vitest';

import { formatRecipe, parseRecipe } from './recipe';

describe('recipes', () => {
  it('parses and formats', () => {
    expect(parseRecipe('bf_emma(0.7)+af_bella(0.3)')).toEqual([
      { voice: 'bf_emma', weight: 0.7 },
      { voice: 'af_bella', weight: 0.3 },
    ]);
    expect(parseRecipe('af_heart')).toEqual([{ voice: 'af_heart', weight: 1 }]);
    expect(parseRecipe('nope')).toBeUndefined();
    expect(parseRecipe('')).toBeUndefined();
    expect(
      formatRecipe([
        { voice: 'bf_emma', weight: 2 },
        { voice: 'af_bella', weight: 1 },
      ]),
    ).toBe('bf_emma(0.67)+af_bella(0.33)');
    expect(formatRecipe([{ voice: 'af_heart', weight: 0.4 }])).toBe('af_heart');
    expect(formatRecipe([{ voice: '', weight: 1 }])).toBe('');
  });
});
