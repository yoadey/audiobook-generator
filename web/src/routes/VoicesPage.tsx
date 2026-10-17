import {
  Alert,
  Badge,
  Card,
  Center,
  Group,
  Loader,
  SegmentedControl,
  Select,
  Stack,
  Table,
  Text,
  TextInput,
  Title,
} from '@mantine/core';
import { useQuery } from '@tanstack/react-query';
import { useState } from 'react';

import {
  type Emotion,
  settingsQuery,
  systemInfoQuery,
  type TTSEngine,
  ttsEnginesQuery,
  type Voice,
  voicesQuery,
} from '../api/client';
import { VoicePreviewButton } from '../components/VoicePreviewButton';
import { usePreview } from '../lib/preview';

type Filter = 'all' | Voice['gender'];

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

/** The voices of an engine besides Kokoro, with previews. */
function EngineCard({
  engine: e,
  text,
  preview,
}: {
  engine: TTSEngine;
  text: string;
  preview: ReturnType<typeof usePreview>;
}) {
  const [emotion, setEmotion] = useState<Emotion>('neutral');
  return (
    <Card withBorder>
      <Stack gap="xs">
        <Group justify="space-between">
          <Group gap="xs">
            <Text fw={600}>{e.name}</Text>
            <Badge variant="light">
              {e.kind}
              {e.variant ? ` · ${e.variant}` : ''}
            </Badge>
            <Badge variant="light" color={e.error ? 'red' : 'green'}>
              {e.error ? 'unreachable' : `${e.voices.length} voices`}
            </Badge>
          </Group>
          {e.emotions && (
            <Select
              size="xs"
              w={150}
              data={emotions}
              value={emotion}
              onChange={(v) => v && setEmotion(v as Emotion)}
              allowDeselect={false}
              aria-label={`Preview emotion of ${e.name}`}
            />
          )}
        </Group>
        <Text size="sm" c="dimmed">
          {e.emotions
            ? 'Speaks emotions and moods itself.'
            : 'Emotions are given by loudness (this model has no emotion control).'}{' '}
          {e.speed ? '' : 'Paces itself. '}
          Choose its voices as <Text span ff="monospace">{`${e.name}:<voice>`}</Text> for a
          character (voice editor) or the narrator (book page, Narrator). Voices cloned from your
          own clips appear here once uploaded to the engine (see docs/tts-engines.md).
        </Text>
        {e.error && (
          <Text size="xs" c="red">
            {e.error}
          </Text>
        )}
        {e.voices.length > 0 && (
          <Group gap="xs">
            {e.voices.map((v) => (
              <Group key={v} gap={2} wrap="nowrap">
                <VoicePreviewButton
                  id={`${e.name}:${v}`}
                  voice={`${e.name}:${v}`}
                  speed={1}
                  text={text.trim() || undefined}
                  preview={preview}
                  label={`Listen to ${v}`}
                  emotion={e.emotions ? emotion : undefined}
                />
                <Text ff="monospace" size="sm">
                  {v}
                </Text>
              </Group>
            ))}
          </Group>
        )}
      </Stack>
    </Card>
  );
}

/** The Kokoro voice catalogue: what casting chooses from, with previews. */
export function VoicesPage() {
  const voices = useQuery(voicesQuery);
  const system = useQuery(systemInfoQuery);
  const kokoro = system.data?.tts.configured ?? true;
  const engines = useQuery(ttsEnginesQuery);
  const preview = usePreview();
  const settings = useQuery(settingsQuery);
  const [filter, setFilter] = useState<Filter>('all');
  // Tried out here only; the saved sample text is in Settings.
  const [text, setText] = useState('');

  const list = (voices.data ?? []).filter(
    (v) => v.language.startsWith('en-') && (filter === 'all' || v.gender === filter),
  );

  return (
    <Stack>
      <Group justify="space-between">
        <Title order={2}>Voices</Title>
        <SegmentedControl
          value={filter}
          onChange={(v) => setFilter(v as Filter)}
          data={[
            { value: 'all', label: 'All' },
            { value: 'female', label: 'Female' },
            { value: 'male', label: 'Male' },
          ]}
          aria-label="Gender"
        />
      </Group>
      <Text size="sm" c="dimmed">
        A book speaks with the engine of its narrator (book page, Narrator), and its characters are
        cast from that engine's voices. Kokoro's base voices are blended into a voice per main
        character (grades are the quality ratings of its model card, A best); other engines give
        each main character a voice of its own.
      </Text>
      <TextInput
        label="Sample text"
        placeholder={settings.data?.previewText || settings.data?.defaultPreviewText}
        value={text}
        onChange={(e) => setText(e.currentTarget.value)}
        maxLength={500}
        maw={720}
      />
      {(engines.data ?? []).map((e) => (
        <EngineCard key={e.name} engine={e} text={text} preview={preview} />
      ))}
      {engines.data?.length === 0 && (
        <Text size="xs" c="dimmed">
          No speech engines besides Kokoro are configured (chart values tts.engines or
          tts.chatterbox).
        </Text>
      )}
      {!kokoro && (
        <Text size="xs" c="dimmed">
          Kokoro is not part of this deployment.
        </Text>
      )}
      {kokoro && voices.isPending && (
        <Center py="xl">
          <Loader />
        </Center>
      )}
      {kokoro && voices.error && (
        <Alert color="red" title="Could not load the voices">
          {voices.error.message}
        </Alert>
      )}
      {kokoro && voices.data && (
        <Table striped highlightOnHover>
          <Table.Thead>
            <Table.Tr>
              <Table.Th w={40} />
              <Table.Th>Voice</Table.Th>
              <Table.Th>Gender</Table.Th>
              <Table.Th>Accent</Table.Th>
              <Table.Th>Grade</Table.Th>
              <Table.Th>Character</Table.Th>
            </Table.Tr>
          </Table.Thead>
          <Table.Tbody>
            {list.map((v) => (
              <Table.Tr key={v.id}>
                <Table.Td>
                  <VoicePreviewButton
                    id={v.id}
                    voice={v.available ? v.id : ''}
                    speed={1}
                    text={text.trim() || undefined}
                    preview={preview}
                    label={`Listen to ${v.id}`}
                  />
                </Table.Td>
                <Table.Td>
                  <Group gap={6}>
                    <Text ff="monospace" size="sm">
                      {v.id}
                    </Text>
                    {!v.available && (
                      <Badge size="xs" color="gray" variant="outline">
                        not on the server
                      </Badge>
                    )}
                  </Group>
                </Table.Td>
                <Table.Td>{v.gender}</Table.Td>
                <Table.Td>{v.language === 'en-GB' ? 'British' : 'American'}</Table.Td>
                <Table.Td>{v.grade ?? '–'}</Table.Td>
                <Table.Td>
                  <Text size="sm">{v.traits || '–'}</Text>
                </Table.Td>
              </Table.Tr>
            ))}
          </Table.Tbody>
        </Table>
      )}
    </Stack>
  );
}
