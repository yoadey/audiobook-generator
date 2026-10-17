import { Alert, Badge, Button, Card, Group, Stack, Text, Textarea } from '@mantine/core';
import { IconWand } from '@tabler/icons-react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { useState } from 'react';

import { bookQuery, type CharacterDetail, designVoice, systemInfoQuery } from '../api/client';
import { notifyError } from '../lib/notify';

const statusLabel: Record<CharacterDetail['voiceDesign']['status'], string> = {
  none: '',
  queued: 'queued',
  running: 'designing',
  done: 'designed',
  failed: 'failed',
};

/**
 * Designs a voice for the character from a description (where the
 * deployment has a voice design server): the clip becomes its voice.
 */
export function VoiceDesignCard({ character: c }: { character: CharacterDetail }) {
  const queryClient = useQueryClient();
  const system = useQuery(systemInfoQuery);
  const book = useQuery(bookQuery(c.bookId));
  const d = c.voiceDesign;
  const [description, setDescription] = useState(d.description || d.suggestedDescription);
  const busy = d.status === 'queued' || d.status === 'running';
  const start = useMutation({
    mutationFn: () => designVoice(c.id, description.trim()),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ['characters', c.id] }),
    onError: (e) => notifyError('Could not design the voice', e),
  });
  const engine = system.data?.tts.voiceDesignEngine;
  if (!engine || !book.data) {
    return null;
  }
  // Designed voices are clips of one engine; a book speaks with one.
  const otherEngine = book.data.engine !== engine;
  return (
    <Card withBorder data-testid="voice-design">
      <Stack gap="sm">
        <Group justify="space-between">
          <Text fw={600}>Design a voice</Text>
          {d.status !== 'none' && (
            <Badge
              variant="light"
              color={d.status === 'failed' ? 'red' : d.status === 'done' ? 'green' : 'blue'}
            >
              {statusLabel[d.status]}
            </Badge>
          )}
        </Group>
        <Text size="sm" c="dimmed">
          Describe the voice: age, gender, timbre, accent, how the character speaks. A voice design
          server speaks a sample in it, and {engine} clones the clip as the character&apos;s voice.
          This takes minutes on CPU; the server starts for it and stops again when idle.
        </Text>
        {otherEngine && (
          <Alert color="gray" variant="light">
            This book speaks with {book.data.engine}: switch it to {engine} to design voices.
          </Alert>
        )}
        {busy && (
          <Alert color="blue" variant="light">
            {d.status === 'queued'
              ? 'Waiting for its turn (the server may have to start first).'
              : 'The voice is being designed.'}
          </Alert>
        )}
        {d.status === 'failed' && d.error && (
          <Alert color="red" variant="light">
            {d.error}
          </Alert>
        )}
        <Textarea
          aria-label="Voice description"
          value={description}
          onChange={(e) => setDescription(e.currentTarget.value)}
          disabled={busy}
          maxLength={500}
          autosize
          minRows={2}
        />
        <Group justify="space-between">
          <Button
            variant="subtle"
            size="xs"
            disabled={busy || description === d.suggestedDescription}
            onClick={() => setDescription(d.suggestedDescription)}
          >
            From the profile
          </Button>
          <Button
            leftSection={<IconWand size={16} />}
            loading={start.isPending}
            disabled={busy || otherEngine || description.trim() === ''}
            onClick={() => start.mutate()}
          >
            {d.status === 'done' ? 'Design again' : 'Design voice'}
          </Button>
        </Group>
      </Stack>
    </Card>
  );
}
