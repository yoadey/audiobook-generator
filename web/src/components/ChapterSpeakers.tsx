import { Alert, Badge, Box, Button, Group, Popover, Select, Stack, Text } from '@mantine/core';
import { useDebouncedValue } from '@mantine/hooks';
import { modals } from '@mantine/modals';
import { IconAlertTriangle, IconUserQuestion, IconUsers } from '@tabler/icons-react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { memo, useState } from 'react';

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
  analysisDone,
  onlyUnsure,
  onOnlyUnsure,
}: {
  chapter: ChapterDetail;
  analysisDone: boolean;
  onlyUnsure: boolean;
  onOnlyUnsure: (on: boolean) => void;
}) {
  const queryClient = useQueryClient();
  const a = chapter.attribution;
  const start = useMutation({
    mutationFn: (force: boolean) => startAttribution(chapter.id, force),
    onSuccess: () =>
      Promise.all([
        queryClient.invalidateQueries({ queryKey: ['chapters', chapter.id] }),
        queryClient.invalidateQueries({ queryKey: ['books', chapter.bookId, 'chapters'] }),
      ]),
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
        {(a.unsure > 0 || onlyUnsure) && (
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
          <Button
            size="compact-xs"
            loading={start.isPending}
            disabled={!analysisDone}
            title={analysisDone ? undefined : 'Needs a finished character analysis'}
            onClick={() => start.mutate(false)}
          >
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
            ? `Waiting until ${waitingUntil.toLocaleString()} (LLM quota, the analysis or a retry)`
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
  names,
  onDone,
}: {
  chapter: ChapterDetail;
  segment: Segment;
  names: Map<number, string>;
  onDone: () => void;
}) {
  const queryClient = useQueryClient();
  const [speaker, setSpeaker] = useState<string>(
    segment.speakerId === undefined ? 'narrator' : String(segment.speakerId),
  );
  const [emotion, setEmotion] = useState<Emotion>(segment.emotion ?? 'neutral');
  const [search, setSearch] = useState('');
  const [debounced] = useDebouncedValue(search.trim(), 300);
  const known = new Map(names);
  const selectedLabel =
    speaker === 'narrator' ? NARRATOR_LABEL : (known.get(Number(speaker)) ?? '');
  // Mantine puts the selected label into the search box: no search for it.
  const found = useQuery({
    ...charactersQuery(chapter.bookId, debounced, 'importance', 1, 20),
    enabled: debounced.length >= 2 && debounced !== selectedLabel,
  });
  const foundIds = new Set<string>();
  for (const c of found.data?.items ?? []) {
    known.set(c.id, c.name);
    foundIds.add(String(c.id));
  }
  const save = useMutation({
    mutationFn: () =>
      updateQuote(chapter.id, segment.quoteRef, {
        ...(speaker === 'narrator' ? { narrator: true } : { speakerId: Number(speaker) }),
        emotion,
      }),
    onSuccess: async (detail) => {
      await queryClient.cancelQueries({ queryKey: ['chapters', chapter.id] });
      queryClient.setQueryData(['chapters', chapter.id], detail);
      void queryClient.invalidateQueries({ queryKey: ['books', chapter.bookId] });
      // Characters' quotes and chapter lists change with the speaker.
      void queryClient.invalidateQueries({ queryKey: ['characters'] });
      onDone();
    },
    onError: (e) => notifyError('Could not save the speaker', e),
  });
  return (
    <Stack gap="xs" w={280}>
      <Select
        label="Speaker"
        data={[
          { value: 'narrator', label: NARRATOR_LABEL },
          ...[...known].map(([id, name]) => ({ value: String(id), label: name })),
        ]}
        value={speaker}
        onChange={(v) => v && setSpeaker(v)}
        searchable
        searchValue={search}
        onSearchChange={setSearch}
        // The server also matches aliases: keep what it found.
        filter={({ options, search: q }) =>
          options.filter(
            (o) =>
              'value' in o &&
              (foundIds.has(o.value) || o.label.toLowerCase().includes(q.toLowerCase().trim())),
          )
        }
        nothingFoundMessage="Type to search all characters"
        allowDeselect={false}
        comboboxProps={{ withinPortal: false }}
        data-autofocus
      />
      <Select
        label="Emotion"
        data={emotions}
        value={emotion}
        onChange={(v) => v && setEmotion(v as Emotion)}
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

const NARRATOR_LABEL = 'Narrator (no speaker)';

/** A piece of a quotation, colored by its speaker; click to change it. */
export const QuoteSpan = memo(function QuoteSpan({
  chapter,
  segment,
  first,
  names,
}: {
  chapter: ChapterDetail;
  segment: Segment;
  first: boolean;
  names: Map<number, string>;
}) {
  // The popover (and its listeners) only exists while it is open: a chapter
  // can have thousands of quote pieces.
  const [open, setOpen] = useState(false);
  const name = segment.speakerId === undefined ? 'Narrator' : (names.get(segment.speakerId) ?? '?');
  const color = speakerColor(segment.speakerId);
  const check = unsure(chapter, segment);
  const labeled =
    first &&
    (segment.speakerId !== undefined ||
      segment.speakerManual ||
      chapter.attributionStatus === 'done');
  const span = (
    <Box
      component="span"
      data-quote={segment.quoteRef}
      role="button"
      tabIndex={0}
      aria-label={`${name}${check ? ' (to check)' : ''}: ${segment.text}`}
      title={
        segment.speakerManual
          ? `${name}, chosen by you`
          : `${name}, confidence ${Math.round(segment.confidence * 100)}%`
      }
      onClick={() => setOpen((o) => !o)}
      onKeyDown={(e) => {
        if (e.key === 'Enter' || e.key === ' ') {
          e.preventDefault();
          setOpen((o) => !o);
        }
      }}
      style={{
        background: `var(--mantine-color-${color}-light)`,
        borderRadius: 4,
        padding: '1px 2px',
        cursor: 'pointer',
        outline: check ? '1px dashed var(--mantine-color-yellow-6)' : undefined,
      }}
    >
      {labeled && (
        <Text
          component="span"
          size="xs"
          fw={600}
          c={`${color}.7`}
          mr={4}
          style={{ whiteSpace: 'nowrap' }}
        >
          {check && <IconUserQuestion size={12} style={{ verticalAlign: -1 }} />} {name}
          {segment.emotion && segment.emotion !== 'neutral' ? ` · ${segment.emotion}` : ''}
        </Text>
      )}
      {segment.text}
    </Box>
  );
  if (!open) {
    return span;
  }
  return (
    <Popover
      opened
      onChange={setOpen}
      position="bottom-start"
      withArrow
      shadow="md"
      trapFocus
      returnFocus
    >
      <Popover.Target>{span}</Popover.Target>
      <Popover.Dropdown>
        <QuoteEditor
          chapter={chapter}
          segment={segment}
          names={names}
          onDone={() => setOpen(false)}
        />
      </Popover.Dropdown>
    </Popover>
  );
});
