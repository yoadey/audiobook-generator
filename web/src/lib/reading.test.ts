import { describe, expect, it } from 'vitest';

import { cueIndexAt, resumeAt, segmentAt, startOf } from './reading';

const cues = [
  { segmentId: 0, startMs: 300, endMs: 900 },
  { segmentId: 11, startMs: 1900, endMs: 2500 },
  { segmentId: 12, startMs: 2650, endMs: 4000 },
  { segmentId: 13, startMs: 4450, endMs: 5000 },
];

describe('reading along', () => {
  it('finds the cue being spoken, also in the pause after it', () => {
    expect(cueIndexAt(cues, 0)).toBe(-1);
    expect(cueIndexAt(cues, 300)).toBe(0);
    expect(cueIndexAt(cues, 2600)).toBe(1);
    expect(cueIndexAt(cues, 2650)).toBe(2);
    expect(cueIndexAt(cues, 99_999)).toBe(3);
    expect(cueIndexAt([], 10)).toBe(-1);
  });

  it('names no segment for the spoken title', () => {
    expect(segmentAt(cues, 500)).toBeUndefined();
    expect(segmentAt(cues, 0)).toBeUndefined();
    expect(segmentAt(cues, 3000)).toBe(12);
    expect(startOf(cues, 13)).toBe(4450);
    expect(startOf(cues, 99)).toBeUndefined();
  });

  it('resumes at the segment read last unless the position is in it', () => {
    expect(resumeAt(cues, 3000, 12)).toBe(3000);
    expect(resumeAt(cues, 0, 13)).toBe(4450);
    expect(resumeAt(cues, 3000, undefined)).toBe(3000);
    expect(resumeAt(cues, 3000, 99)).toBe(3000);
  });
});
