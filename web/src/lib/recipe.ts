/** One base voice of a Kokoro voice recipe. */
export interface RecipePart {
  voice: string;
  weight: number;
}

const partRe = /^([a-z]{2}_[a-z0-9]+)(?:\((\d+(?:\.\d+)?)\))?$/;

/** Parses "bf_emma(0.7)+af_bella(0.3)" (or "af_heart"); undefined if malformed. */
export function parseRecipe(recipe: string): RecipePart[] | undefined {
  const parts: RecipePart[] = [];
  for (const raw of recipe.trim().split('+')) {
    const m = partRe.exec(raw.trim());
    if (!m?.[1]) {
      return undefined;
    }
    parts.push({ voice: m[1], weight: m[2] ? Number(m[2]) : 1 });
  }
  return parts.length > 0 ? parts : undefined;
}

/** Formats parts as a recipe with weights normalized to sum to 1. */
export function formatRecipe(parts: RecipePart[]): string {
  const used = parts.filter((p) => p.voice && p.weight > 0);
  const total = used.reduce((sum, p) => sum + p.weight, 0);
  if (used.length === 0 || total <= 0) {
    return '';
  }
  if (used.length === 1) {
    return used[0]?.voice ?? '';
  }
  return used.map((p) => `${p.voice}(${Number((p.weight / total).toFixed(2))})`).join('+');
}

const engineVoiceRe = /^([a-z][a-z0-9-]{0,30}):([A-Za-z0-9][A-Za-z0-9._-]{0,63})$/;

/** Splits a voice of another engine ("chatterbox:Emily.wav"); undefined for Kokoro's. */
export function splitEngineVoice(voice: string): { engine: string; voice: string } | undefined {
  const m = engineVoiceRe.exec(voice.trim());
  return m?.[1] && m[2] ? { engine: m[1], voice: m[2] } : undefined;
}
