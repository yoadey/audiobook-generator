import { Alert, Box, Button, Center, Divider, Loader, Stack, Text, Title } from '@mantine/core';
import { IconArrowNarrowRight, IconFocus2 } from '@tabler/icons-react';
import { useQuery } from '@tanstack/react-query';
import { memo, useCallback, useEffect, useMemo, useRef, useState } from 'react';

import { type Chapter, chapterQuery, type Cue, type Paragraph, type Segment } from '../api/client';
import { cueOf, pieces, segmentAt, sentenceAt, startOf } from '../lib/reading';

/** Reading preferences (kept per browser). */
export type ReaderPrefs = { size: 'S' | 'M' | 'L' | 'XL'; font: 'serif' | 'sans' };

const FONT_SIZE: Record<ReaderPrefs['size'], string> = {
  S: '1rem',
  M: '1.125rem',
  L: '1.3rem',
  XL: '1.5rem',
};
const SERIF = 'ui-serif, "Iowan Old Style", "Palatino Linotype", Georgia, Cambria, serif';
/** How long scrolling must rest before it counts as the reading position. */
const READ_DELAY_MS = 700;
/** Where the reading position is looked for: below the fixed page header. */
const TOP_OFFSET = 72;

/**
 * The chapter's text to read along, in the page's own scrolling: the
 * sentence being spoken is marked and kept in view while the audio plays.
 * Scrolling while it is paused moves the position to the first segment in
 * view, so listening continues where reading stopped; clicking a passage
 * jumps there.
 */
export function Reader({
  chapter,
  cues,
  positionMs,
  playing,
  startSegment,
  onSeek,
  onRead,
  next,
  onNext,
  settled,
  prefs,
}: {
  chapter: Chapter;
  /** When each segment is spoken (absent without audio). */
  cues?: Cue[];
  /** The timings are loaded, or there are none to wait for. */
  settled: boolean;
  positionMs: number;
  playing: boolean;
  /** Where reading stopped last time (shown first). */
  startSegment?: number;
  onSeek: (ms: number) => void;
  /** The reader moved to a segment while the audio is paused. */
  onRead: (segmentId: number) => void;
  next?: Chapter;
  onNext: () => void;
  prefs: ReaderPrefs;
}) {
  const text = useQuery(chapterQuery(chapter.id));
  const root = useRef<HTMLDivElement>(null);
  const [follow, setFollow] = useState(true);
  // Without audio, the segment the reader marked last.
  const [marked, setMarked] = useState<number | undefined>(startSegment);
  // Scroll events until then come from a scroll this component started.
  const ownScroll = useRef(false);
  // When the reader's last own scroll started.
  const ownStart = useRef(0);
  const ownTimer = useRef<ReturnType<typeof setTimeout>>(undefined);
  const readTimer = useRef<ReturnType<typeof setTimeout>>(undefined);
  const placed = useRef<number | null>(null);
  // The latest values for the window's scroll handler and the paragraphs'
  // click handler, which stay the same while the position moves.
  const latest = useRef({ onSeek, onRead, playing });
  useEffect(() => {
    latest.current = { onSeek, onRead, playing };
  }, [onSeek, onRead, playing]);

  const active = cues ? segmentAt(cues, positionMs) : marked;

  // Playing again follows the voice.
  const [wasPlaying, setWasPlaying] = useState(playing);
  if (playing !== wasPlaying) {
    setWasPlaying(playing);
    if (playing) setFollow(true);
  }

  const segments = useMemo(() => {
    const byId = new Map<number, { s: Segment; p: number }>();
    const order: number[] = [];
    for (const p of text.data?.paragraphs ?? []) {
      for (const s of p.segments) {
        byId.set(s.id, { s, p: p.id });
        order.push(s.id);
      }
    }
    return { byId, order };
  }, [text.data]);
  const activeParagraph = active !== undefined ? segments.byId.get(active)?.p : undefined;

  // The sentence spoken now (by its share of the segment's time); the whole
  // segment when that cannot be told.
  const activeSeg = active !== undefined ? segments.byId.get(active)?.s : undefined;
  const cue = cues && active !== undefined ? cueOf(cues, active) : undefined;
  const sentence = activeSeg && cue ? sentenceAt(activeSeg.text, cue, positionMs) : undefined;
  const mark: [number, number] | undefined = activeSeg
    ? (sentence ?? [0, Array.from(activeSeg.text).length])
    : undefined;

  // A segment that is not spoken (only punctuation) stands for the next
  // one that is, so a position always has a time in the audio.
  const spoken = useCallback(
    (segmentId: number) => {
      if (!cues) return segmentId;
      const timed = new Set(cues.map((c) => c.segmentId));
      const { order } = segments;
      const i = order.indexOf(segmentId);
      if (i < 0) return segmentId;
      const after = order.slice(i).find((id) => timed.has(id));
      return (
        after ??
        order
          .slice(0, i)
          .reverse()
          .find((id) => timed.has(id)) ??
        segmentId
      );
    },
    [cues, segments],
  );

  const scrollTo = useCallback((segmentId: number, smooth: boolean) => {
    const el = root.current?.querySelector<HTMLElement>(`[data-seg="${segmentId}"]`);
    if (!el) return false;
    const top = el.getBoundingClientRect().top + window.scrollY - window.innerHeight / 3;
    ownScroll.current = true;
    ownStart.current = Date.now();
    clearTimeout(ownTimer.current);
    ownTimer.current = setTimeout(() => (ownScroll.current = false), smooth ? 1200 : 150);
    window.scrollTo?.({ top: Math.max(0, top), behavior: smooth ? 'smooth' : 'auto' });
    return true;
  }, []);

  // A new chapter starts at the top, or where reading stopped.
  useEffect(() => {
    if (!text.data || !settled || placed.current === chapter.id) return;
    // Where reading stopped wins: the audio's position may not be
    // restored yet (it still says 0, the chapter's heading).
    const target = startSegment ?? active;
    if (target === undefined || !scrollTo(target, false)) {
      ownScroll.current = true;
      ownStart.current = Date.now();
      window.scrollTo?.({ top: 0 });
      ownTimer.current = setTimeout(() => (ownScroll.current = false), 150);
    }
    placed.current = chapter.id;
  }, [text.data, settled, chapter.id, startSegment, active, scrollTo]);

  // Keep the spoken segment in view.
  useEffect(() => {
    if (playing && follow && active !== undefined) scrollTo(active, true);
  }, [playing, follow, active, scrollTo]);

  // The user scrolls: while playing, stop following; while paused, the
  // first segment in view becomes the position once scrolling rests. A
  // scroll counts as the user's after their own input (wheel, touch,
  // scroll keys, a press on the scrollbar) made after our scroll began,
  // also while ours is still running. Clicks and space (play) are no
  // scrolling: the scroll they cause is ours.
  useEffect(() => {
    let input = 0;
    const touched = () => (input = Date.now());
    const keyed = (e: KeyboardEvent) => {
      if (['ArrowUp', 'ArrowDown', 'PageUp', 'PageDown', 'Home', 'End'].includes(e.key)) touched();
    };
    // A press on the page's scrollbar targets the root element.
    const pressed = (e: MouseEvent) => {
      if (e.target === document.documentElement) touched();
    };
    const done = () => (ownScroll.current = false);
    const onScroll = () => {
      if (input > ownStart.current && Date.now() - input < 1000) {
        ownScroll.current = false;
        clearTimeout(ownTimer.current);
      }
      if (ownScroll.current) return;
      if (latest.current.playing) {
        setFollow(false);
        return;
      }
      clearTimeout(readTimer.current);
      readTimer.current = setTimeout(() => {
        for (const el of root.current?.querySelectorAll<HTMLElement>('[data-seg]') ?? []) {
          if (el.getBoundingClientRect().bottom > TOP_OFFSET) {
            const id = spoken(Number(el.dataset.seg));
            setMarked(id);
            latest.current.onRead(id);
            return;
          }
        }
      }, READ_DELAY_MS);
    };
    const inputs = ['wheel', 'touchmove'] as const;
    for (const ev of inputs) window.addEventListener(ev, touched, { passive: true });
    window.addEventListener('mousedown', pressed);
    window.addEventListener('keydown', keyed);
    window.addEventListener('scroll', onScroll, { passive: true });
    window.addEventListener('scrollend', done);
    return () => {
      for (const ev of inputs) window.removeEventListener(ev, touched);
      window.removeEventListener('mousedown', pressed);
      window.removeEventListener('keydown', keyed);
      window.removeEventListener('scroll', onScroll);
      window.removeEventListener('scrollend', done);
      clearTimeout(readTimer.current);
      clearTimeout(ownTimer.current);
    };
  }, [spoken]);

  const select = useCallback(
    (clicked: number) => {
      const segmentId = spoken(clicked);
      const start = cues ? startOf(cues, segmentId) : undefined;
      setFollow(true);
      if (start !== undefined) {
        latest.current.onSeek(start);
      } else {
        setMarked(segmentId);
        latest.current.onRead(segmentId);
      }
    },
    [cues, spoken],
  );

  if (text.isPending) {
    return (
      <Center py="xl">
        <Loader />
      </Center>
    );
  }
  if (text.error) {
    return (
      <Alert color="red" title="Could not load the text">
        {text.error.message}
      </Alert>
    );
  }

  const paragraphs = text.data.paragraphs;
  const titled = paragraphs[0]?.kind === 'heading';
  return (
    <Box
      ref={root}
      maw="38em"
      mx="auto"
      w="100%"
      px={{ base: 'xs', sm: 'md' }}
      style={{
        fontFamily: prefs.font === 'serif' ? SERIF : undefined,
        fontSize: FONT_SIZE[prefs.size],
        lineHeight: 1.75,
      }}
      data-testid="reader"
      aria-label="Chapter text"
    >
      {!titled && (
        <Title order={2} ff="inherit" mb="lg" mt="md" fw={600}>
          {chapter.title}
        </Title>
      )}
      <Stack gap="0.9em">
        {paragraphs.map((p) => (
          <ParagraphView
            key={p.id}
            p={p}
            active={p.id === activeParagraph ? active : undefined}
            mark={p.id === activeParagraph ? mark : undefined}
            onSelect={select}
          />
        ))}
      </Stack>
      {next && (
        <Center py={48}>
          <Button
            variant="subtle"
            color="gray"
            rightSection={<IconArrowNarrowRight size={16} />}
            onClick={onNext}
          >
            {next.title}
          </Button>
        </Center>
      )}
      {playing && !follow && active !== undefined && (
        <Box
          pos="fixed"
          bottom={128}
          left="50%"
          style={{ transform: 'translateX(-50%)', zIndex: 3 }}
        >
          <Button
            size="xs"
            radius="xl"
            variant="default"
            leftSection={<IconFocus2 size={14} />}
            onClick={() => {
              setFollow(true);
              scrollTo(active, true);
            }}
          >
            Back to the voice
          </Button>
        </Box>
      )}
    </Box>
  );
}

const ParagraphView = memo(function ParagraphView({
  p,
  active,
  mark,
  onSelect,
}: {
  p: Paragraph;
  /** The marked segment, when it is in this paragraph. */
  active?: number;
  /** The marked characters of the active segment. */
  mark?: [number, number];
  onSelect: (segmentId: number) => void;
}) {
  if (p.kind === 'scene_break') {
    return (
      <Divider
        my="md"
        label="⁂"
        labelPosition="center"
        color="var(--mantine-color-default-border)"
      />
    );
  }
  const spans = p.segments.map((s, i) => (
    <span key={s.id}>
      {i > 0 && ' '}
      <span
        data-seg={s.id}
        data-kind={s.kind}
        onClick={() => onSelect(s.id)}
        style={{
          cursor: 'pointer',
          fontStyle: s.kind === 'thought' ? 'italic' : undefined,
        }}
      >
        {pieces(s.text, s.emphasis, s.id === active ? mark : undefined).map((piece, k) => (
          <span
            key={k}
            style={{
              fontStyle: piece.italic ? 'italic' : undefined,
              background: piece.marked ? 'var(--mantine-color-yellow-light)' : undefined,
              borderRadius: piece.marked ? 3 : undefined,
              boxDecorationBreak: 'clone',
              WebkitBoxDecorationBreak: 'clone',
            }}
          >
            {piece.text}
          </span>
        ))}
      </span>
    </span>
  ));
  if (p.kind === 'heading') {
    return (
      <Title order={2} ff="inherit" fw={600} mt="md" mb="xs">
        {spans.length > 0 ? spans : p.text}
      </Title>
    );
  }
  return (
    <Text component="p" m={0} fz="inherit" lh="inherit" ff="inherit">
      {spans}
    </Text>
  );
});
