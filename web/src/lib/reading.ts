import type { Cue } from '../api/client';

/** Index of the cue being spoken at ms (the last one started), -1 before the first. */
export function cueIndexAt(cues: Cue[], ms: number): number {
  let lo = 0;
  let hi = cues.length - 1;
  let found = -1;
  while (lo <= hi) {
    const mid = (lo + hi) >> 1;
    if (cues[mid]!.startMs <= ms) {
      found = mid;
      lo = mid + 1;
    } else {
      hi = mid - 1;
    }
  }
  return found;
}

/** The segment spoken at ms (undefined before the first or for the title). */
export function segmentAt(cues: Cue[], ms: number): number | undefined {
  const i = cueIndexAt(cues, ms);
  const id = i >= 0 ? cues[i]!.segmentId : 0;
  return id === 0 ? undefined : id;
}

/** Where a segment starts being spoken. */
export function startOf(cues: Cue[], segmentId: number): number | undefined {
  return cues.find((c) => c.segmentId === segmentId)?.startMs;
}

/**
 * Where to resume: positionMs, unless the stored segment says the reader
 * moved on (read without audio, or past what was heard); then the start
 * of that segment.
 */
export function resumeAt(cues: Cue[], positionMs: number, segmentId?: number): number {
  if (segmentId === undefined) return positionMs;
  const start = startOf(cues, segmentId);
  if (start === undefined) return positionMs;
  return segmentAt(cues, positionMs) === segmentId ? positionMs : start;
}
