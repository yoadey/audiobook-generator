const numberFormat = new Intl.NumberFormat('en');

export function formatNumber(n: number): string {
  return numberFormat.format(n);
}

/** Narration speed used for estimates (Kokoro at speed 1.0). */
const WORDS_PER_MINUTE = 155;

/** Estimated listening time, e.g. "41 min" or "12 h 5 min". */
export function formatListeningTime(words: number): string {
  const minutes = Math.round(words / WORDS_PER_MINUTE);
  if (minutes < 1) {
    return '<1 min';
  }
  if (minutes < 60) {
    return `${minutes} min`;
  }
  const h = Math.floor(minutes / 60);
  const m = minutes % 60;
  return m === 0 ? `${formatNumber(h)} h` : `${formatNumber(h)} h ${m} min`;
}
