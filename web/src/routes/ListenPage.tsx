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
import {
  onlineManager,
  useIsMutating,
  useMutation,
  useQuery,
  useQueryClient,
} from '@tanstack/react-query';
import { useParams } from '@tanstack/react-router';
import {
  useCallback,
  useEffect,
  useLayoutEffect,
  useRef,
  useState,
  useSyncExternalStore,
} from 'react';

import {
  audioUrl,
  type Book,
  bookQuery,
  type Chapter,
  chaptersQuery,
  coverUrl,
  type Progress,
  progressQuery,
  progressSaveKey,
  renderChapter,
  saveProgress,
  timingsQuery,
} from '../api/client';
import { AudioBadge } from '../components/AudioBadge';
import { AnchorLink } from '../components/links';
import { Reader } from '../components/Reader';
import { ReaderSettings } from '../components/ReaderSettings';
import { formatDuration } from '../lib/format';
import { notifyError } from '../lib/notify';
import { resumeAt as resumePosition, segmentAt, startOf } from '../lib/reading';
import { typing } from '../lib/keys';
import { parsePrefs, type ReaderPrefs } from '../lib/readerPrefs';

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
  return parsePrefs(stored(PREFS_KEY, '{}'));
}

export function ListenPage() {
  const { bookId: raw } = useParams({ from: '/books/$bookId/listen' });
  const bookId = Number(raw);
  const book = useQuery(bookQuery(bookId));
  const chapters = useQuery(chaptersQuery(bookId));
  // Always the stored position (another device, or this one in an earlier
  // visit, may have moved it), before the player starts from it.
  // While a save is still on its way, its answer is the stored position.
  const saving = useIsMutating({ mutationKey: progressSaveKey(bookId) }) > 0;
  const progress = useQuery({
    ...progressQuery(bookId),
    enabled: !saving,
    refetchOnMount: 'always',
  });

  // Offline the refresh (or the save it waits for) waits for the network;
  // the cached position will do.
  const online = useSyncExternalStore(onlineManager.subscribe, () => onlineManager.isOnline());
  const refreshing =
    !progress.isFetchedAfterMount && progress.fetchStatus !== 'paused' && (online || !saving);
  if (book.isPending || chapters.isPending || progress.isPending || refreshing) {
    return (
      <Center py="xl">
        <Loader />
      </Center>
    );
  }
  // A failed refresh still has the position from before.
  const stored = progress.data;
  if (book.error || chapters.error || !stored) {
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
  return <Player book={book.data} narrated={narrated} progress={stored} />;
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
  const [settingsOpen, setSettingsOpen] = useState(false);
  // The playing chapter's entry in the chapter list (opened at it).
  const currentEntry = useRef<HTMLButtonElement>(null);
  // The page's first chapter was left: its stored position is outdated.
  const [leftStart, setLeftStart] = useState(false);
  // Where reading stopped (the stored position, then the reader's).
  const [readSeg, setReadSeg] = useState<{ chapter: number; segment: number } | null>(
    progress.chapterId === startId && progress.segmentId !== undefined
      ? { chapter: startId, segment: progress.segmentId }
      : null,
  );
  const lastSaved = useRef(0);
  // Where playback got to since the last save (or a save that failed):
  // stored when the page is left.
  const playedAt = useRef<{ chapter: number; ms: number; segment?: number } | null>(null);
  const saves = useRef(0);
  const closedAt = useRef(0);

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
    mutationKey: progressSaveKey(bookId),
    // One after the other: the server keeps the last one it gets.
    scope: { id: `progress-${bookId}` },
    mutationFn: ({
      chapterId,
      ms,
      segmentId,
      keepalive,
      seq,
    }: {
      chapterId: number;
      ms: number;
      segmentId?: number;
      keepalive?: boolean;
      seq: number;
    }) =>
      // Still waiting when the tab was closed (and kept for going back):
      // the position stored then is newer.
      seq <= closedAt.current
        ? Promise.resolve(undefined)
        : saveProgress(bookId, chapterId, ms, segmentId, keepalive),
    onSuccess: (stored) => {
      // An earlier save must not undo the position of a later one.
      if (stored && queryClient.isMutating({ mutationKey: progressSaveKey(bookId) }) === 1) {
        queryClient.setQueryData(progressQuery(bookId).queryKey, stored);
      }
      return queryClient.invalidateQueries({ queryKey: ['books', bookId, 'chapters'] });
    },
    onError: (_err, { chapterId, ms, segmentId, seq }) => {
      // The latest one is stored again when the page is left, unless
      // playback moved on.
      if (seq === saves.current)
        playedAt.current ??= { chapter: chapterId, ms, segment: segmentId };
      return queryClient.invalidateQueries({ queryKey: progressQuery(bookId).queryKey });
    },
  });
  // Stores the position; the segment comes from the timings of the chapter
  // playing, or is given (reading without audio).
  // Leaving the page stores with keepalive; closing it stores right away,
  // as a save waiting for an earlier one would never be sent.
  const persist = useCallback(
    (chapterId: number, ms: number, segmentId?: number, leaving?: 'page' | 'tab') => {
      lastSaved.current = Date.now();
      playedAt.current = null;
      saves.current++;
      const seg =
        segmentId ??
        (chapterId === currentId && cuesRef.current ? segmentAt(cuesRef.current, ms) : undefined);
      // The page starts from the cache when it is opened again.
      queryClient.setQueryData(progressQuery(bookId).queryKey, (old) => ({
        ...old,
        chapterId,
        positionMs: Math.max(0, Math.round(ms)),
        segmentId: seg,
      }));
      if (leaving === 'tab') {
        closedAt.current = saves.current;
        // Failed (offline), and the page was kept: stored once it is back.
        void saveProgress(bookId, chapterId, ms, seg, true).catch(() =>
          saveMutate({ chapterId, ms, segmentId: seg, seq: ++saves.current }),
        );
      } else {
        saveMutate({ chapterId, ms, segmentId: seg, keepalive: !!leaving, seq: saves.current });
      }
    },
    [saveMutate, currentId, queryClient, bookId],
  );
  const persistRef = useRef(persist);
  const currentRef = useRef(currentId);
  useEffect(() => {
    persistRef.current = persist;
    currentRef.current = currentId;
  }, [persist, currentId]);

  // Leaving the page (or closing it) while listening stores where it was;
  // the player's own saves come only every few seconds.
  useEffect(() => {
    const flush = (leaving: 'page' | 'tab') => {
      const p = playedAt.current;
      // A chapter left behind was stored when it was left.
      if (p && p.chapter === currentRef.current) {
        persistRef.current(p.chapter, p.ms, p.segment, leaving);
      }
    };
    const hide = () => flush('tab');
    window.addEventListener('pagehide', hide);
    return () => {
      window.removeEventListener('pagehide', hide);
      flush('page');
    };
  }, []);

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

  // Keyboard: space plays and pauses, the arrows skip (or turn the pages).
  const paged = prefs.turning === 'pages';
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (typing(e) || e.metaKey || e.ctrlKey || e.altKey) return;
      if (e.key === ' ') {
        e.preventDefault();
        togglePlay();
      } else if (paged) {
        // The arrows turn the pages (the reader's own keys).
      } else if (e.key === 'ArrowLeft') {
        seek(-15);
      } else if (e.key === 'ArrowRight') {
        seek(30);
      }
    };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, [togglePlay, seek, paged]);

  useEffect(() => store(PREFS_KEY, JSON.stringify(prefs)), [prefs]);
  // The page colour covers the whole page while reading (before it shows).
  useLayoutEffect(() => {
    document.documentElement.dataset.paper = prefs.paper;
    return () => {
      delete document.documentElement.dataset.paper;
    };
  }, [prefs.paper]);
  // Turning pages: a sideways swipe is a turn, not the browser's back.
  useLayoutEffect(() => {
    document.documentElement.dataset.turning = prefs.turning;
    return () => {
      delete document.documentElement.dataset.turning;
    };
  }, [prefs.turning]);

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
    // Turning pages: the page is the screen, the text never scrolls it.
    <Stack gap={0} {...(paged ? { h: 'calc(100dvh - 92px)' } : { mih: 'calc(100dvh - 92px)' })}>
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
            if (!e.currentTarget.paused) playedAt.current = { chapter: current.id, ms };
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

      <Box style={{ flex: 1, minHeight: 0 }}>
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
              <ActionIcon
                variant="subtle"
                color="gray"
                onClick={() => setSettingsOpen(true)}
                aria-label="Reading settings"
              >
                <IconLetterCase size={18} />
              </ActionIcon>
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

      <ReaderSettings
        opened={settingsOpen}
        onClose={() => setSettingsOpen(false)}
        prefs={prefs}
        onChange={setPrefs}
      />

      <Drawer
        opened={chaptersOpen}
        onClose={() => setChaptersOpen(false)}
        position="right"
        title="Chapters"
        size="sm"
        closeButtonProps={{ 'aria-label': 'Close' }}
        onEnterTransitionEnd={() => currentEntry.current?.scrollIntoView({ block: 'center' })}
      >
        <Stack gap={0}>
          {narrated.map((c, i) => (
            <UnstyledButton
              key={c.id}
              onClick={() => {
                setChaptersOpen(false);
                // The playing chapter goes on where it is.
                if (c.id !== current.id) goTo(c, playing);
              }}
              px="xs"
              py={8}
              bg={c.id === current.id ? 'var(--mantine-color-default-hover)' : undefined}
              style={{ borderRadius: 'var(--mantine-radius-sm)' }}
              aria-current={c.id === current.id ? 'true' : undefined}
              // Selected when the list opens.
              ref={c.id === current.id ? currentEntry : undefined}
              data-autofocus={c.id === current.id ? true : undefined}
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
