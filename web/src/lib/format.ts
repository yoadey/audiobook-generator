const numberFormat = new Intl.NumberFormat('en');

export function formatNumber(n: number): string {
  return numberFormat.format(n);
}

/** Narration speed used for estimates (Kokoro at speed 1.0). */
const WORDS_PER_MINUTE = 155;
/** An adult's silent reading speed. */
const READ_WORDS_PER_MINUTE = 250;

/** Estimated listening time, e.g. "41 min" or "12 h 5 min". */
export function formatListeningTime(words: number): string {
  return formatMinutes(Math.round(words / WORDS_PER_MINUTE));
}

/** Estimated reading time, e.g. "26 min" or "7 h 30 min". */
export function formatReadingTime(words: number): string {
  return formatMinutes(Math.round(words / READ_WORDS_PER_MINUTE));
}

function formatMinutes(minutes: number): string {
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

/** Time until a moment, e.g. "in 45 s", "in 12 min", "in 3 h 5 min"; "now" when past. */
export function formatUntil(iso: string, now: number = Date.now()): string {
  const s = Math.round((new Date(iso).getTime() - now) / 1000);
  if (s <= 0) {
    return 'now';
  }
  if (s < 60) {
    return `in ${s} s`;
  }
  const minutes = Math.round(s / 60);
  if (minutes < 60) {
    return `in ${minutes} min`;
  }
  const h = Math.floor(minutes / 60);
  const m = minutes % 60;
  return m === 0 ? `in ${h} h` : `in ${h} h ${m} min`;
}

/** Compact counts: 950, 12.3k, 1.2M. */
export function formatCompact(n: number): string {
  return new Intl.NumberFormat('en', { notation: 'compact', maximumFractionDigits: 1 }).format(n);
}
