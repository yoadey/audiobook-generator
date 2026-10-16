import { Alert, Badge, Button, Card, Group, List, Stack, Text } from '@mantine/core';
import { modals } from '@mantine/modals';
import { IconMicrophone, IconRefresh } from '@tabler/icons-react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { useEffect } from 'react';

import {
  analysisQuery,
  type Book,
  type Casting,
  castingActive,
  castingQuery,
  type CastingWarning,
  startCasting,
} from '../api/client';
import { formatNumber } from '../lib/format';
import { notifyError } from '../lib/notify';
import { AnchorLink, ButtonLink } from './links';

const statusColor: Record<Casting['status'], string> = {
  none: 'gray',
  queued: 'blue',
  running: 'blue',
  done: 'green',
  failed: 'red',
};

const statusLabel: Record<Casting['status'], string> = {
  none: 'not cast',
  queued: 'queued',
  running: 'casting',
  done: 'done',
  failed: 'failed',
};

function CharacterLink({ bookId, id, name }: { bookId: number; id: number; name: string }) {
  return (
    <AnchorLink
      to="/books/$bookId/characters/$characterId"
      params={{ bookId: String(bookId), characterId: String(id) }}
      size="sm"
    >
      {name}
    </AnchorLink>
  );
}

function Warning({ bookId, w }: { bookId: number; w: CastingWarning }) {
  return (
    <List.Item>
      <CharacterLink bookId={bookId} id={w.characterId} name={w.characterName} />{' '}
      {w.kind === 'narrator' ? (
        'sounds like the narrator'
      ) : (
        <>
          sounds like{' '}
          {w.otherId !== undefined && (
            <CharacterLink bookId={bookId} id={w.otherId} name={w.otherName ?? ''} />
          )}
        </>
      )}
    </List.Item>
  );
}

/** State and controls of a book's voice casting. */
export function CastingCard({ book }: { book: Book }) {
  const queryClient = useQueryClient();
  const casting = useQuery(castingQuery(book.id));
  // A finished analysis starts the casting on the server.
  const analysisStatus = useQuery(analysisQuery(book.id)).data?.status;
  useEffect(() => {
    void queryClient.invalidateQueries({ queryKey: ['books', book.id, 'casting'] });
  }, [analysisStatus, book.id, queryClient]);
  const start = useMutation({
    mutationFn: (restart: boolean) => startCasting(book.id, restart),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ['books', book.id] }),
    onError: (e) => notifyError('Could not start the casting', e),
  });

  const c = casting.data;
  if (!c || analysisStatus !== 'done') {
    return null;
  }
  const active = castingActive(c.status);
  const waitingUntil =
    c.job?.status === 'queued' && c.job.notBefore ? new Date(c.job.notBefore) : undefined;

  const confirmRecast = () =>
    modals.openConfirmModal({
      title: 'Cast all voices again?',
      children: (
        <Text size="sm">
          The main characters are chosen again and every voice the casting picked is replaced.
          Voices and tiers you chose by hand stay. Audio rendered so far keeps the old voices until
          it is rendered again.
        </Text>
      ),
      labels: { confirm: 'Recast', cancel: 'Cancel' },
      onConfirm: () => start.mutate(true),
    });

  return (
    <Card withBorder data-testid="casting-card">
      <Stack gap="sm">
        <Group justify="space-between">
          <Group gap="xs">
            <Text fw={600}>Voice casting</Text>
            <Badge variant="light" color={statusColor[c.status]}>
              {statusLabel[c.status]}
            </Badge>
          </Group>
          {c.mainCount > 0 && (
            <ButtonLink
              to="/books/$bookId/characters"
              params={{ bookId: String(book.id) }}
              variant="light"
              size="xs"
              leftSection={<IconMicrophone size={14} />}
            >
              {formatNumber(c.mainCount)} main characters
            </ButtonLink>
          )}
        </Group>
        <Text size="sm" c="dimmed">
          The {formatNumber(c.mainLimit)} most important speaking characters get a voice of their
          own, chosen by the LLM and checked to differ from the narrator ({c.narratorVoice}) and
          from each other. Everyone else speaks with a pool voice that fits their gender and age.
        </Text>
        {c.status !== 'none' && (
          <Text size="sm">
            {formatNumber(c.voicedCount)} of {formatNumber(c.mainCount)} main characters voiced ·{' '}
            {formatNumber(c.minorCount)} with pool voices
          </Text>
        )}
        {waitingUntil && (
          <Text size="sm" c="dimmed">
            Waiting for LLM quota until {waitingUntil.toLocaleString()}
          </Text>
        )}
        {c.status === 'failed' && c.error && (
          <Alert color="red" title="The casting stopped">
            {c.error}
          </Alert>
        )}
        {c.warnings.length > 0 && (
          <Alert color="yellow" title="Voices that are hard to tell apart">
            <List size="sm" spacing={2}>
              {c.warnings.map((w) => (
                <Warning
                  key={`${w.kind}-${w.characterId}-${w.otherId ?? 0}`}
                  bookId={book.id}
                  w={w}
                />
              ))}
            </List>
          </Alert>
        )}
        <Group>
          {(c.status === 'none' || c.status === 'failed') && (
            <Button
              leftSection={<IconMicrophone size={16} />}
              loading={start.isPending}
              onClick={() => start.mutate(false)}
            >
              Cast voices
            </Button>
          )}
          {c.status === 'done' && (
            <Button
              variant="default"
              leftSection={<IconMicrophone size={16} />}
              loading={start.isPending}
              onClick={() => start.mutate(false)}
            >
              Cast new main characters
            </Button>
          )}
          {!active && c.status !== 'none' && (
            <Button
              variant="subtle"
              leftSection={<IconRefresh size={16} />}
              onClick={confirmRecast}
            >
              Recast all
            </Button>
          )}
        </Group>
      </Stack>
    </Card>
  );
}
