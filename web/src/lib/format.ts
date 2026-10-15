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

/** Clock-style duration: "4:05" or "1:02:03". */
export function formatDuration(ms: number): string {
  const total = Math.max(0, Math.floor(ms / 1000));
  const h = Math.floor(total / 3600);
  const m = Math.floor((total % 3600) / 60);
  const s = String(total % 60).padStart(2, '0');
  return h > 0 ? `${h}:${String(m).padStart(2, '0')}:${s}` : `${m}:${s}`;
}
