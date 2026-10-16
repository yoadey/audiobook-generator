import {
  ActionIcon,
  Alert,
  Badge,
  Box,
  Breadcrumbs,
  Button,
  Center,
  Divider,
  Group,
  Loader,
  Stack,
  Switch,
  Text,
  TextInput,
  Title,
  Tooltip,
} from '@mantine/core';
import { modals } from '@mantine/modals';
import {
  IconArrowLeft,
  IconArrowRight,
  IconCheck,
  IconGitMerge,
  IconScissors,
  IconWaveSine,
} from '@tabler/icons-react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { useNavigate, useParams } from '@tanstack/react-router';
import { useState } from 'react';

import {
  audioUrl,
  bookQuery,
  type ChapterDetail,
  chapterQuery,
  mergeChapterWithNext,
  type Paragraph,
  renderChapter,
  splitChapter,
  updateChapter,
} from '../api/client';
import { AudioBadge } from '../components/AudioBadge';
import { AttributionBar, QuoteSpan } from '../components/ChapterSpeakers';
import { AnchorLink } from '../components/links';
import { confirmMerge } from '../lib/confirm';
import { formatListeningTime, formatNumber } from '../lib/format';
import { unsure } from '../lib/speakers';
import { notifyError, notifySuccess } from '../lib/notify';

const narrations: Record<NonNullable<ChapterDetail['narration']>, string> = {
  first_person: 'First person',
  third_person: 'Third person',
  mixed: 'Mixed narration',
  unknown: '',
};

function narrationLabel(c: ChapterDetail): string {
  const label = c.narration ? narrations[c.narration] : '';
  const pov = c.characters?.find((x) => x.id === c.povCharacterId)?.name;
  if (!label && !pov) {
    return '';
  }
  return `${[label, pov && `POV ${pov}`].filter(Boolean).join(', ')} · `;
}

function ParagraphView({
  chapter,
  p,
  firstPieces,
  onSplit,
}: {
  chapter: ChapterDetail;
  p: Paragraph;
  firstPieces: Set<number>;
  onSplit?: () => void;
}) {
  const body =
    p.kind === 'scene_break' ? (
      <Divider my="sm" label="⁂" labelPosition="center" />
    ) : p.kind === 'heading' ? (
      <Title order={3}>{p.text}</Title>
    ) : (
      <Text style={{ lineHeight: 1.7 }}>
        {p.segments.map((s, i) => (
          <span key={s.id}>
            {i > 0 && ' '}
            {s.kind === 'quote' ? (
              <QuoteSpan chapter={chapter} segment={s} first={firstPieces.has(s.id)} />
            ) : (
              s.text
            )}
          </span>
        ))}
      </Text>
    );
  return (
    <Group align="flex-start" wrap="nowrap" gap="xs">
      <Box w={28} pt={2}>
        {onSplit && (
          <Tooltip label="Start a new chapter here">
            <ActionIcon
              variant="subtle"
              color="gray"
              size="sm"
              onClick={onSplit}
              aria-label="Split here"
            >
              <IconScissors size={14} />
            </ActionIcon>
          </Tooltip>
        )}
      </Box>
      <Box style={{ flex: 1 }}>{body}</Box>
    </Group>
  );
}

function TitleEditor({ chapter, onSave }: { chapter: ChapterDetail; onSave: (t: string) => void }) {
  // Remounted (key) when the stored title changes, so no syncing effect.
  const [title, setTitle] = useState(chapter.title);
  const dirty = title.trim() !== '' && title.trim() !== chapter.title;
  return (
    <TextInput
      value={title}
      onChange={(e) => setTitle(e.currentTarget.value)}
      onKeyDown={(e) => e.key === 'Enter' && dirty && onSave(title.trim())}
      size="lg"
      aria-label="Chapter title"
      style={{ flex: 1 }}
      rightSection={
        dirty && (
          <ActionIcon onClick={() => onSave(title.trim())} aria-label="Save title">
            <IconCheck size={16} />
          </ActionIcon>
        )
      }
    />
  );
}

function SplitTitle({ onSubmit }: { onSubmit: (title: string) => void }) {
  const [title, setTitle] = useState('');
  return (
    <form
      onSubmit={(e) => {
        e.preventDefault();
        if (title.trim()) onSubmit(title.trim());
      }}
    >
      <Stack>
        <Text size="sm">Everything from this paragraph on becomes a new chapter.</Text>
        <TextInput
          label="Title of the new chapter"
          value={title}
          onChange={(e) => setTitle(e.currentTarget.value)}
          data-autofocus
        />
        <Group justify="flex-end">
          <Button type="submit" disabled={!title.trim()}>
            Split
          </Button>
        </Group>
      </Stack>
    </form>
  );
}

export function ChapterPage() {
  const { bookId: rawBook, chapterId: raw } = useParams({
    from: '/books/$bookId/chapters/$chapterId',
  });
  const bookId = Number(rawBook);
  const chapterId = Number(raw);
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const book = useQuery(bookQuery(bookId));
  const { data: chapter, error, isPending } = useQuery(chapterQuery(chapterId));
  const [onlyUnsure, setOnlyUnsure] = useState(false);

  const invalidate = () =>
    Promise.all([
      queryClient.invalidateQueries({ queryKey: ['books', bookId] }),
      queryClient.invalidateQueries({ queryKey: ['chapters'] }),
    ]);
  const update = useMutation({
    mutationFn: (body: { title?: string; excluded?: boolean }) => updateChapter(chapterId, body),
    onSuccess: invalidate,
    onError: (err) => notifyError('Could not update chapter', err),
  });
  const merge = useMutation({
    mutationFn: () => mergeChapterWithNext(chapterId),
    onSuccess: invalidate,
    onError: (err) => notifyError('Could not merge chapters', err),
  });
  const render = useMutation({
    mutationFn: () => renderChapter(chapterId, true),
    onSuccess: invalidate,
    onError: (err) => notifyError('Could not queue rendering', err),
  });
  const split = useMutation({
    mutationFn: ({ paragraphId, title }: { paragraphId: number; title: string }) =>
      splitChapter(chapterId, paragraphId, title),
    onSuccess: async (created) => {
      await invalidate();
      notifySuccess(`Created “${created.title}”`);
      await navigate({
        to: '/books/$bookId/chapters/$chapterId',
        params: { bookId: String(bookId), chapterId: String(created.id) },
      });
    },
    onError: (err) => notifyError('Could not split chapter', err),
  });

  if (isPending) {
    return (
      <Center py="xl">
        <Loader />
      </Center>
    );
  }
  if (error) {
    return (
      <Alert color="red" title="Could not load chapter">
        {error.message}
      </Alert>
    );
  }

  // The first piece of each quotation carries its speaker's name.
  const firstPieces = new Set<number>();
  const seenQuotes = new Set<number>();
  for (const p of chapter.paragraphs) {
    for (const s of p.segments) {
      if (s.quoteRef > 0 && !seenQuotes.has(s.quoteRef)) {
        seenQuotes.add(s.quoteRef);
        firstPieces.add(s.id);
      }
    }
  }
  const below = chapter.attribution?.unsureBelow ?? 0.6;
  const visible = onlyUnsure
    ? chapter.paragraphs.filter((p) => p.segments.some((s) => unsure(s, below)))
    : chapter.paragraphs;

  const go = (id: number | undefined) =>
    id &&
    navigate({
      to: '/books/$bookId/chapters/$chapterId',
      params: { bookId: String(bookId), chapterId: String(id) },
    });

  return (
    <Stack maw={860}>
      <Breadcrumbs>
        <AnchorLink to="/library" size="sm">
          Library
        </AnchorLink>
        <AnchorLink to="/books/$bookId" params={{ bookId: String(bookId) }} size="sm">
          {book.data?.title ?? 'Book'}
        </AnchorLink>
        <Text size="sm">
          #{chapter.idx + 1} {chapter.title}
        </Text>
      </Breadcrumbs>

      <Group wrap="nowrap">
        <TitleEditor
          key={`${chapter.id}:${chapter.title}`}
          chapter={chapter}
          onSave={(title) => update.mutate({ title })}
        />
      </Group>
      <Group justify="space-between">
        <Text size="sm" c="dimmed">
          {formatNumber(chapter.wordCount)} words · {formatNumber(chapter.quoteCount)} quotes ·{' '}
          {formatListeningTime(chapter.wordCount)}
        </Text>
        <Group>
          <Switch
            label="Narrate"
            checked={!chapter.excluded}
            onChange={(e) => update.mutate({ excluded: !e.currentTarget.checked })}
          />
          <Button
            variant="default"
            size="xs"
            leftSection={<IconGitMerge size={14} />}
            disabled={!chapter.nextChapterId}
            onClick={() => confirmMerge(chapter, undefined, () => merge.mutate())}
          >
            Merge with next
          </Button>
          <ActionIcon.Group>
            <ActionIcon
              variant="default"
              disabled={!chapter.prevChapterId}
              onClick={() => go(chapter.prevChapterId)}
              aria-label="Previous chapter"
            >
              <IconArrowLeft size={16} />
            </ActionIcon>
            <ActionIcon
              variant="default"
              disabled={!chapter.nextChapterId}
              onClick={() => go(chapter.nextChapterId)}
              aria-label="Next chapter"
            >
              <IconArrowRight size={16} />
            </ActionIcon>
          </ActionIcon.Group>
        </Group>
      </Group>

      <Group gap="sm">
        <AudioBadge chapter={chapter} />
        {chapter.audioStatus === 'ready' && (
          <audio
            key={`${chapter.id}:${chapter.audioDurationMs}`}
            controls
            preload="none"
            src={audioUrl(chapter.id)}
            style={{ height: 32, flex: 1 }}
          />
        )}
        <Button
          size="xs"
          variant="default"
          leftSection={<IconWaveSine size={14} />}
          loading={render.isPending}
          disabled={chapter.audioStatus === 'queued' || chapter.audioStatus === 'rendering'}
          onClick={() => render.mutate()}
        >
          {chapter.audioStatus === 'ready' ? 'Render again' : 'Render audio'}
        </Button>
      </Group>

      {(chapter.characters?.length ?? 0) > 0 && (
        <Group gap={6} data-testid="chapter-characters">
          <Text size="sm" c="dimmed">
            {narrationLabel(chapter)}Characters:
          </Text>
          {chapter.characters?.map((c) => (
            <AnchorLink
              key={c.id}
              to="/books/$bookId/characters/$characterId"
              params={{ bookId: String(bookId), characterId: String(c.id) }}
              size="sm"
            >
              <Badge
                variant={c.id === chapter.povCharacterId ? 'filled' : 'light'}
                style={{ cursor: 'pointer' }}
              >
                {c.name}
              </Badge>
            </AnchorLink>
          ))}
        </Group>
      )}

      <AttributionBar chapter={chapter} onlyUnsure={onlyUnsure} onOnlyUnsure={setOnlyUnsure} />

      <Stack gap="sm" mt="md">
        {visible.map((p) => (
          <ParagraphView
            key={p.id}
            chapter={chapter}
            p={p}
            firstPieces={firstPieces}
            onSplit={
              p.idx === 0 || onlyUnsure
                ? undefined
                : () =>
                    modals.open({
                      title: 'Split chapter',
                      children: (
                        <SplitTitle
                          onSubmit={(title) => {
                            modals.closeAll();
                            split.mutate({ paragraphId: p.id, title });
                          }}
                        />
                      ),
                    })
            }
          />
        ))}
      </Stack>
    </Stack>
  );
}
