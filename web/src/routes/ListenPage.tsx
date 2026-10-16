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
  Tabs,
  Text,
  Title,
  UnstyledButton,
} from '@mantine/core';
import {
  IconBook,
  IconList,
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
  timingsQuery,
} from '../api/client';
import { AudioBadge } from '../components/AudioBadge';
import { AnchorLink } from '../components/links';
import { Reader } from '../components/Reader';
import { formatDuration } from '../lib/format';
import { notifyError } from '../lib/notify';
import { resumeAt as resumePosition, segmentAt, startOf } from '../lib/reading';

const SAVE_EVERY_MS = 15_000;
const RATES = ['0.8', '0.9', '1', '1.1', '1.25', '1.5', '1.75', '2'];
const RATE_KEY = 'abg.playbackRate';
const TAB_KEY = 'abg.listenTab';

function stored(key: string, fallback: string): string {
  try {
    return localStorage.getItem(key) ?? fallback;
  } catch {
    return fallback;
  }
}

function store(key: string, value: string) {
  try {
    localStorage.setItem(key, value);
  } catch {
    // Storage unavailable (private mode): the choice only lasts this visit.
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
  // Where reading stopped: listening resumes there once the timings are known.
  const resumeSegment = useRef<number | null>(
    progress.chapterId === startId ? (progress.segmentId ?? null) : null,
  );
  const autoplay = useRef(false);

  const index = narrated.findIndex((c) => c.id === currentId);
  const current: Chapter | undefined = narrated[index];
  const ready = current?.audioStatus === 'ready';

  const audio = useRef<HTMLAudioElement>(null);
  const [playing, setPlaying] = useState(false);
  const [position, setPosition] = useState(0);
  const [duration, setDuration] = useState(0);
  // The render a playing chapter started with (see the <audio> src).
  const [pinned, setPinned] = useState<{ id: number; version?: string } | null>(null);
  const [rate, setRate] = useState(() => stored(RATE_KEY, '1'));
  const [tab, setTab] = useState(() => stored(TAB_KEY, 'text'));
  // The chapter the page opened at (where reading stopped is known for it).
  const [opened, setOpened] = useState<number | null>(startId);
  const lastSaved = useRef(0);

  // The render being played (pinned) and when its segments are spoken.
  const version = pinned?.id === current?.id ? pinned?.version : current?.audioVersion;
  const timings = useQuery({
    ...timingsQuery(currentId, version),
    enabled: ready,
  });
  const cues = ready ? timings.data?.cues : undefined;
  const cuesRef = useRef(cues);
  useEffect(() => {
    cuesRef.current = cues;
  }, [cues]);

  const save = useMutation({
    mutationFn: ({
      chapterId,
      ms,
      segmentId,
    }: {
      chapterId: number;
      ms: number;
      segmentId?: number;
    }) => saveProgress(bookId, chapterId, ms, segmentId),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ['books', bookId, 'chapters'] }),
  });
  // Stores the position; the segment comes from the timings of the chapter
  // playing, or is given (reading without audio).
  const persist = useCallback(
    (chapterId: number, ms: number, segmentId?: number) => {
      lastSaved.current = Date.now();
      const seg =
        segmentId ??
        (chapterId === currentId && cuesRef.current ? segmentAt(cuesRef.current, ms) : undefined);
      save.mutate({ chapterId, ms, segmentId: seg });
    },
    [save, currentId],
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
      persist(current.id, resumeAt.current ?? 0, resumeSegment.current ?? undefined);
    }
  }, [current, persist]);

  const goTo = useCallback(
    (c: Chapter | undefined, play: boolean) => {
      if (!c) return;
      if (current && audio.current) {
        persist(current.id, audio.current.currentTime * 1000);
      }
      resumeAt.current = null;
      resumeSegment.current = null;
      autoplay.current = play;
      setOpened(null);
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
    store(RATE_KEY, rate);
  }, [rate, ready]);

  // Resume where reading stopped once the audio and its timings are there.
  useEffect(() => {
    const a = audio.current;
    if (!cues || !a || !duration || resumeSegment.current === null) return;
    const ms = resumePosition(cues, a.currentTime * 1000, resumeSegment.current);
    resumeSegment.current = null;
    if (ms !== a.currentTime * 1000) {
      a.currentTime = ms / 1000;
      setPosition(ms);
    }
  }, [cues, duration]);

  // Clicking a passage: listen from there.
  const seekTo = useCallback(
    (ms: number) => {
      const a = audio.current;
      if (!a || !current) return;
      a.currentTime = ms / 1000;
      setPosition(ms);
      if (a.paused) persist(current.id, ms);
    },
    [current, persist],
  );
  // Reading while paused moves the listening position along.
  const readAt = useCallback(
    (segmentId: number) => {
      if (!current) return;
      const a = audio.current;
      const start = cuesRef.current ? startOf(cuesRef.current, segmentId) : undefined;
      if (a && start !== undefined) {
        if (!a.paused) return;
        a.currentTime = start / 1000;
        setPosition(start);
        persist(current.id, start, segmentId);
      } else {
        resumeSegment.current = segmentId;
        persist(current.id, 0, segmentId);
      }
    },
    [current, persist],
  );

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
              // Once playing, stay on that render: a re-render must not swap
              // the file under seeks and rebuffering.
              src={audioUrl(
                current.id,
                pinned?.id === current.id ? pinned.version : current.audioVersion,
              )}

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
              onPlay={() => {
                setPlaying(true);
                if (pinned?.id !== current.id) {
                  setPinned({ id: current.id, version: current.audioVersion });
                }
              }}
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

      <Tabs
        value={tab}
        onChange={(v) => {
          if (!v) return;
          setTab(v);
          store(TAB_KEY, v);
        }}
        keepMounted={false}
      >
        <Tabs.List>
          <Tabs.Tab value="text" leftSection={<IconBook size={16} />}>
            Read along
          </Tabs.Tab>
          <Tabs.Tab value="chapters" leftSection={<IconList size={16} />}>
            Chapters
          </Tabs.Tab>
        </Tabs.List>
        <Tabs.Panel value="text" pt="sm">
          <Card withBorder padding={0}>
            <Reader
              key={current.id}
              chapter={current}
              cues={cues}
              settled={!ready || !timings.isPending}
              positionMs={position}
              playing={playing}
              startSegment={
                current.id !== opened
                  ? undefined
                  : (progress.segmentId ??
                    (cues && progress.positionMs > 0
                      ? segmentAt(cues, progress.positionMs)
                      : undefined))
              }
              onSeek={seekTo}
              onRead={readAt}
              next={narrated[index + 1]}
              onNext={() => goTo(narrated[index + 1], playing)}
            />
          </Card>
        </Tabs.Panel>
        <Tabs.Panel value="chapters" pt="sm">
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
        </Tabs.Panel>
      </Tabs>
    </Stack>
  );
}
