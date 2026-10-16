import type { ChapterDetail, Segment } from '../api/client';

const palette = [
  'blue',
  'grape',
  'teal',
  'orange',
  'pink',
  'cyan',
  'lime',
  'indigo',
  'red',
  'yellow',
  'violet',
  'green',
];

/** A stable color per speaker; the narrator is gray. */
export function speakerColor(id?: number): string {
  return id === undefined ? 'gray' : (palette[id % palette.length] ?? 'blue');
}

/**
 * Whether an automatic speaker should be checked by a person: only once the
 * chapter is attributed (before that every quote has confidence 0).
 */
export function unsure(c: ChapterDetail, s: Segment): boolean {
  return (
    c.attributionStatus === 'done' &&
    s.kind === 'quote' &&
    !s.speakerManual &&
    s.confidence < (c.attribution?.unsureBelow ?? 0.6)
  );
}

/** Names of the characters a chapter mentions or that speak in it. */
export function speakerNames(c: ChapterDetail): Map<number, string> {
  const names = new Map<number, string>();
  for (const x of [...(c.characters ?? []), ...(c.speakers ?? [])]) {
    names.set(x.id, x.name);
  }
  return names;
}
