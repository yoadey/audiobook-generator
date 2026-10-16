import type { Segment } from '../api/client';

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

/** Whether an automatic speaker should be checked by a person. */
export function unsure(s: Segment, below: number): boolean {
  return s.kind === 'quote' && !s.speakerManual && s.confidence < below;
}
