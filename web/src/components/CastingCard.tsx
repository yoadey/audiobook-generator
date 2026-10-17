import { Alert, Badge, Button, Card, Group, List, Stack, Text, Tooltip } from '@mantine/core';
import { modals } from '@mantine/modals';
import { IconMicrophone, IconRefresh, IconWand } from '@tabler/icons-react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { useEffect } from 'react';

import {
  analysisQuery,
  type Book,
  type Casting,
  castingActive,
  castingQuery,
  type CastingWarning,
  designBookVoices,
  startCasting,
  systemInfoQuery,
} from '../api/client';
import { formatNumber } from '../lib/format';
import { notifyError, notifySuccess } from '../lib/notify';
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
          {w.otherId !== undefined ? (
            <CharacterLink bookId={bookId} id={w.otherId} name={w.otherName ?? ''} />
          ) : (
            'another character'
          )}
        </>
      )}
    </List.Item>
  );
}

/** State and controls of a book's voice casting. */
export function CastingCard({ book }: { book: Book }) {
  const queryClient = useQueryClient();
  const analysis = useQuery(analysisQuery(book.id)).data;
  const analysisStatus = analysis?.status;
  // A finished analysis starts the casting on the server right after it
  // reports "done": look again for a short while until it shows up.
  const finishedAt = analysisStatus === 'done' ? analysis?.finishedAt : undefined;
  const base = castingQuery(book.id);
  const casting = useQuery({
    ...base,
    refetchInterval: (q) => {
      const justFinished =
        finishedAt !== undefined && Date.now() - new Date(finishedAt).getTime() < 60_000;
      if (q.state.data?.status === 'none' && justFinished) {
        return 3000;
      }
      return typeof base.refetchInterval === 'function' ? base.refetchInterval(q) : false;
    },
  });
  useEffect(() => {
    void queryClient.invalidateQueries({ queryKey: ['books', book.id, 'casting'] });
  }, [analysisStatus, book.id, queryClient]);
  const system = useQuery(systemInfoQuery);
  const design = useMutation({
    mutationFn: () => designBookVoices(book.id),
    onSuccess: (r) => {
      notifySuccess(
        r.queued > 0
          ? `Designing ${r.queued} voice${r.queued === 1 ? '' : 's'}: minutes each, the server starts first`
          : 'Every main character has a voice chosen by hand or being designed',
      );
      return queryClient.invalidateQueries({ queryKey: ['characters'] });
    },
    onError: (e) => notifyError('Could not design the voices', e),
  });
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
              disabled={start.isPending}
              onClick={confirmRecast}
            >
              Recast all
            </Button>
          )}
          {system.data?.tts.voiceDesignEngine === book.engine && c.mainCount > 0 && (
            <Tooltip label="A voice of its own from each main character's profile (not those chosen by hand)">
              <Button
                variant="subtle"
                leftSection={<IconWand size={16} />}
                loading={design.isPending}
                onClick={() =>
                  modals.openConfirmModal({
                    title: 'Design voices for the main characters?',
                    children: (
                      <Text size="sm">
                        Each main character whose voice was not chosen by hand gets a voice designed
                        from its profile. This takes minutes per voice on CPU; edit a description
                        first on the character's page if you like.
                      </Text>
                    ),
                    labels: { confirm: 'Design voices', cancel: 'Cancel' },
                    onConfirm: () => design.mutate(),
                  })
                }
              >
                Design voices
              </Button>
            </Tooltip>
          )}
        </Group>
      </Stack>
    </Card>
  );
}
