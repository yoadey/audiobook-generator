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

// Words whose period does not end a sentence (as the server's segmenter).
const ABBREVIATIONS = new Set(
  'mr mrs ms dr st jr sr prof capt gen col lt sgt rev hon vs etc no mt e.g i.e cf fig vol ch'.split(
    ' ',
  ),
);

function abbreviation(chars: string[], i: number): boolean {
  let j = i;
  while (j > 0 && /[\p{L}.]/u.test(chars[j - 1]!)) j--;
  const word = chars.slice(j, i).join('');
  return (word.length === 1 && /\p{Lu}/u.test(word)) || ABBREVIATIONS.has(word.toLowerCase());
}

/** Sentences of a text as [start, end) character ranges (all characters covered). */
export function sentenceRanges(text: string): [number, number][] {
  const chars = Array.from(text);
  const out: [number, number][] = [];
  let start = 0;
  for (let i = 0; i < chars.length; i++) {
    if (!'.!?…'.includes(chars[i]!)) continue;
    let j = i + 1;
    while (j < chars.length && '.!?…”’"\')]*'.includes(chars[j]!)) j++;
    if (j >= chars.length || chars[j] !== ' ') continue;
    const next = chars[j + 1] ?? '';
    if (!/[\p{Lu}\d“‘"'(—–]/u.test(next)) continue;
    if (chars[i] === '.' && abbreviation(chars, i)) continue;
    out.push([start, j]);
    start = j + 1;
    i = j;
  }
  if (start < chars.length) out.push([start, chars.length]);
  return out;
}

/** Index in cue.words of the word spoken at ms (-1 before the first or without words). */
function wordIndexAt(cue: Cue, ms: number): number {
  const words = cue.words ?? [];
  let found = -1;
  for (let i = 0; i < words.length && words[i]![1]! <= ms; i++) found = i;
  return found;
}

/**
 * The sentence of a segment being spoken at ms: the one of the word being
 * spoken when the cue has word times, else assuming the sentences take
 * time by their length (the cue is the whole segment).
 */
export function sentenceAt(text: string, cue: Cue, ms: number): [number, number] | undefined {
  const ranges = sentenceRanges(text);
  if (ranges.length < 2) return undefined;
  const words = cue.words ?? [];
  if (words.length > 0) {
    const at = words[Math.max(0, wordIndexAt(cue, ms))]![0]!;
    return ranges.find(([, b]) => at < b) ?? ranges[ranges.length - 1];
  }
  const total = ranges.reduce((n, [a, b]) => n + (b - a), 0);
  const share = Math.min(1, Math.max(0, (ms - cue.startMs) / Math.max(1, cue.endMs - cue.startMs)));
  let done = 0;
  for (const r of ranges) {
    done += r[1] - r[0];
    if (share * total < done) return r;
  }
  return ranges[ranges.length - 1];
}

/** The word being spoken at ms as [start, end) characters, when the cue has word times. */
export function wordAt(text: string, cue: Cue, ms: number): [number, number] | undefined {
  const i = wordIndexAt(cue, ms);
  if (i < 0 || ms >= cue.endMs) return undefined;
  const chars = Array.from(text);
  const start = cue.words![i]![0]!;
  let end = start;
  while (
    end < chars.length &&
    (/[\p{L}\p{N}]/u.test(chars[end]!) ||
      (/['’-]/.test(chars[end]!) && /[\p{L}\p{N}]/u.test(chars[end + 1] ?? '')))
  ) {
    end++;
  }
  return end > start ? [start, end] : undefined;
}

/** The cue of a segment. */
export function cueOf(cues: Cue[], segmentId: number): Cue | undefined {
  return cues.find((c) => c.segmentId === segmentId);
}

export type Piece = { text: string; italic: boolean; marked: boolean; word: boolean };

/**
 * Splits text into pieces at the edges of its italic runs, of the marked
 * range and of the word being spoken, for rendering.
 */
export function pieces(
  text: string,
  italic: number[][] = [],
  mark?: [number, number],
  word?: [number, number],
): Piece[] {
  const chars = Array.from(text);
  const cuts = new Set([0, chars.length]);
  for (const [a, b] of italic) {
    cuts.add(Math.max(0, Math.min(chars.length, a!)));
    cuts.add(Math.max(0, Math.min(chars.length, b!)));
  }
  for (const r of [mark, word]) {
    if (r) {
      cuts.add(r[0]);
      cuts.add(r[1]);
    }
  }
  const at = [...cuts].sort((x, y) => x - y);
  const out: Piece[] = [];
  for (let i = 0; i + 1 < at.length; i++) {
    const [a, b] = [at[i]!, at[i + 1]!];
    if (a >= b) continue;
    out.push({
      text: chars.slice(a, b).join(''),
      italic: italic.some(([s, e]) => s! <= a && b <= e!),
      marked: !!mark && mark[0] <= a && b <= mark[1],
      word: !!word && word[0] <= a && b <= word[1],
    });
  }
  return out;
}
