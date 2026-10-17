import {
  ActionIcon,
  Alert,
  Anchor,
  Box,
  Breadcrumbs,
  Center,
  Drawer,
  Group,
  Loader,
  Menu,
  Popover,
  SegmentedControl,
  Slider,
  Stack,
  Text,
  UnstyledButton,
} from '@mantine/core';
import {
  IconLetterCase,
  IconListDetails,
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
import { Reader, type ReaderPrefs } from '../components/Reader';
import { formatDuration } from '../lib/format';
import { notifyError } from '../lib/notify';
import { resumeAt as resumePosition, segmentAt, startOf } from '../lib/reading';

const SAVE_EVERY_MS = 15_000;
const RATES = ['0.8', '0.9', '1', '1.1', '1.25', '1.5', '1.75', '2'];
const RATE_KEY = 'abg.playbackRate';
const PREFS_KEY = 'abg.readerPrefs';

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

function storedPrefs(): ReaderPrefs {
  try {
    const p = JSON.parse(stored(PREFS_KEY, '{}')) as Partial<ReaderPrefs>;
    return {
      size: p.size && ['S', 'M', 'L', 'XL'].includes(p.size) ? p.size : 'M',
      font: p.font === 'sans' ? 'sans' : 'serif',
    };
  } catch {
    return { size: 'M', font: 'serif' };
  }
}

/**
 * Whether a key press belongs to something else than the player: a form
 * field, the position slider (which handles arrows itself), or an open
 * menu, popover or drawer. A focused button of the player does not count:
 * space still plays and pauses after a click on "Forward 30".
 */
function typing(e: KeyboardEvent): boolean {
  const t = e.target as HTMLElement | null;
  return (
    e.defaultPrevented ||
    (!!t &&
      (/^(INPUT|TEXTAREA|SELECT)$/.test(t.tagName) ||
        t.isContentEditable ||
        t.getAttribute('role') === 'slider' ||
        !!t.closest('[role=dialog],[role=menu],[role=listbox]')))
  );
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
  // A chapter without audio plays nothing (its <audio> is gone).
  const [playingState, setPlaying] = useState(false);
  const playing = playingState && ready;
  const [position, setPosition] = useState(0);
  const [duration, setDuration] = useState(0);
  // The render a playing chapter started with (see the <audio> src).
  const [pinned, setPinned] = useState<{ id: number; version?: string } | null>(null);
  const [rate, setRate] = useState(() => stored(RATE_KEY, '1'));
  const [prefs, setPrefs] = useState<ReaderPrefs>(storedPrefs);
  const [chaptersOpen, setChaptersOpen] = useState(false);
  // The page's first chapter was left: its stored position is outdated.
  const [leftStart, setLeftStart] = useState(false);
  // Where reading stopped (the stored position, then the reader's).
  const [readSeg, setReadSeg] = useState<{ chapter: number; segment: number } | null>(
    progress.chapterId === startId && progress.segmentId !== undefined
      ? { chapter: startId, segment: progress.segmentId }
      : null,
  );
  const lastSaved = useRef(0);

  // The render being played (pinned) and when its segments are spoken.
  const version = pinned?.id === current?.id ? pinned?.version : current?.audioVersion;
  const timings = useQuery({
    ...timingsQuery(currentId, version),
    enabled: ready,
  });
  // Only the timings of the render being played (the server falls back to
  // the current one when that render is gone).
  const cues =
    ready && timings.data && (!version || timings.data.version === version)
      ? timings.data.cues
      : undefined;
  const cuesRef = useRef(cues);
  useEffect(() => {
    cuesRef.current = cues;
  }, [cues]);

  // With word times, the position follows the audio more finely than its
  // time updates (a few per second) so the marked word keeps pace.
  const wordTimed = !!cues?.some((c) => c.words?.length);
  useEffect(() => {
    if (!playing || !wordTimed) return;
    let frame = 0;
    let last = -1;
    const tick = () => {
      const a = audio.current;
      if (a) {
        const ms = a.currentTime * 1000;
        if (Math.abs(ms - last) >= 60) {
          last = ms;
          setPosition(ms);
        }
      }
      frame = requestAnimationFrame(tick);
    };
    frame = requestAnimationFrame(tick);
    return () => cancelAnimationFrame(frame);
  }, [playing, wordTimed]);

  const { mutate: saveMutate } = useMutation({
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
      saveMutate({ chapterId, ms, segmentId: seg });
    },
    [saveMutate, currentId],
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
      // The new chapter's <audio> reports when it plays; the old one's
      // pause may never arrive once it is gone.
      setPlaying(false);
      setReadSeg(null);
      setLeftStart(true);
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
      if (!a) return;
      a.currentTime = ms / 1000;
      setPosition(ms);
      if (a.paused) persist(currentId, ms);
    },
    [currentId, persist],
  );
  // Reading while paused moves the listening position along.
  const readAt = useCallback(
    (segmentId: number) => {
      const a = audio.current;
      if (a) {
        if (!a.paused) return;
        const start = cuesRef.current ? startOf(cuesRef.current, segmentId) : undefined;
        setReadSeg({ chapter: currentId, segment: segmentId });
        if (start === undefined) {
          // No timings (yet): keep the position, and move there once
          // they are known.
          resumeSegment.current = segmentId;
          persist(currentId, a.currentTime * 1000, segmentId);
          return;
        }
        a.currentTime = start / 1000;
        setPosition(start);
        persist(currentId, start, segmentId);
      } else {
        resumeSegment.current = segmentId;
        setReadSeg({ chapter: currentId, segment: segmentId });
        persist(currentId, 0, segmentId);
      }
    },
    [currentId, persist],
  );

  // Keyboard: space plays and pauses, the arrows skip.
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (typing(e) || e.metaKey || e.ctrlKey || e.altKey) return;
      if (e.key === ' ') {
        e.preventDefault();
        togglePlay();
      } else if (e.key === 'ArrowLeft') {
        seek(-15);
      } else if (e.key === 'ArrowRight') {
        seek(30);
      }
    };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, [togglePlay, seek]);

  useEffect(() => store(PREFS_KEY, JSON.stringify(prefs)), [prefs]);

  if (!current) {
    return <Alert title="Nothing to play">This book has no narrated chapters.</Alert>;
  }

  const status =
    current.audioStatus === 'failed'
      ? (current.audioError ?? 'Rendering failed.')
      : current.audioStatus === 'none'
        ? 'No audio yet; your reading position is kept.'
        : 'Preparing the audio; it starts as soon as it is ready.';
  const total = duration || (current.audioDurationMs ?? 0);

  return (
    <Stack gap={0} mih="calc(100dvh - 92px)">
      <Group justify="space-between" wrap="nowrap" mb="md">
        <Breadcrumbs separatorMargin={6} style={{ minWidth: 0 }}>
          <AnchorLink to="/library" size="sm" c="dimmed">
            Library
          </AnchorLink>
          <AnchorLink
            to="/books/$bookId"
            params={{ bookId: String(bookId) }}
            size="sm"
            c="dimmed"
            truncate
          >
            {book.title}
          </AnchorLink>
        </Breadcrumbs>
        <Text size="xs" c="dimmed" style={{ whiteSpace: 'nowrap' }}>
          {index + 1} / {narrated.length}
        </Text>
      </Group>

      {ready && (
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
      )}

      <Box style={{ flex: 1 }}>
        <Reader
          key={current.id}
          chapter={current}
          cues={cues}
          settled={!ready || !timings.isPending}
          positionMs={position}
          playing={playing}
          prefs={prefs}
          startSegment={
            readSeg?.chapter === current.id
              ? readSeg.segment
              : !leftStart && current.id === startId && cues && progress.positionMs > 0
                ? segmentAt(cues, progress.positionMs)
                : undefined
          }
          onSeek={seekTo}
          onRead={readAt}
          next={narrated[index + 1]}
          onNext={() => goTo(narrated[index + 1], playing)}
        />
      </Box>

      {/* The player: a quiet bar that stays at the bottom while reading. */}
      <Box
        pos="sticky"
        bottom={0}
        mt="xl"
        mx="calc(var(--mantine-spacing-md) * -1)"
        mb="calc(var(--mantine-spacing-md) * -1)"
        px="md"
        pt={6}
        pb={10}
        bg="var(--mantine-color-body)"
        style={{ borderTop: '1px solid var(--mantine-color-default-border)', zIndex: 2 }}
        data-testid="player"
      >
        <Stack gap={4} maw={760} mx="auto">
          {ready ? (
            <Group gap="xs" wrap="nowrap">
              <Text size="xs" c="dimmed" w={44} ta="right" ff="monospace">
                {formatDuration(position)}
              </Text>
              <Slider
                style={{ flex: 1 }}
                size="xs"
                value={total ? (position / total) * 100 : 0}
                onChange={(v) => {
                  if (audio.current && total)
                    audio.current.currentTime = ((v / 100) * total) / 1000;
                }}
                label={null}
                disabled={!duration}
                aria-label="Position"
              />
              <Text size="xs" c="dimmed" w={44} ff="monospace">
                {formatDuration(total)}
              </Text>
            </Group>
          ) : (
            <Group justify="center" gap="xs" mih={22}>
              <Text size="xs" c={current.audioStatus === 'failed' ? 'red' : 'dimmed'} lineClamp={1}>
                {status}
              </Text>
              {(current.audioStatus === 'none' || current.audioStatus === 'failed') && (
                <Anchor component="button" size="xs" onClick={() => render.mutate(current.id)}>
                  Render now
                </Anchor>
              )}
            </Group>
          )}
          <Group justify="space-between" wrap="nowrap">
            <Text size="sm" fw={500} truncate style={{ flex: 1, minWidth: 0 }} visibleFrom="sm">
              {current.title}
            </Text>
            <Group gap={4} wrap="nowrap" justify="center" style={{ flex: 'none' }}>
              <ActionIcon
                variant="subtle"
                color="gray"
                disabled={index <= 0}
                onClick={() => goTo(narrated[index - 1], playing)}
                aria-label="Previous chapter"
              >
                <IconPlayerSkipBack size={18} />
              </ActionIcon>
              <ActionIcon
                variant="subtle"
                color="gray"
                disabled={!ready}
                onClick={() => seek(-15)}
                aria-label="Back 15 seconds"
              >
                <IconRewindBackward15 size={20} />
              </ActionIcon>
              <ActionIcon
                size={44}
                radius="xl"
                disabled={!ready}
                onClick={togglePlay}
                aria-label={playing ? 'Pause' : 'Play'}
              >
                {playing ? <IconPlayerPauseFilled size={22} /> : <IconPlayerPlayFilled size={22} />}
              </ActionIcon>
              <ActionIcon
                variant="subtle"
                color="gray"
                disabled={!ready}
                onClick={() => seek(30)}
                aria-label="Forward 30 seconds"
              >
                <IconRewindForward30 size={20} />
              </ActionIcon>
              <ActionIcon
                variant="subtle"
                color="gray"
                disabled={index >= narrated.length - 1}
                onClick={() => goTo(narrated[index + 1], playing)}
                aria-label="Next chapter"
              >
                <IconPlayerSkipForward size={18} />
              </ActionIcon>
            </Group>
            <Group gap={2} wrap="nowrap" justify="flex-end" style={{ flex: 1 }}>
              <Menu position="top" withinPortal>
                <Menu.Target>
                  <UnstyledButton
                    aria-label="Playback speed"
                    px={6}
                    fz="sm"
                    c="dimmed"
                    ff="monospace"
                  >
                    {rate}×
                  </UnstyledButton>
                </Menu.Target>
                <Menu.Dropdown>
                  {RATES.map((r) => (
                    <Menu.Item key={r} onClick={() => setRate(r)} fw={r === rate ? 600 : undefined}>
                      {r}×
                    </Menu.Item>
                  ))}
                </Menu.Dropdown>
              </Menu>
              <Popover position="top-end" withinPortal shadow="md">
                <Popover.Target>
                  <ActionIcon variant="subtle" color="gray" aria-label="Text settings">
                    <IconLetterCase size={18} />
                  </ActionIcon>
                </Popover.Target>
                <Popover.Dropdown>
                  <Stack gap="xs">
                    <SegmentedControl
                      size="xs"
                      value={prefs.size}
                      onChange={(v) => setPrefs({ ...prefs, size: v as ReaderPrefs['size'] })}
                      data={['S', 'M', 'L', 'XL']}
                      aria-label="Text size"
                    />
                    <SegmentedControl
                      size="xs"
                      value={prefs.font}
                      onChange={(v) => setPrefs({ ...prefs, font: v as ReaderPrefs['font'] })}
                      data={[
                        { value: 'serif', label: 'Serif' },
                        { value: 'sans', label: 'Sans' },
                      ]}
                      aria-label="Font"
                    />
                  </Stack>
                </Popover.Dropdown>
              </Popover>
              <ActionIcon
                variant="subtle"
                color="gray"
                onClick={() => setChaptersOpen(true)}
                aria-label="Chapters"
              >
                <IconListDetails size={18} />
              </ActionIcon>
            </Group>
          </Group>
        </Stack>
      </Box>

      <Drawer
        opened={chaptersOpen}
        onClose={() => setChaptersOpen(false)}
        position="right"
        title="Chapters"
        size="sm"
      >
        <Stack gap={0}>
          {narrated.map((c, i) => (
            <UnstyledButton
              key={c.id}
              onClick={() => {
                setChaptersOpen(false);
                goTo(c, playing);
              }}
              px="xs"
              py={8}
              bg={c.id === current.id ? 'var(--mantine-color-default-hover)' : undefined}
              style={{ borderRadius: 'var(--mantine-radius-sm)' }}
              aria-current={c.id === current.id ? 'true' : undefined}
            >
              <Group justify="space-between" wrap="nowrap" gap="xs">
                <Text size="sm" fw={c.id === current.id ? 600 : 400} truncate>
                  <Text span c="dimmed" size="xs" mr={6}>
                    {i + 1}
                  </Text>
                  {c.title}
                </Text>
                <AudioBadge chapter={c} />
              </Group>
            </UnstyledButton>
          ))}
        </Stack>
      </Drawer>
    </Stack>
  );
}
