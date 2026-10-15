import {
  ActionIcon,
  Alert,
  Box,
  Breadcrumbs,
  Button,
  Card,
  Center,
  Group,
  Loader,
  ScrollArea,
  Select,
  Slider,
  Stack,
  Text,
  Title,
  UnstyledButton,
} from '@mantine/core';
import {
  IconPlayerPauseFilled,
  IconPlayerPlayFilled,
  IconPlayerSkipBack,
  IconPlayerSkipForward,
  IconRewindBackward15,
  IconRewindForward30,
} from '@tabler/icons-react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { useParams } from '@tanstack/react-router';
import { useCallback, useEffect, useRef, useState } from 'react';

import {
  audioUrl,
  type Book,
  bookQuery,
  type Chapter,
  chaptersQuery,
  coverUrl,
  type Progress,
  progressQuery,
  renderChapter,
  saveProgress,
} from '../api/client';
import { AudioBadge } from '../components/AudioBadge';
import { AnchorLink } from '../components/links';
import { formatDuration } from '../lib/format';
import { notifyError } from '../lib/notify';

const SAVE_EVERY_MS = 15_000;
const RATES = ['0.8', '0.9', '1', '1.1', '1.25', '1.5', '1.75', '2'];
const RATE_KEY = 'abg.playbackRate';

function storedRate(): string {
  try {
    return localStorage.getItem(RATE_KEY) ?? '1';
  } catch {
    return '1';
  }
}

export function ListenPage() {
  const { bookId: raw } = useParams({ from: '/books/$bookId/listen' });
  const bookId = Number(raw);
  const book = useQuery(bookQuery(bookId));
  const chapters = useQuery(chaptersQuery(bookId));
  const progress = useQuery(progressQuery(bookId));

  if (book.isPending || chapters.isPending || progress.isPending) {
    return (
      <Center py="xl">
        <Loader />
      </Center>
    );
  }
  if (book.error || chapters.error || progress.error) {
    return (
      <Alert color="red" title="Could not load the book">
        {(book.error ?? chapters.error ?? progress.error)?.message}
      </Alert>
    );
  }
  const narrated = chapters.data.items.filter((c) => !c.excluded);
  if (narrated.length === 0) {
    return <Alert title="Nothing to play">This book has no narrated chapters.</Alert>;
  }
  return <Player book={book.data} narrated={narrated} progress={progress.data} />;
}

function Player({
  book,
  narrated,
  progress,
}: {
  book: Book;
  narrated: Chapter[];
  progress: Progress;
}) {
  const bookId = book.id;
  const queryClient = useQueryClient();

  // Start where the listener stopped, else at the first narrated chapter.
  const startId =
    progress.chapterId !== undefined && narrated.some((c) => c.id === progress.chapterId)
      ? progress.chapterId
      : narrated[0]!.id;
  const [currentId, setCurrentId] = useState(startId);
  // Position to seek to once the chapter's audio has loaded.
  const resumeAt = useRef<number | null>(
    progress.chapterId === startId ? progress.positionMs : null,
  );
  const autoplay = useRef(false);

  const index = narrated.findIndex((c) => c.id === currentId);
  const current: Chapter | undefined = narrated[index];
  const ready = current?.audioStatus === 'ready';

  const audio = useRef<HTMLAudioElement>(null);
  const [playing, setPlaying] = useState(false);
  const [position, setPosition] = useState(0);
  const [duration, setDuration] = useState(0);
  const [rate, setRate] = useState(storedRate);
  const lastSaved = useRef(0);

  const save = useMutation({
    mutationFn: ({ chapterId, ms }: { chapterId: number; ms: number }) =>
      saveProgress(bookId, chapterId, ms),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ['books', bookId, 'chapters'] }),
  });
  const persist = useCallback(
    (chapterId: number, ms: number) => {
      lastSaved.current = Date.now();
      save.mutate({ chapterId, ms });
    },
    [save],
  );

  const render = useMutation({
    mutationFn: (id: number) => renderChapter(id),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ['books', bookId, 'chapters'] }),
    onError: (err) => notifyError('Could not queue rendering', err),
  });

  // Entering a chapter stores the position, which also queues the
  // lookahead window on the server.
  const savedFor = useRef<number | null>(null);
  useEffect(() => {
    if (current && savedFor.current !== current.id) {
      savedFor.current = current.id;
      persist(current.id, resumeAt.current ?? 0);
    }
  }, [current, persist]);

  const goTo = useCallback(
    (c: Chapter | undefined, play: boolean) => {
      if (!c) return;
      if (current && audio.current) {
        persist(current.id, audio.current.currentTime * 1000);
      }
      resumeAt.current = null;
      autoplay.current = play;
      setPosition(0);
      setDuration(0);
      setCurrentId(c.id);
    },
    [current, persist],
  );

  const togglePlay = useCallback(() => {
    const a = audio.current;
    if (!a) return;
    if (a.paused) void a.play();
    else a.pause();
  }, []);
  const seek = useCallback((delta: number) => {
    const a = audio.current;
    if (a) a.currentTime = Math.max(0, Math.min(a.duration || 0, a.currentTime + delta));
  }, []);

  // Lock-screen / headset controls.
  useEffect(() => {
    if (!('mediaSession' in navigator) || !current) return;
    const ms = navigator.mediaSession;
    ms.metadata = new MediaMetadata({
      title: current.title,
      artist: book.author,
      album: book.title,
      artwork: book.hasCover ? [{ src: coverUrl(bookId) }] : [],
    });
    const handlers: [MediaSessionAction, MediaSessionActionHandler][] = [
      ['play', () => void audio.current?.play()],
      ['pause', () => audio.current?.pause()],
      ['seekbackward', () => seek(-15)],
      ['seekforward', () => seek(30)],
      ['previoustrack', () => goTo(narrated[index - 1], true)],
      ['nexttrack', () => goTo(narrated[index + 1], true)],
    ];
    for (const [action, handler] of handlers) {
      try {
        ms.setActionHandler(action, handler);
      } catch {
        // Unsupported action in this browser.
      }
    }
  }, [current, book, bookId, seek, goTo, narrated, index]);

  useEffect(() => {
    if (audio.current) audio.current.playbackRate = Number(rate);
    try {
      localStorage.setItem(RATE_KEY, rate);
    } catch {
      // Storage unavailable (private mode): the rate only lasts this visit.
    }
  }, [rate, ready]);

  if (!current) {
    return <Alert title="Nothing to play">This book has no narrated chapters.</Alert>;
  }

  return (
    <Stack maw={900}>
      <Breadcrumbs>
        <AnchorLink to="/library" size="sm">
          Library
        </AnchorLink>
        <AnchorLink to="/books/$bookId" params={{ bookId: String(bookId) }} size="sm">
          {book.title}
        </AnchorLink>
        <Text size="sm">Listen</Text>
      </Breadcrumbs>

      <Card withBorder padding="lg">
        <Stack gap="sm">
          <Group justify="space-between" wrap="nowrap">
            <Box style={{ minWidth: 0 }}>
              <Text size="sm" c="dimmed">
                Chapter {index + 1} of {narrated.length}
              </Text>
              <Title order={3} lineClamp={2}>
                {current.title}
              </Title>
            </Box>
            <AudioBadge chapter={current} />
          </Group>

          {ready ? (
            <audio
              key={current.id}
              ref={audio}
              src={audioUrl(current.id)}
              preload="metadata"
              onLoadedMetadata={(e) => {
                const a = e.currentTarget;
                a.playbackRate = Number(rate);
                setDuration(a.duration * 1000);
                if (resumeAt.current !== null) {
                  a.currentTime = resumeAt.current / 1000;
                  resumeAt.current = null;
                }
                if (autoplay.current) {
                  autoplay.current = false;
                  void a.play();
                }
              }}
              onTimeUpdate={(e) => {
                const ms = e.currentTarget.currentTime * 1000;
                setPosition(ms);
                if (Date.now() - lastSaved.current > SAVE_EVERY_MS) persist(current.id, ms);
              }}
              onPlay={() => setPlaying(true)}
              onPause={(e) => {
                setPlaying(false);
                persist(current.id, e.currentTarget.currentTime * 1000);
              }}
              onEnded={() => goTo(narrated[index + 1], true)}
            />
          ) : (
            <Alert color={current.audioStatus === 'failed' ? 'red' : 'blue'}>
              <Group justify="space-between">
                <Text size="sm">
                  {current.audioStatus === 'failed'
                    ? (current.audioError ?? 'Rendering failed.')
                    : current.audioStatus === 'none'
                      ? 'This chapter has no audio yet.'
                      : 'This chapter is being prepared; it starts as soon as it is ready.'}
                </Text>
                {(current.audioStatus === 'none' || current.audioStatus === 'failed') && (
                  <Button
                    size="xs"
                    onClick={() => render.mutate(current.id)}
                    loading={render.isPending}
                  >
                    Render now
                  </Button>
                )}
              </Group>
            </Alert>
          )}

          <Slider
            value={duration ? (position / duration) * 100 : 0}
            onChange={(v) => {
              if (audio.current && duration)
                audio.current.currentTime = ((v / 100) * duration) / 1000;
            }}
            label={null}
            disabled={!ready || !duration}
            aria-label="Position"
          />
          <Group justify="space-between">
            <Text size="xs" c="dimmed">
              {formatDuration(position)}
            </Text>
            <Text size="xs" c="dimmed">
              {formatDuration(duration || (current.audioDurationMs ?? 0))}
            </Text>
          </Group>

          <Group justify="center" gap="md">
            <ActionIcon
              variant="subtle"
              size="lg"
              disabled={index <= 0}
              onClick={() => goTo(narrated[index - 1], playing)}
              aria-label="Previous chapter"
            >
              <IconPlayerSkipBack />
            </ActionIcon>
            <ActionIcon
              variant="subtle"
              size="lg"
              disabled={!ready}
              onClick={() => seek(-15)}
              aria-label="Back 15 seconds"
            >
              <IconRewindBackward15 />
            </ActionIcon>
            <ActionIcon
              size={56}
              radius="xl"
              disabled={!ready}
              onClick={togglePlay}
              aria-label={playing ? 'Pause' : 'Play'}
            >
              {playing ? <IconPlayerPauseFilled size={28} /> : <IconPlayerPlayFilled size={28} />}
            </ActionIcon>
            <ActionIcon
              variant="subtle"
              size="lg"
              disabled={!ready}
              onClick={() => seek(30)}
              aria-label="Forward 30 seconds"
            >
              <IconRewindForward30 />
            </ActionIcon>
            <ActionIcon
              variant="subtle"
              size="lg"
              disabled={index >= narrated.length - 1}
              onClick={() => goTo(narrated[index + 1], playing)}
              aria-label="Next chapter"
            >
              <IconPlayerSkipForward />
            </ActionIcon>
          </Group>
          <Group justify="center">
            <Select
              size="xs"
              w={110}
              data={RATES.map((r) => ({ value: r, label: `${r}×` }))}
              value={rate}
              onChange={(v) => v && setRate(v)}
              aria-label="Playback speed"
              allowDeselect={false}
            />
          </Group>
        </Stack>
      </Card>

      <Card withBorder padding={0}>
        <ScrollArea h={360}>
          {narrated.map((c, i) => (
            <UnstyledButton
              key={c.id}
              onClick={() => goTo(c, true)}
              w="100%"
              px="md"
              py={8}
              bg={c.id === current.id ? 'var(--mantine-color-default-hover)' : undefined}
              style={{ borderBottom: '1px solid var(--mantine-color-default-border)' }}
            >
              <Group justify="space-between" wrap="nowrap">
                <Text size="sm" fw={c.id === current.id ? 600 : 400} truncate>
                  {i + 1}. {c.title}
                </Text>
                <AudioBadge chapter={c} />
              </Group>
            </UnstyledButton>
          ))}
        </ScrollArea>
      </Card>
    </Stack>
  );
}
