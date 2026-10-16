import {
  Alert,
  Badge,
  Box,
  Button,
  Group,
  Popover,
  Select,
  Stack,
  Text,
  Tooltip,
} from '@mantine/core';
import { useDebouncedValue } from '@mantine/hooks';
import { modals } from '@mantine/modals';
import { IconAlertTriangle, IconUserQuestion, IconUsers } from '@tabler/icons-react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { useState } from 'react';

import {
  type ChapterDetail,
  charactersQuery,
  type Emotion,
  type Segment,
  startAttribution,
  updateQuote,
} from '../api/client';
import { notifyError } from '../lib/notify';
import { speakerColor, unsure } from '../lib/speakers';

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

const statusLabel: Record<ChapterDetail['attributionStatus'], string> = {
  none: 'not attributed',
  queued: 'queued',
  running: 'attributing',
  done: 'attributed',
  failed: 'failed',
};

/** State and controls of a chapter's speaker attribution. */
export function AttributionBar({
  chapter,
  onlyUnsure,
  onOnlyUnsure,
}: {
  chapter: ChapterDetail;
  onlyUnsure: boolean;
  onOnlyUnsure: (on: boolean) => void;
}) {
  const queryClient = useQueryClient();
  const a = chapter.attribution;
  const start = useMutation({
    mutationFn: (force: boolean) => startAttribution(chapter.id, force),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ['chapters', chapter.id] }),
    onError: (e) => notifyError('Could not attribute the speakers', e),
  });
  if (!a || a.quotes === 0 || chapter.excluded) {
    return null;
  }
  const active = a.status === 'queued' || a.status === 'running';
  const waitingUntil =
    a.job?.status === 'queued' && a.job.notBefore ? new Date(a.job.notBefore) : undefined;
  return (
    <Stack gap={6} data-testid="attribution-bar">
      <Group gap="sm">
        <IconUsers size={16} />
        <Text size="sm">
          Speakers:{' '}
          <Badge variant="light" color={a.status === 'failed' ? 'red' : 'gray'}>
            {statusLabel[a.status]}
          </Badge>{' '}
          {a.attributed} of {a.quotes} quotes with a speaker
          {a.unsure > 0 && `, ${a.unsure} to check`}
        </Text>
        {a.unsure > 0 && (
          <Button
            size="compact-xs"
            variant={onlyUnsure ? 'filled' : 'light'}
            color="yellow"
            leftSection={<IconAlertTriangle size={12} />}
            onClick={() => onOnlyUnsure(!onlyUnsure)}
          >
            {onlyUnsure ? 'Show all' : 'Show only those to check'}
          </Button>
        )}
        {(a.status === 'none' || a.status === 'failed') && (
          <Button size="compact-xs" loading={start.isPending} onClick={() => start.mutate(false)}>
            Attribute speakers
          </Button>
        )}
        {a.status === 'done' && (
          <Button
            size="compact-xs"
            variant="subtle"
            disabled={start.isPending}
            onClick={() =>
              modals.openConfirmModal({
                title: 'Attribute the speakers again?',
                children: (
                  <Text size="sm">
                    Speakers found automatically are replaced (this uses LLM quota); speakers you
                    chose stay.
                  </Text>
                ),
                labels: { confirm: 'Attribute again', cancel: 'Cancel' },
                onConfirm: () => start.mutate(true),
              })
            }
          >
            Attribute again
          </Button>
        )}
      </Group>
      {active && (
        <Text size="xs" c="dimmed">
          {waitingUntil
            ? `Waiting for LLM quota until ${waitingUntil.toLocaleString()}`
            : 'The speakers are being attributed…'}
        </Text>
      )}
      {a.status === 'failed' && a.error && (
        <Alert color="red" variant="light" p="xs">
          {a.error} — the quotes are read by the narrator.
        </Alert>
      )}
    </Stack>
  );
}

/** Speaker and emotion of a quotation, chosen by hand. */
function QuoteEditor({
  chapter,
  segment,
  onDone,
}: {
  chapter: ChapterDetail;
  segment: Segment;
  onDone: () => void;
}) {
  const queryClient = useQueryClient();
  const [speaker, setSpeaker] = useState<string>(
    segment.speakerId === undefined ? 'narrator' : String(segment.speakerId),
  );
  const [emotion, setEmotion] = useState<string>(segment.emotion ?? 'neutral');
  const [search, setSearch] = useState('');
  const [debounced] = useDebouncedValue(search.trim(), 300);
  const found = useQuery({
    ...charactersQuery(chapter.bookId, debounced, 'importance', 1, 20),
    enabled: debounced.length >= 2,
  });
  const known = new Map<number, string>();
  for (const c of [...(chapter.speakers ?? []), ...(chapter.characters ?? [])]) {
    known.set(c.id, c.name);
  }
  for (const c of found.data?.items ?? []) {
    known.set(c.id, c.name);
  }
  const save = useMutation({
    mutationFn: () =>
      updateQuote(chapter.id, segment.quoteRef, {
        ...(speaker === 'narrator' ? { narrator: true } : { speakerId: Number(speaker) }),
        emotion: emotion as Emotion,
      }),
    onSuccess: (detail) => {
      queryClient.setQueryData(['chapters', chapter.id], detail);
      void queryClient.invalidateQueries({ queryKey: ['books', chapter.bookId] });
      onDone();
    },
    onError: (e) => notifyError('Could not save the speaker', e),
  });
  return (
    <Stack gap="xs" w={280}>
      <Select
        label="Speaker"
        data={[
          { value: 'narrator', label: 'Narrator (no speaker)' },
          ...[...known].map(([id, name]) => ({ value: String(id), label: name })),
        ]}
        value={speaker}
        onChange={(v) => v && setSpeaker(v)}
        searchable
        searchValue={search}
        onSearchChange={setSearch}
        nothingFoundMessage="Type to search all characters"
        allowDeselect={false}
        comboboxProps={{ withinPortal: false }}
      />
      <Select
        label="Emotion"
        data={emotions}
        value={emotion}
        onChange={(v) => v && setEmotion(v)}
        allowDeselect={false}
        comboboxProps={{ withinPortal: false }}
      />
      <Group justify="flex-end">
        <Button size="xs" loading={save.isPending} onClick={() => save.mutate()}>
          Save
        </Button>
      </Group>
    </Stack>
  );
}

/** A piece of a quotation, colored by its speaker; click to change it. */
export function QuoteSpan({
  chapter,
  segment,
  first,
}: {
  chapter: ChapterDetail;
  segment: Segment;
  first: boolean;
}) {
  const [open, setOpen] = useState(false);
  const names = new Map<number, string>();
  for (const c of [...(chapter.characters ?? []), ...(chapter.speakers ?? [])]) {
    names.set(c.id, c.name);
  }
  const name = segment.speakerId === undefined ? 'Narrator' : (names.get(segment.speakerId) ?? '?');
  const color = speakerColor(segment.speakerId);
  const below = chapter.attribution?.unsureBelow ?? 0.6;
  const check =
    chapter.attributionStatus === 'done' || segment.speakerManual ? unsure(segment, below) : false;
  return (
    <Popover opened={open} onChange={setOpen} position="bottom-start" withArrow shadow="md">
      <Popover.Target>
        <Box
          component="span"
          data-quote={segment.quoteRef}
          role="button"
          tabIndex={0}
          aria-label={`Quote ${segment.quoteRef}, spoken by ${name}`}
          onClick={() => setOpen((o) => !o)}
          onKeyDown={(e) => (e.key === 'Enter' || e.key === ' ') && setOpen((o) => !o)}
          style={{
            background: `var(--mantine-color-${color}-light)`,
            borderRadius: 4,
            padding: '1px 2px',
            cursor: 'pointer',
            outline: check ? '1px dashed var(--mantine-color-yellow-6)' : undefined,
          }}
        >
          {first &&
            (segment.speakerId !== undefined ||
              segment.speakerManual ||
              chapter.attributionStatus === 'done') && (
              <Tooltip
                label={
                  segment.speakerManual
                    ? 'Chosen by you'
                    : `Confidence ${Math.round(segment.confidence * 100)}%`
                }
                openDelay={300}
              >
                <Text
                  component="span"
                  size="xs"
                  fw={600}
                  c={`${color}.7`}
                  mr={4}
                  style={{ whiteSpace: 'nowrap' }}
                >
                  {check ? (
                    <IconUserQuestion
                      size={12}
                      style={{ verticalAlign: -1 }}
                      aria-label="to check"
                    />
                  ) : null}{' '}
                  {name}
                  {segment.emotion && segment.emotion !== 'neutral' ? ` · ${segment.emotion}` : ''}
                </Text>
              </Tooltip>
            )}
          {segment.text}
        </Box>
      </Popover.Target>
      <Popover.Dropdown>
        {open && <QuoteEditor chapter={chapter} segment={segment} onDone={() => setOpen(false)} />}
      </Popover.Dropdown>
    </Popover>
  );
}
