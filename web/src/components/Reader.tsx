import {
  Alert,
  Box,
  Button,
  Center,
  Divider,
  Group,
  Loader,
  ScrollArea,
  SegmentedControl,
  Stack,
  Text,
  Title,
} from '@mantine/core';
import { IconArrowDown, IconPlayerTrackNext } from '@tabler/icons-react';
import { useQuery } from '@tanstack/react-query';
import { memo, useCallback, useEffect, useMemo, useRef, useState } from 'react';

import { type Chapter, chapterQuery, type Cue, type Paragraph } from '../api/client';
import { segmentAt, startOf } from '../lib/reading';

const SIZE_KEY = 'abg.readerSize';
const SIZES = { S: 'sm', M: 'md', L: 'lg', XL: 'xl' } as const;
type Size = keyof typeof SIZES;
/** How long scrolling must rest before it counts as the reading position. */
const READ_DELAY_MS = 800;

function storedSize(): Size {
  try {
    const v = localStorage.getItem(SIZE_KEY);
    return v && v in SIZES ? (v as Size) : 'M';
  } catch {
    return 'M';
  }
}

/**
 * The chapter's text to read along: the segment being spoken is marked and
 * kept in view while the audio plays. Scrolling while it is paused moves
 * the position to the first segment in view, so listening continues where
 * reading stopped; clicking a segment jumps there.
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
  hasAudio,
}: {
  chapter: Chapter;
  /** When each segment is spoken (absent without audio). */
  cues?: Cue[];
  /** The timings are loaded, or there are none to wait for. */
  settled: boolean;
  hasAudio: boolean;
  positionMs: number;
  playing: boolean;
  /** Where reading stopped last time (shown first). */
  startSegment?: number;
  onSeek: (ms: number) => void;
  /** The reader moved to a segment while the audio is paused. */
  onRead: (segmentId: number) => void;
  next?: Chapter;
  onNext: () => void;
}) {
  const text = useQuery(chapterQuery(chapter.id));
  const viewport = useRef<HTMLDivElement>(null);
  const [size, setSize] = useState<Size>(storedSize);
  const [follow, setFollow] = useState(true);
  // Without audio, the segment the reader marked last.
  const [marked, setMarked] = useState<number | undefined>(startSegment);
  // The user (not code) is scrolling.
  const interacted = useRef(false);
  // Scroll events until then come from a scroll this component started.
  const ownScrollUntil = useRef(0);
  const readTimer = useRef<ReturnType<typeof setTimeout>>(undefined);
  const placed = useRef<number | null>(null);
  // The latest callbacks, so the paragraphs' click handler stays the same
  // while the position moves (memoised paragraphs re-render only when
  // their marked segment changes).
  const handlers = useRef({ onSeek, onRead });
  useEffect(() => {
    handlers.current = { onSeek, onRead };
  }, [onSeek, onRead]);

  const active = cues ? segmentAt(cues, positionMs) : marked;

  // Playing again follows the voice.
  const [wasPlaying, setWasPlaying] = useState(playing);
  if (playing !== wasPlaying) {
    setWasPlaying(playing);
    if (playing) setFollow(true);
  }

  // Which paragraph holds a segment: only that paragraph re-renders when
  // the spoken segment moves on.
  const paragraphOf = useMemo(() => {
    const m = new Map<number, number>();
    for (const p of text.data?.paragraphs ?? []) {
      for (const s of p.segments) m.set(s.id, p.id);
    }
    return m;
  }, [text.data]);
  const activeParagraph = active !== undefined ? paragraphOf.get(active) : undefined;

  // A segment that is not spoken (only punctuation) stands for the next
  // one that is, so a position always has a time in the audio.
  const spoken = useCallback(
    (segmentId: number) => {
      if (!cues) return segmentId;
      const timed = new Set(cues.map((c) => c.segmentId));
      const order = (text.data?.paragraphs ?? []).flatMap((p) => p.segments.map((s) => s.id));
      const i = order.indexOf(segmentId);
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
    [cues, text.data],
  );

  const scrollTo = useCallback((segmentId: number, smooth: boolean) => {
    const vp = viewport.current;
    const el = vp?.querySelector<HTMLElement>(`[data-seg="${segmentId}"]`);
    if (!vp || !el) return false;
    interacted.current = false;
    ownScrollUntil.current = Date.now() + (smooth ? 700 : 100);
    const top = el.getBoundingClientRect().top - vp.getBoundingClientRect().top + vp.scrollTop;
    vp.scrollTo?.({
      top: Math.max(0, top - vp.clientHeight / 3),
      behavior: smooth ? 'smooth' : 'auto',
    });
    return true;
  }, []);

  useEffect(() => {
    try {
      localStorage.setItem(SIZE_KEY, size);
    } catch {
      // Storage unavailable: the size only lasts this visit.
    }
  }, [size]);

  // A new chapter starts at the top, or where reading stopped.
  useEffect(() => {
    if (!text.data || !settled || placed.current === chapter.id) return;
    const target = active ?? startSegment;
    if (target !== undefined ? scrollTo(target, false) : true) {
      if (target === undefined) viewport.current?.scrollTo?.({ top: 0 });
      placed.current = chapter.id;
    }
  }, [text.data, settled, chapter.id, startSegment, active, scrollTo]);

  // Keep the spoken segment in view.
  useEffect(() => {
    if (playing && follow && active !== undefined) scrollTo(active, true);
  }, [playing, follow, active, scrollTo]);

  useEffect(() => () => clearTimeout(readTimer.current), []);

  const onScroll = () => {
    if (!interacted.current || Date.now() < ownScrollUntil.current) return;
    if (playing) {
      setFollow(false);
      return;
    }
    clearTimeout(readTimer.current);
    readTimer.current = setTimeout(() => {
      const vp = viewport.current;
      if (!vp) return;
      const top = vp.getBoundingClientRect().top;
      for (const el of vp.querySelectorAll<HTMLElement>('[data-seg]')) {
        if (el.getBoundingClientRect().bottom > top + 4) {
          const id = spoken(Number(el.dataset.seg));
          setMarked(id);
          handlers.current.onRead(id);
          return;
        }
      }
    }, READ_DELAY_MS);
  };
  const userScrolls = () => {
    interacted.current = true;
  };

  const select = useCallback(
    (clicked: number) => {
      const segmentId = spoken(clicked);
      const start = cues ? startOf(cues, segmentId) : undefined;
      setFollow(true);
      if (start !== undefined) {
        handlers.current.onSeek(start);
      } else {
        setMarked(segmentId);
        handlers.current.onRead(segmentId);
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

  return (
    <Stack gap={0} pos="relative">
      <Group justify="space-between" px="md" py="xs">
        <Text size="xs" c="dimmed">
          {hasAudio
            ? 'Click a passage to listen from there; scrolling while paused moves the position.'
            : 'No audio yet; your reading position is kept.'}
        </Text>
        <SegmentedControl
          size="xs"
          value={size}
          onChange={(v) => setSize(v as Size)}
          data={Object.keys(SIZES)}
          aria-label="Text size"
        />
      </Group>
      <Divider />
      <ScrollArea
        h="60vh"
        viewportRef={viewport}
        onScrollPositionChange={onScroll}
        // On the root: also the scrollbar (beside the viewport) counts.
        onWheel={userScrolls}
        onTouchMove={userScrolls}
        onKeyDown={userScrolls}
        onPointerDown={userScrolls}
        viewportProps={{ tabIndex: 0, 'aria-label': 'Chapter text' }}
      >
        <Stack gap="sm" px="lg" py="md" maw="42em" mx="auto">
          {text.data.paragraphs.map((p) => (
            <ParagraphView
              key={p.id}
              p={p}
              size={SIZES[size]}
              active={p.id === activeParagraph ? active : undefined}
              onSelect={select}
            />
          ))}
          {next && (
            <Center py="lg">
              <Button
                variant="light"
                leftSection={<IconPlayerTrackNext size={16} />}
                onClick={onNext}
              >
                Next: {next.title}
              </Button>
            </Center>
          )}
        </Stack>
      </ScrollArea>
      {playing && !follow && active !== undefined && (
        <Box pos="absolute" bottom={12} right={16}>
          <Button
            size="xs"
            leftSection={<IconArrowDown size={14} />}
            onClick={() => {
              setFollow(true);
              scrollTo(active, true);
            }}
          >
            Follow the voice
          </Button>
        </Box>
      )}
    </Stack>
  );
}

const ParagraphView = memo(function ParagraphView({
  p,
  size,
  active,
  onSelect,
}: {
  p: Paragraph;
  size: string;
  /** The marked segment, when it is in this paragraph. */
  active?: number;
  onSelect: (segmentId: number) => void;
}) {
  if (p.kind === 'scene_break') {
    return <Divider my="sm" label="⁂" labelPosition="center" />;
  }
  const spans = p.segments.map((s, i) => (
    <span key={s.id}>
      {i > 0 && ' '}
      <span
        data-seg={s.id}
        onClick={() => onSelect(s.id)}
        style={{
          cursor: 'pointer',
          borderRadius: 3,
          background: s.id === active ? 'var(--mantine-color-yellow-light)' : undefined,
          boxShadow: s.id === active ? '0 0 0 2px var(--mantine-color-yellow-light)' : undefined,
        }}
      >
        {s.text}
      </span>
    </span>
  ));
  if (p.kind === 'heading') {
    return (
      <Title order={3} mt="sm">
        {spans.length > 0 ? spans : p.text}
      </Title>
    );
  }
  return (
    <Text size={size} style={{ lineHeight: 1.75 }}>
      {spans}
    </Text>
  );
});
