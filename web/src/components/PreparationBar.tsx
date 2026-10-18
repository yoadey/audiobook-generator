import { Alert, Anchor, Badge, Button, Collapse, Group, Stack, Text } from '@mantine/core';
import { modals } from '@mantine/modals';
import { IconWand } from '@tabler/icons-react';
import { useMutation, useQueryClient } from '@tanstack/react-query';
import { useState } from 'react';

import { type ChapterDetail, startPreparation } from '../api/client';
import { notifyError } from '../lib/notify';

const statusLabel: Record<ChapterDetail['prepStatus'], string> = {
  none: 'not prepared',
  queued: 'queued',
  running: 'preparing',
  done: 'prepared',
  failed: 'failed',
};

const count = (n: number, word: string) => `${n} ${word}${n === 1 ? '' : 's'}`;

/** The spots of a chapter that are read differently than written. */
function spokenForms(chapter: ChapterDetail): { text: string; say: string }[] {
  const out: { text: string; say: string }[] = [];
  for (const p of chapter.paragraphs) {
    for (const s of p.segments) {
      const chars = Array.from(s.text);
      for (const f of s.spokenAs ?? []) {
        out.push({ text: chars.slice(f.start, f.end).join(''), say: f.say });
      }
    }
  }
  return out;
}

/** State and controls of a chapter's preparation for reading aloud. */
export function PreparationBar({
  chapter,
  enabled,
}: {
  chapter: ChapterDetail;
  /** The book prepares its chapters before rendering. */
  enabled: boolean;
}) {
  const queryClient = useQueryClient();
  const [open, setOpen] = useState(false);
  const p = chapter.preparation;
  const start = useMutation({
    mutationFn: (force: boolean) => startPreparation(chapter.id, force),
    onSuccess: () =>
      Promise.all([
        queryClient.invalidateQueries({ queryKey: ['chapters', chapter.id] }),
        queryClient.invalidateQueries({ queryKey: ['books', chapter.bookId, 'chapters'] }),
      ]),
    onError: (e) => notifyError('Could not prepare the chapter', e),
  });
  if (!p || chapter.excluded || (!enabled && p.status === 'none')) {
    return null;
  }
  const active = p.status === 'queued' || p.status === 'running';
  const waitingUntil =
    p.job?.status === 'queued' && p.job.notBefore ? new Date(p.job.notBefore) : undefined;
  const forms = open ? spokenForms(chapter) : [];
  return (
    <Stack gap={6} data-testid="preparation-bar">
      <Group gap="sm">
        <IconWand size={16} />
        <Text size="sm">
          Reading aloud:{' '}
          <Badge variant="light" color={p.status === 'failed' ? 'red' : 'gray'}>
            {statusLabel[p.status]}
          </Badge>
          {p.status === 'done' &&
            ` ${count(p.moods, 'mood')}, ${count(p.pauses, 'pause')}, ${count(p.flows, 'paragraph')} read on, ${count(p.spokenForms, 'spot')} read differently`}
          {!enabled && ' (not applied: the book does not prepare its chapters)'}
        </Text>
        {p.spokenForms > 0 && (
          <Anchor component="button" size="sm" onClick={() => setOpen((o) => !o)}>
            {open ? 'Hide spots' : 'Show spots'}
          </Anchor>
        )}
        {enabled && (p.status === 'none' || p.status === 'failed') && (
          <Button size="compact-xs" loading={start.isPending} onClick={() => start.mutate(false)}>
            Prepare
          </Button>
        )}
        {enabled && p.status === 'done' && (
          <Button
            size="compact-xs"
            variant="subtle"
            disabled={start.isPending}
            onClick={() =>
              modals.openConfirmModal({
                title: 'Prepare the chapter again?',
                children: (
                  <Text size="sm">
                    Moods, pauses and spoken forms are found again (this uses LLM quota);
                    pronunciations in the lexicon stay.
                  </Text>
                ),
                labels: { confirm: 'Prepare again', cancel: 'Cancel' },
                onConfirm: () => start.mutate(true),
              })
            }
          >
            Prepare again
          </Button>
        )}
      </Group>
      <Collapse expanded={open}>
        <Stack gap={2} pl="lg">
          {forms.map((f, i) => (
            <Text key={i} size="sm">
              <Text span c="dimmed">
                {f.text}
              </Text>{' '}
              → {f.say}
            </Text>
          ))}
        </Stack>
      </Collapse>
      {active && (
        <Text size="xs" c="dimmed">
          {waitingUntil
            ? `Waiting until ${waitingUntil.toLocaleString()} (LLM quota or a retry)`
            : 'The chapter is being prepared…'}
        </Text>
      )}
      {p.status === 'failed' && p.error && (
        <Alert color="red" variant="light" p="xs">
          {p.error} — the chapter is read as it is.
        </Alert>
      )}
    </Stack>
  );
}
