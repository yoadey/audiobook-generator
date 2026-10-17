import {
  ActionIcon,
  Alert,
  Blockquote,
  Button,
  Card,
  Group,
  SegmentedControl,
  Select,
  Slider,
  Stack,
  Text,
  Textarea,
  Tooltip,
} from '@mantine/core';
import { IconPlus, IconRefresh, IconRotate, IconTrash } from '@tabler/icons-react';
import { useMutation, useQuery } from '@tanstack/react-query';
import { useState } from 'react';

import {
  type CharacterDetail,
  type CharacterUpdate,
  type Emotion,
  quotesQuery,
  ttsEnginesQuery,
  updateCharacter,
  type Voice,
  voicesQuery,
} from '../api/client';
import { notifyError, notifySuccess } from '../lib/notify';
import { usePreview } from '../lib/preview';
import { formatRecipe, parseRecipe, type RecipePart, splitEngineVoice } from '../lib/recipe';
import { VoicePreviewButton } from './VoicePreviewButton';

type TierChoice = 'auto' | 'main' | 'minor';

// As many base voices as the server accepts in a recipe.
const MAX_PARTS = 4;

const emotions: Emotion[] = [
  'neutral',
  'happy',
  'sad',
  'angry',
  'afraid',
  'surprised',
  'whispering',
  'shouting',
  'sarcastic',
];

const genderGroup: Record<Voice['gender'], string> = {
  female: 'Female',
  male: 'Male',
  unknown: 'Other',
};

/** Scales weights to sum to 1 (a mix like "a(2)+b(1)" may come from the narrator). */
function normalize(parts: RecipePart[]): RecipePart[] {
  const total = parts.reduce((sum, p) => sum + p.weight, 0);
  return total > 0
    ? parts.map((p) => ({
        ...p,
        weight: Math.max(0.05, Math.round((p.weight / total) * 100) / 100),
      }))
    : parts;
}

function voiceOptions(voices: Voice[], keep: string[]) {
  const usable = voices.filter(
    (v) => (v.available && v.language.startsWith('en-')) || keep.includes(v.id),
  );
  return (['female', 'male', 'unknown'] as const)
    .map((g) => ({
      group: genderGroup[g],
      items: usable
        .filter((v) => v.gender === g)
        .map((v) => ({
          value: v.id,
          label: `${v.id}${v.grade ? ` (${v.grade})` : ''}${v.traits ? ` · ${v.traits}` : ''}`,
        })),
    }))
    .filter((g) => g.items.length > 0);
}

/**
 * Tier, voice recipe and speed of a character, with previews of real quotes.
 * Remounted (key) when the stored character changes.
 */
export function VoiceEditor({
  character: c,
  castingActive = false,
  onSaved,
}: {
  character: CharacterDetail;
  castingActive?: boolean;
  onSaved: () => void;
}) {
  const voices = useQuery(voicesQuery);
  const quotes = useQuery(quotesQuery(c.id));
  const preview = usePreview(c.bookId);
  const initialTier: TierChoice = c.tierManual ? c.tier : 'auto';
  const engines = useQuery(ttsEnginesQuery);
  // A voice of another engine ("chatterbox:Emily.wav") or a Kokoro recipe.
  const initialEngine = splitEngineVoice(c.voice);
  const initialParts = normalize(initialEngine ? [] : (parseRecipe(c.voice) ?? []));
  const [tier, setTier] = useState<TierChoice>(initialTier);
  const [parts, setParts] = useState<RecipePart[]>(initialParts);
  const [engine, setEngine] = useState(initialEngine?.engine ?? '');
  const [engineVoice, setEngineVoice] = useState(initialEngine?.voice ?? '');
  const [emotion, setEmotion] = useState<Emotion>('neutral');
  const [speed, setSpeed] = useState(c.voiceSpeed);
  const [quote, setQuote] = useState(0);
  // Previews speak quotes from the book, or a text of the character's own.
  const [ownText, setOwnText] = useState(c.previewText);
  const [useOwn, setUseOwn] = useState(c.previewText !== '');

  const recipe = engine ? (engineVoice ? `${engine}:${engineVoice}` : '') : formatRecipe(parts);
  const initialRecipe = initialEngine ? c.voice.trim() : formatRecipe(initialParts);
  const voiceChanged = recipe !== initialRecipe || speed !== c.voiceSpeed;
  const engineInfo = engines.data?.find((e) => e.name === engine);
  const engineNames = (engines.data ?? []).map((e) => e.name);
  if (initialEngine && !engineNames.includes(initialEngine.engine)) {
    engineNames.push(initialEngine.engine);
  }
  const takesSpeed = !engine || (engineInfo?.speed ?? false);
  const previewText = useOwn ? ownText.trim() : '';
  const textChanged = previewText !== c.previewText;
  const dirty = tier !== initialTier || voiceChanged || textChanged;
  const samples = quotes.data ?? [];
  const sample = samples.length > 0 ? samples[quote % samples.length] : undefined;

  const save = useMutation({
    mutationFn: (body: CharacterUpdate) => updateCharacter(c.id, body),
    onSuccess: () => {
      notifySuccess('Voice saved');
      onSaved();
    },
    onError: (e) => notifyError('Could not save the voice', e),
  });
  const submit = () => {
    const body: CharacterUpdate = {};
    if (tier !== initialTier) {
      body.tier = tier;
    }
    if (voiceChanged && recipe) {
      body.voice = recipe;
      body.voiceSpeed = speed;
    }
    if (textChanged) {
      body.previewText = previewText;
    }
    save.mutate(body);
  };

  const setPart = (i: number, p: Partial<RecipePart>) =>
    setParts(parts.map((old, j) => (i === j ? { ...old, ...p } : old)));
  const total = parts.reduce((sum, p) => sum + p.weight, 0);
  const options = voiceOptions(
    voices.data ?? [],
    parts.map((p) => p.voice),
  );
  // The next voice to add: one not in the recipe yet, of the character's
  // gender where possible.
  const used = new Set(parts.map((p) => p.voice));
  const unused = (voices.data ?? []).filter(
    (v) => v.available && v.language.startsWith('en-') && !used.has(v.id),
  );
  const nextVoice = (unused.find((v) => v.gender === c.gender) ?? unused[0])?.id;

  const source =
    c.voiceSource === 'own'
      ? c.voiceManual
        ? 'Chosen by you; the casting keeps it.'
        : `Cast automatically${c.voiceNote ? `: ${c.voiceNote}` : '.'}`
      : c.tier === 'main' && castingActive
        ? 'A voice of its own is being cast; until then it speaks with a pool voice.'
        : c.voiceSource === 'pool'
          ? 'A pool voice for minor characters of this gender and age. Change it to give the character a voice of its own.'
          : 'No fitting voice is available; the narrator reads the lines.';

  return (
    <Card withBorder data-testid="voice-editor">
      <Stack gap="sm">
        <Group justify="space-between">
          <Text fw={600}>Voice</Text>
          <SegmentedControl
            size="xs"
            value={tier}
            onChange={(v) => setTier(v as TierChoice)}
            data={[
              { value: 'auto', label: `Automatic (${c.tier})` },
              { value: 'main', label: 'Main' },
              { value: 'minor', label: 'Minor' },
            ]}
            aria-label="Tier"
          />
        </Group>
        <Text size="sm" c="dimmed">
          {source}
        </Text>
        {voices.isError && (
          <Alert color="yellow" variant="light">
            The TTS server is unreachable; voices cannot be listed or previewed right now.
          </Alert>
        )}

        {engineNames.length > 0 && (
          <SegmentedControl
            size="xs"
            w="fit-content"
            value={engine}
            onChange={setEngine}
            data={[
              { value: '', label: 'Kokoro' },
              ...engineNames.map((n) => ({ value: n, label: n })),
            ]}
            aria-label="Engine"
          />
        )}
        {engine && (
          <>
            {!engineInfo ? (
              <Alert color="yellow" variant="light">
                The engine {engine} is not configured; lines with this voice cannot be rendered.
              </Alert>
            ) : (
              engineInfo.error && (
                <Alert color="yellow" variant="light">
                  Its voices could not be listed: {engineInfo.error}
                </Alert>
              )
            )}
            <Select
              label="Voice"
              data={[...new Set([...(engineInfo?.voices ?? []), engineVoice].filter(Boolean))]}
              value={engineVoice || null}
              onChange={(v) => v && setEngineVoice(v)}
              searchable
              allowDeselect={false}
              w={320}
              aria-label="Engine voice"
            />
            <Text size="xs" c="dimmed">
              {engineInfo?.emotions
                ? 'This engine speaks emotions and moods itself; its voices do not mix.'
                : 'Emotions are given by loudness; its voices do not mix.'}{' '}
              It has no word timings: the reader marks its lines by sentence.
            </Text>
          </>
        )}
        {!engine &&
          parts.map((p, i) => (
            <Group key={i} align="flex-end" wrap="nowrap">
              <Select
                label={i === 0 ? 'Base voices' : undefined}
                data={options}
                value={p.voice}
                onChange={(v) => v && setPart(i, { voice: v })}
                searchable
                allowDeselect={false}
                w={320}
                aria-label={`Base voice ${i + 1}`}
              />
              <Stack gap={2} style={{ flex: 1 }}>
                <Text size="xs" c="dimmed">
                  {total > 0 ? Math.round((p.weight / total) * 100) : 0}%
                </Text>
                <Slider
                  min={0.05}
                  max={1}
                  step={0.05}
                  value={p.weight}
                  onChange={(weight) => setPart(i, { weight })}
                  label={null}
                  thumbLabel={`Weight of ${p.voice}`}
                />
              </Stack>
              <Tooltip label="Remove">
                <ActionIcon
                  variant="subtle"
                  color="red"
                  disabled={parts.length <= 1}
                  onClick={() => setParts(parts.filter((_, j) => j !== i))}
                  aria-label={`Remove ${p.voice}`}
                >
                  <IconTrash size={16} />
                </ActionIcon>
              </Tooltip>
            </Group>
          ))}
        {!engine && (
          <Group>
            <Button
              variant="subtle"
              size="xs"
              leftSection={<IconPlus size={14} />}
              disabled={parts.length >= MAX_PARTS || !nextVoice}
              onClick={() => nextVoice && setParts([...parts, { voice: nextVoice, weight: 0.3 }])}
            >
              Add base voice
            </Button>
          </Group>
        )}
        {takesSpeed ? (
          <Stack gap={4} maw={320}>
            <Text size="sm" fw={500}>
              Speed: {speed.toFixed(2)}×
            </Text>
            <Slider
              min={0.5}
              max={2}
              step={0.01}
              value={speed}
              onChange={setSpeed}
              label={null}
              thumbLabel="Speed"
            />
          </Stack>
        ) : (
          <Text size="xs" c="dimmed">
            This engine paces itself.
          </Text>
        )}

        <Group gap="xs" align="center" wrap="nowrap">
          <VoicePreviewButton
            id="editor"
            voice={recipe}
            speed={speed}
            text={previewText || sample?.text}
            preview={preview}
            label="Listen to this voice"
            emotion={engineInfo?.emotions ? emotion : undefined}
          />
          <Text size="sm" ff="monospace">
            {recipe || '—'}
          </Text>
          {engineInfo?.emotions && (
            <Select
              size="xs"
              w={140}
              data={emotions}
              value={emotion}
              onChange={(v) => v && setEmotion(v as Emotion)}
              allowDeselect={false}
              aria-label="Preview emotion"
            />
          )}
        </Group>
        <SegmentedControl
          size="xs"
          w="fit-content"
          value={useOwn ? 'own' : 'quotes'}
          onChange={(v) => setUseOwn(v === 'own')}
          data={[
            { value: 'quotes', label: 'Quotes from the book' },
            { value: 'own', label: 'Own sample text' },
          ]}
          aria-label="Preview text"
        />
        {useOwn ? (
          <Textarea
            aria-label="Sample text"
            placeholder={sample?.text ?? 'What this character says in previews'}
            value={ownText}
            onChange={(e) => setOwnText(e.currentTarget.value)}
            maxLength={500}
            autosize
            minRows={2}
          />
        ) : sample ? (
          <Blockquote p="xs" cite={`— ${sample.chapterTitle}`}>
            <Group justify="space-between" wrap="nowrap" align="flex-start">
              <Text size="sm">{sample.text}</Text>
              {samples.length > 1 && (
                <Tooltip label="Another quote">
                  <ActionIcon
                    variant="subtle"
                    onClick={() => setQuote(quote + 1)}
                    aria-label="Another quote"
                  >
                    <IconRefresh size={16} />
                  </ActionIcon>
                </Tooltip>
              )}
            </Group>
          </Blockquote>
        ) : (
          <Text size="xs" c="dimmed">
            {quotes.isPending
              ? 'Looking for a quote…'
              : 'No quote of this character was found; previews use a standard sentence.'}
          </Text>
        )}

        <Group justify="flex-end">
          {c.voiceManual && (
            <Button
              variant="subtle"
              leftSection={<IconRotate size={16} />}
              loading={save.isPending}
              onClick={() =>
                save.mutate(tier !== initialTier ? { voice: '', tier } : { voice: '' })
              }
            >
              Back to the automatic voice
            </Button>
          )}
          <Button disabled={!dirty || !recipe} loading={save.isPending} onClick={submit}>
            Save voice
          </Button>
        </Group>
      </Stack>
    </Card>
  );
}
