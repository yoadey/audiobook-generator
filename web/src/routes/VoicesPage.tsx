import {
  Alert,
  Badge,
  Center,
  Group,
  Loader,
  SegmentedControl,
  Stack,
  Table,
  Text,
  TextInput,
  Title,
} from '@mantine/core';
import { useQuery } from '@tanstack/react-query';
import { useState } from 'react';

import { settingsQuery, type Voice, voicesQuery } from '../api/client';
import { VoicePreviewButton } from '../components/VoicePreviewButton';
import { usePreview } from '../lib/preview';

type Filter = 'all' | Voice['gender'];

/** The Kokoro voice catalogue: what casting chooses from, with previews. */
export function VoicesPage() {
  const voices = useQuery(voicesQuery);
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
        The English Kokoro base voices. Casting blends them into a voice per main character; minor
        characters speak with single base voices. Grades are the quality ratings of the Kokoro model
        card (A best).
      </Text>
      <TextInput
        label="Sample text"
        placeholder={settings.data?.previewText || settings.data?.defaultPreviewText}
        value={text}
        onChange={(e) => setText(e.currentTarget.value)}
        maxLength={500}
        maw={720}
      />
      {voices.isPending && (
        <Center py="xl">
          <Loader />
        </Center>
      )}
      {voices.error && (
        <Alert color="red" title="Could not load the voices">
          {voices.error.message}
        </Alert>
      )}
      {voices.data && (
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
