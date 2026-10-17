import {
  ActionIcon,
  Button,
  Card,
  Group,
  NumberInput,
  SegmentedControl,
  Select,
  Slider,
  Stack,
  Switch,
  Text,
  Textarea,
  TextInput,
  Tooltip,
} from '@mantine/core';
import { IconPlayerPlay, IconPlayerStop } from '@tabler/icons-react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { useRef, useState } from 'react';

import {
  type Book,
  previewVoice,
  settingsQuery,
  systemInfoQuery,
  ttsEnginesQuery,
  updateBook,
  voicesQuery,
} from '../api/client';
import { splitEngineVoice } from '../lib/recipe';
import { notifyError, notifySuccess } from '../lib/notify';

const accents: Record<string, string> = { 'en-US': 'American', 'en-GB': 'British' };

/** The engine name of Kokoro's voices (as the API reports a book's engine). */
const KOKORO = 'kokoro';

/** Narrator voice, synthesis speed and lookahead of a book. */
export function NarratorSettings({ book }: { book: Book }) {
  const queryClient = useQueryClient();
  const voices = useQuery(voicesQuery);
  const [voice, setVoice] = useState(book.narratorVoice);
  const [speed, setSpeed] = useState(book.narratorSpeed);
  const [lookahead, setLookahead] = useState<number | string>(book.lookahead);
  const [sampleText, setSampleText] = useState(book.previewText);
  const [prepare, setPrepare] = useState(book.prepareChapters);
  const settings = useQuery(settingsQuery);
  const [playing, setPlaying] = useState(false);
  const audio = useRef<HTMLAudioElement | null>(null);

  const english = (voices.data ?? []).filter((v) => v.language in accents && v.available);
  const groups = Object.entries(accents).map(([lang, label]) => ({
    group: label,
    items: english
      .filter((v) => v.language === lang)
      .map((v) => ({
        value: v.id,
        label: `${v.id} · ${v.gender}${v.grade ? ` · grade ${v.grade}` : ''}${v.traits ? ` · ${v.traits}` : ''}`,
      })),
  }));
  // The book speaks with the engine of its narrator: Kokoro (when this
  // deployment has it) or another engine ("chatterbox:Emily.wav").
  const system = useQuery(systemInfoQuery);
  const engines = useQuery(ttsEnginesQuery);
  const kokoro = system.data?.tts.configured ?? true;
  const engineVoice = splitEngineVoice(voice);
  const engine = engineVoice?.engine ?? KOKORO;
  const engineInfo = engines.data?.find((e) => e.name === engine);
  const engineChoices = [
    ...(kokoro || engine === KOKORO ? [{ value: KOKORO, label: 'Kokoro' }] : []),
    ...(engines.data ?? []).map((e) => ({
      value: e.name,
      label: `${e.name}${e.variant ? ` (${e.variant})` : ''}`,
    })),
  ];
  if (engine !== KOKORO && !engineInfo) {
    engineChoices.push({ value: engine, label: `${engine} (not configured)` });
  }
  const options =
    engine === KOKORO
      ? groups
      : [...new Set([...(engineInfo?.voices ?? []), engineVoice?.voice ?? ''])]
          .filter(Boolean)
          .map((v) => ({ value: `${engine}:${v}`, label: v }));
  const isMix = engine === KOKORO && !english.some((v) => v.id === voice);
  // Changing the engine starts from its default voice.
  const switchEngine = (next: string) => {
    if (next === engine) {
      return;
    }
    if (next === KOKORO) {
      const def = system.data?.tts.defaultVoice ?? '';
      setVoice(splitEngineVoice(def) ? (english[0]?.id ?? 'af_heart') : def || 'af_heart');
      return;
    }
    const info = engines.data?.find((e) => e.name === next);
    setVoice(info?.defaultVoice ?? (info?.voices[0] ? `${next}:${info.voices[0]}` : ''));
  };

  const save = useMutation({
    mutationFn: () =>
      updateBook(book.id, {
        narratorVoice: voice,
        narratorSpeed: speed,
        lookahead: Number(lookahead) || 0,
        previewText: sampleText,
        prepareChapters: prepare,
      }),
    onSuccess: () => {
      notifySuccess('Narrator settings saved');
      return queryClient.invalidateQueries({ queryKey: ['books', book.id] });
    },
    onError: (err) => notifyError('Could not save settings', err),
  });

  const preview = useMutation({
    // An edited sample text is heard before it is saved.
    mutationFn: () =>
      previewVoice(voice, speed, sampleText.trim() || undefined, undefined, book.id),
    onSuccess: (url) => {
      audio.current?.pause();
      const a = new Audio(url);
      audio.current = a;
      a.onended = () => setPlaying(false);
      setPlaying(true);
      void a.play();
    },
    onError: (err) => notifyError('Preview failed', err),
  });

  const dirty =
    voice !== book.narratorVoice ||
    speed !== book.narratorSpeed ||
    Number(lookahead) !== book.lookahead ||
    sampleText.trim() !== book.previewText ||
    prepare !== book.prepareChapters;

  return (
    <Card withBorder>
      <Stack gap="sm">
        <Text fw={600}>Narrator</Text>
        {engineChoices.length > 1 && (
          <Stack gap={4}>
            <Text size="sm" fw={500}>
              Engine
            </Text>
            <SegmentedControl
              w="fit-content"
              value={engine}
              onChange={switchEngine}
              data={engineChoices}
              aria-label="Engine"
            />
            {engine !== book.engine && (
              <Text size="xs" c="dimmed">
                Saving switches the book to {engine === KOKORO ? 'Kokoro' : engine}: the narrator
                and the characters cast automatically speak with its voices (the casting runs
                again); voices you chose for characters stay.
              </Text>
            )}
          </Stack>
        )}
        <Group align="flex-end" wrap="wrap">
          {isMix ? (
            <TextInput
              label="Voice (mix)"
              description="e.g. bf_emma(2)+af_bella(1)"
              value={voice}
              onChange={(e) => setVoice(e.currentTarget.value)}
              w={300}
            />
          ) : (
            <Select
              label="Voice"
              data={options}
              value={voice || null}
              onChange={(v) => v && setVoice(v)}
              searchable
              w={300}
              disabled={engine === KOKORO && voices.isError}
              placeholder={
                engine === KOKORO && voices.isError
                  ? 'TTS server unreachable'
                  : engineInfo?.error
                    ? `${engine} unreachable`
                    : undefined
              }
            />
          )}
          <Tooltip label={playing ? 'Stop' : 'Listen to a sample'}>
            <ActionIcon
              size="lg"
              variant="default"
              loading={preview.isPending}
              aria-label="Preview voice"
              onClick={() => {
                if (playing) {
                  audio.current?.pause();
                  setPlaying(false);
                } else {
                  preview.mutate();
                }
              }}
            >
              {playing ? <IconPlayerStop size={16} /> : <IconPlayerPlay size={16} />}
            </ActionIcon>
          </Tooltip>
          <Stack gap={4} w={220}>
            <Text size="sm" fw={500}>
              Narration speed: {speed.toFixed(2)}×
            </Text>
            <Slider
              min={0.7}
              max={1.5}
              step={0.05}
              value={speed}
              onChange={setSpeed}
              label={null}
            />
          </Stack>
          <NumberInput
            label="Keep ahead"
            description="chapters after your position"
            min={0}
            max={50}
            value={lookahead}
            onChange={setLookahead}
            w={160}
          />
        </Group>
        {engineVoice && (
          <Text size="xs" c="dimmed">
            {engineInfo
              ? `${engine}${engineInfo.variant ? ` (${engineInfo.variant})` : ''} reads most of the book: without a GPU it is much slower than Kokoro.${engineInfo.speed ? '' : ' It paces itself (the speed is not applied).'}`
              : `The engine ${engine} is not configured: chapters cannot be rendered with this voice.`}
          </Text>
        )}
        <Textarea
          label="Sample text"
          description="What voice previews in this book say (with its pronunciations); empty for the text in Settings."
          placeholder={settings.data?.previewText || settings.data?.defaultPreviewText}
          value={sampleText}
          onChange={(e) => setSampleText(e.currentTarget.value)}
          maxLength={500}
          autosize
          minRows={1}
          maw={720}
        />
        <Switch
          label="Prepare chapters with AI"
          description="Before a chapter is rendered, the LLM marks the mood of the narration, dramatic pauses and spots the voice would misread, and suggests pronunciations for the lexicon. The text itself is never changed; this uses LLM quota like the speaker attribution."
          checked={prepare}
          onChange={(e) => setPrepare(e.currentTarget.checked)}
          maw={720}
        />
        <Group>
          <Button onClick={() => save.mutate()} disabled={!dirty} loading={save.isPending}>
            Save
          </Button>
        </Group>
        <Text size="xs" c="dimmed">
          Changing the voice, speed or preparation marks existing audio as outdated; chapters in the
          lookahead window are rendered again automatically while you listen.
        </Text>
      </Stack>
    </Card>
  );
}
