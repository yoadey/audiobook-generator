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
  Tooltip,
} from '@mantine/core';
import { IconPlus, IconRefresh, IconRotate, IconTrash } from '@tabler/icons-react';
import { useMutation, useQuery } from '@tanstack/react-query';
import { useState } from 'react';

import {
  type CharacterDetail,
  type CharacterUpdate,
  quotesQuery,
  updateCharacter,
  type Voice,
  voicesQuery,
} from '../api/client';
import { notifyError, notifySuccess } from '../lib/notify';
import { usePreview } from '../lib/preview';
import { formatRecipe, parseRecipe, type RecipePart } from '../lib/recipe';
import { VoicePreviewButton } from './VoicePreviewButton';

type TierChoice = 'auto' | 'main' | 'minor';

const MAX_PARTS = 3;

const genderGroup: Record<Voice['gender'], string> = {
  female: 'Female',
  male: 'Male',
  unknown: 'Other',
};

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
  onSaved,
}: {
  character: CharacterDetail;
  onSaved: () => void;
}) {
  const voices = useQuery(voicesQuery);
  const quotes = useQuery(quotesQuery(c.id));
  const preview = usePreview();
  const initialTier: TierChoice = c.tierManual ? c.tier : 'auto';
  const initialParts = parseRecipe(c.voice) ?? [];
  const [tier, setTier] = useState<TierChoice>(initialTier);
  const [parts, setParts] = useState<RecipePart[]>(initialParts);
  const [speed, setSpeed] = useState(c.voiceSpeed);
  const [quote, setQuote] = useState(0);

  const recipe = formatRecipe(parts);
  const voiceChanged = recipe !== formatRecipe(initialParts) || speed !== c.voiceSpeed;
  const dirty = tier !== initialTier || voiceChanged;
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
    save.mutate(body);
  };

  const setPart = (i: number, p: Partial<RecipePart>) =>
    setParts(parts.map((old, j) => (i === j ? { ...old, ...p } : old)));
  const total = parts.reduce((sum, p) => sum + p.weight, 0);
  const options = voiceOptions(
    voices.data ?? [],
    parts.map((p) => p.voice),
  );

  const source =
    c.voiceSource === 'own'
      ? c.voiceManual
        ? 'Chosen by you; the casting keeps it.'
        : `Cast automatically${c.voiceNote ? `: ${c.voiceNote}` : '.'}`
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

        {parts.map((p, i) => (
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
                aria-label={`Weight of ${p.voice}`}
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
        <Group>
          <Button
            variant="subtle"
            size="xs"
            leftSection={<IconPlus size={14} />}
            disabled={parts.length >= MAX_PARTS || options.length === 0}
            onClick={() =>
              setParts([...parts, { voice: options[0]?.items[0]?.value ?? '', weight: 0.3 }])
            }
          >
            Add base voice
          </Button>
        </Group>
        <Stack gap={4} maw={320}>
          <Text size="sm" fw={500}>
            Speed: {speed.toFixed(2)}×
          </Text>
          <Slider
            min={0.8}
            max={1.25}
            step={0.01}
            value={speed}
            onChange={setSpeed}
            label={null}
            aria-label="Speed"
          />
        </Stack>

        <Group gap="xs" align="center" wrap="nowrap">
          <VoicePreviewButton
            id="editor"
            voice={recipe}
            speed={speed}
            text={sample?.text}
            preview={preview}
            label="Listen to this voice"
          />
          <Text size="sm" ff="monospace">
            {recipe || '—'}
          </Text>
        </Group>
        {sample ? (
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
              onClick={() => save.mutate({ voice: '' })}
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
