import {
  ActionIcon,
  Alert,
  Box,
  Button,
  Center,
  Divider,
  Group,
  Loader,
  Text,
  Title,
} from '@mantine/core';
import {
  IconArrowNarrowRight,
  IconChevronLeft,
  IconChevronRight,
  IconFocus2,
} from '@tabler/icons-react';
import { useQuery } from '@tanstack/react-query';
import { memo, useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState } from 'react';

import { type Chapter, chapterQuery, type Cue, type Paragraph, type Segment } from '../api/client';
import { typing } from '../lib/keys';
import { cueOf, pieces, segmentAt, sentenceAt, startOf, wordAt } from '../lib/reading';
import { FONTS, type ReaderPrefs, WIDTHS } from '../lib/readerPrefs';

/** How long scrolling must rest before it counts as the reading position. */
const READ_DELAY_MS = 700;
/** Where the reading position is looked for: below the fixed page header. */
const TOP_OFFSET = 72;

/** A pause in wheel events that ends a gesture (a trackpad's momentum
 * keeps sending them): one turn per gesture. */
const WHEEL_GAP_MS = 200;
/** How far a swipe must go to turn the page (px). */
const SWIPE_PX = 50;

/**
 * The chapter's text to read along, in the page's own scrolling or in
 * pages to turn: the sentence being spoken is marked and kept in view
 * while the audio plays. Scrolling (or turning a page) while it is paused
 * moves the position to the first segment in view, so listening continues
 * where reading stopped; clicking a passage jumps there.
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
  const paged = prefs.turning === 'pages';
  // Pages: the window the text shows through, the text in columns of a
  // page each, the page shown and the size of a page.
  const viewport = useRef<HTMLDivElement>(null);
  const content = useRef<HTMLDivElement>(null);
  const [view, setView] = useState({ page: 0, smooth: false });
  const [pageCount, setPageCount] = useState(1);
  const [box, setBox] = useState({ w: 0, h: 0 });
  const [follow, setFollow] = useState(true);
  // Without audio, the segment the reader marked last.
  const [marked, setMarked] = useState<number | undefined>(startSegment);
  // Where reading is while paused (placed, turned, scrolled or clicked to):
  // what stays in view when the text flows anew.
  const readAt = useRef<number | undefined>(startSegment);
  const anchor = () =>
    latest.current.playing ? latest.current.active : (readAt.current ?? latest.current.active);
  // Scroll events until then come from a scroll this component started.
  const ownScroll = useRef(false);
  // When the reader's last own scroll started.
  const ownStart = useRef(0);
  const ownTimer = useRef<ReturnType<typeof setTimeout>>(undefined);
  const readTimer = useRef<ReturnType<typeof setTimeout>>(undefined);
  const placed = useRef<number | null>(null);
  // The latest values for the window's scroll handler and the paragraphs'
  // click handler, which stay the same while the position moves.
  const active = cues ? segmentAt(cues, positionMs) : marked;
  const latest = useRef({ onSeek, onRead, playing, paged, active });
  useEffect(() => {
    latest.current = { onSeek, onRead, playing, paged, active };
  }, [onSeek, onRead, playing, paged, active]);

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

  // The sentence spoken now (by its words' times, or by its share of the
  // segment's time); the whole segment when that cannot be told. The word
  // spoken now when the render has word times.
  const activeSeg = active !== undefined ? segments.byId.get(active)?.s : undefined;
  const cue = cues && active !== undefined ? cueOf(cues, active) : undefined;
  const sentence = activeSeg && cue ? sentenceAt(activeSeg.text, cue, positionMs) : undefined;
  // What is marked: while playing as chosen; while paused, the position
  // when it is to be shown.
  const marking = playing ? prefs.highlight !== 'off' : prefs.showPosition;
  const mark: [number, number] | undefined =
    activeSeg && marking ? (sentence ?? [0, Array.from(activeSeg.text).length]) : undefined;
  const word =
    activeSeg && cue && playing && prefs.highlight === 'word'
      ? wordAt(activeSeg.text, cue, positionMs)
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

  // The page an element is on: its offset in the columns (both move by
  // the same turn, so the turn itself does not count).
  const pageOf = useCallback(
    (el: Element, last = false) => {
      const c = content.current;
      if (!c || !box.w) return 0;
      const rects = el.getClientRects();
      const r = rects[last ? rects.length - 1 : 0] ?? el.getBoundingClientRect();
      return Math.max(0, Math.floor((r.left - c.getBoundingClientRect().left) / box.w));
    },
    [box.w],
  );
  // The first segment shown on a page (one going on from the page before
  // counts, as it does when scrolling).
  const firstOn = useCallback(
    (page: number) => {
      for (const el of content.current?.querySelectorAll<HTMLElement>('[data-seg]') ?? []) {
        if (pageOf(el, true) >= page) return Number(el.dataset.seg);
      }
      return undefined;
    },
    [pageOf],
  );
  const showPage = useCallback((page: number, smooth: boolean) => {
    setView((v) => (v.page === page ? v : { page, smooth }));
  }, []);
  const pageNow = useRef(0);
  useEffect(() => {
    pageNow.current = view.page;
  }, [view.page]);

  // Brings a segment into view: scrolls to it, or turns to its page. A
  // segment running over a page break: the page of the word spoken (or
  // the sentence marked), and no turn while that is on the page shown.
  const show = useCallback(
    (segmentId: number, smooth: boolean) => {
      if (!paged) return scrollTo(segmentId, smooth);
      const el = content.current?.querySelector(`[data-seg="${segmentId}"]`);
      if (!el || !box.w) return false;
      const spot = el.querySelector('[data-word]') ?? el.querySelector('[data-mark]') ?? el;
      const first = pageOf(spot);
      const now = pageNow.current;
      showPage(now >= first && now <= pageOf(spot, true) ? now : first, smooth);
      return true;
    },
    [paged, scrollTo, box.w, pageOf, showPage],
  );

  // The size of a page, and how many pages the chapter takes.
  useLayoutEffect(() => {
    const vp = viewport.current;
    if (!paged || !vp) return;
    // Its exact width (zoom makes it fractional): the columns' pitch.
    const measure = () => {
      const r = vp.getBoundingClientRect();
      setBox({ w: r.width, h: r.height });
    };
    measure();
    if (typeof ResizeObserver === 'undefined') return;
    const ro = new ResizeObserver(measure);
    ro.observe(vp);
    return () => ro.disconnect();
  }, [paged, text.data]);
  const count = useCallback(() => {
    const c = content.current;
    if (!c || !box.w) return;
    const n = Math.max(1, Math.ceil((c.scrollWidth - 1) / box.w));
    setPageCount(n);
    setView((v) => (v.page < n ? v : { page: n - 1, smooth: false }));
  }, [box.w]);
  // A font arriving late (or a face first used) flows the text anew: the
  // position stays in view.
  useEffect(() => {
    const fonts = document.fonts;
    if (!paged || !fonts) return;
    let live = true;
    const reflow = () => {
      if (!live) return;
      count();
      const a = anchor();
      if (a !== undefined) show(a, false);
    };
    fonts.addEventListener('loadingdone', reflow);
    void fonts.ready.then(reflow);
    return () => {
      live = false;
      fonts.removeEventListener('loadingdone', reflow);
    };
  }, [paged, count, show]);

  // A new chapter starts at the top, or where reading stopped.
  useEffect(() => {
    if (!text.data || !settled || placed.current === chapter.id || (paged && !box.w)) return;
    // Where reading stopped wins: the audio's position may not be
    // restored yet (it still says 0, the chapter's heading).
    const target = startSegment ?? active;
    readAt.current = target;
    if (target === undefined || !show(target, false)) {
      ownScroll.current = true;
      ownStart.current = Date.now();
      window.scrollTo?.({ top: 0 });
      ownTimer.current = setTimeout(() => (ownScroll.current = false), 150);
    }
    placed.current = chapter.id;
  }, [text.data, settled, chapter.id, startSegment, active, show, paged, box.w]);

  // The voice moved (skipping, a seek while paused): reading is there.
  useEffect(() => {
    if (active !== undefined && placed.current === chapter.id) readAt.current = active;
  }, [active, chapter.id]);

  // Text of another size or shape flows anew: keep the position in view
  // (the browser cannot while a panel locks the page), as no reading.
  // So does another page size, or turning pages instead of scrolling.
  const layout = [
    prefs.turning,
    prefs.font,
    prefs.size,
    prefs.lineHeight,
    prefs.width,
    prefs.margin,
    prefs.paragraphs,
    box.w,
    box.h,
  ].join();
  const laidOut = useRef(layout);
  useLayoutEffect(() => {
    if (paged) count();
    if (laidOut.current === layout) return;
    laidOut.current = layout;
    const a = anchor();
    if (a !== undefined) show(a, false);
  }, [layout, active, show, paged, count, text.data]);

  // Keep the spoken segment in view (with pages, also the word spoken: a
  // segment can run over a page break).
  const voiceAt = paged ? `${active}:${word?.[0]}:${mark?.[0]}` : String(active);
  useEffect(() => {
    if (playing && follow && active !== undefined) show(active, true);
  }, [playing, follow, active, show, voiceAt]);

  // Turning a page: while playing, the voice is left behind (unless it is
  // on that page); while paused, the page's first segment becomes the
  // position. Past the last page comes the next chapter.
  const turnBy = (delta: number, repeat = false) => {
    // Nothing laid out yet (the text still loading): nothing to turn.
    if (!text.data || !box.w) return;
    const page = view.page + delta;
    if (page < 0) return;
    if (page >= pageCount) {
      // A held key does not run on through the chapters.
      if (delta > 0 && next && !repeat) onNext();
      return;
    }
    showPage(page, true);
    if (playing) {
      const el =
        active !== undefined ? content.current?.querySelector(`[data-seg="${active}"]`) : null;
      setFollow(!!el && pageOf(el) === page);
      return;
    }
    const first = firstOn(page);
    if (first !== undefined) {
      const id = spoken(first);
      setMarked(id);
      readAt.current = id;
      latest.current.onRead(id);
    }
  };
  const turn = useRef(turnBy);
  useEffect(() => {
    turn.current = turnBy;
  });
  const wheel = useRef({ at: 0, armed: true });
  const touch = useRef<{ x: number; y: number } | null>(null);
  useEffect(() => {
    if (!paged) return;
    const onKey = (e: KeyboardEvent) => {
      if (typing(e) || e.metaKey || e.ctrlKey || e.altKey) return;
      if (e.key === 'ArrowRight' || e.key === 'PageDown') {
        e.preventDefault();
        turn.current(1, e.repeat);
      } else if (e.key === 'ArrowLeft' || e.key === 'PageUp') {
        e.preventDefault();
        turn.current(-1, e.repeat);
      }
    };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, [paged]);

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
      // Pages are turned, not scrolled.
      if (latest.current.paged) return;
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
        if (latest.current.paged) return; // switched to pages meanwhile
        for (const el of root.current?.querySelectorAll<HTMLElement>('[data-seg]') ?? []) {
          if (el.getBoundingClientRect().bottom > TOP_OFFSET) {
            const id = spoken(Number(el.dataset.seg));
            setMarked(id);
            readAt.current = id;
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
        readAt.current = segmentId;
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
  const indented = prefs.paragraphs === 'indented';
  const width = WIDTHS[prefs.width];
  const body = (
    <>
      {!titled && (
        <Title order={2} ff="inherit" mb="lg" mt="md" fw={600} style={HEADING}>
          {chapter.title}
        </Title>
      )}
      {paragraphs.map((p, i) => (
        <ParagraphView
          key={p.id}
          p={p}
          spaced={!indented}
          // Printed books indent a paragraph that follows another one.
          indent={indented && p.kind === 'text' && paragraphs[i - 1]?.kind === 'text'}
          active={p.id === activeParagraph ? active : undefined}
          mark={p.id === activeParagraph ? mark : undefined}
          word={p.id === activeParagraph ? word : undefined}
          onSelect={select}
        />
      ))}
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
    </>
  );
  const backButton = playing && !follow && active !== undefined && (
    <Button
      size="xs"
      radius="xl"
      variant="default"
      leftSection={<IconFocus2 size={14} />}
      onClick={() => {
        setFollow(true);
        show(active, true);
      }}
    >
      Back to the voice
    </Button>
  );
  const backToVoice = backButton && (
    <Box pos="fixed" bottom={128} left="50%" style={{ transform: 'translateX(-50%)', zIndex: 3 }}>
      {backButton}
    </Box>
  );
  const font = {
    fontFamily: FONTS[prefs.font].family,
    fontSize: prefs.size,
    lineHeight: prefs.lineHeight,
    textAlign: prefs.align,
    hyphens: prefs.align === 'justify' ? 'auto' : undefined,
  } as const;
  const maw = width && `calc(${width}em + ${2 * prefs.margin}px)`;

  if (!paged) {
    return (
      <Box
        ref={root}
        lang="en"
        maw={maw}
        mx="auto"
        w="100%"
        style={{ ...font, paddingInline: prefs.margin }}
        data-testid="reader"
        aria-label="Chapter text"
      >
        {body}
        {backToVoice}
      </Box>
    );
  }
  // Pages: the text flows into columns as wide as the page, side by side,
  // and the page shown is moved into the window.
  return (
    <Box
      ref={root}
      lang="en"
      maw={maw}
      mx="auto"
      w="100%"
      h="100%"
      pos="relative"
      style={{ ...font, display: 'flex', flexDirection: 'column' }}
      data-testid="reader"
      aria-label="Chapter text"
    >
      <Box
        ref={viewport}
        style={{
          flex: 1,
          minHeight: 0,
          overflow: 'hidden',
          touchAction: 'pan-y',
          overscrollBehaviorX: 'none',
        }}
        onWheel={(e) => {
          if (e.ctrlKey) return; // zooming
          const w = wheel.current;
          const now = Date.now();
          if (now - w.at > WHEEL_GAP_MS) w.armed = true;
          w.at = now;
          const d = Math.abs(e.deltaY) >= Math.abs(e.deltaX) ? e.deltaY : e.deltaX;
          if (!w.armed || Math.abs(d) < 20) return;
          w.armed = false;
          turnBy(d > 0 ? 1 : -1);
        }}
        onTouchStart={(e) => {
          const t = e.touches[0];
          touch.current = t ? { x: t.clientX, y: t.clientY } : null;
        }}
        onTouchEnd={(e) => {
          const from = touch.current;
          const t = e.changedTouches[0];
          touch.current = null;
          if (!from || !t) return;
          const dx = t.clientX - from.x;
          if (Math.abs(dx) > SWIPE_PX && Math.abs(dx) > Math.abs(t.clientY - from.y)) {
            turnBy(dx < 0 ? 1 : -1);
          }
        }}
      >
        <Box
          ref={content}
          h="100%"
          data-testid="pages"
          style={{
            paddingInline: prefs.margin,
            columnCount: 1,
            columnGap: 2 * prefs.margin,
            columnFill: 'auto',
            transform: `translateX(${-view.page * box.w}px)`,
            transition: view.smooth ? 'transform 220ms ease' : undefined,
          }}
        >
          {body}
        </Box>
      </Box>
      <Group justify="center" gap="xs" py={4} style={{ fontFamily: 'var(--mantine-font-family)' }}>
        <ActionIcon
          variant="subtle"
          color="gray"
          aria-label="Previous page"
          disabled={view.page === 0}
          onClick={() => turnBy(-1)}
        >
          <IconChevronLeft size={18} />
        </ActionIcon>
        <Text size="xs" c="dimmed" miw={96} ta="center">
          Page {Math.min(view.page + 1, pageCount)} of {pageCount}
        </Text>
        <ActionIcon
          variant="subtle"
          color="gray"
          aria-label="Next page"
          disabled={view.page >= pageCount - 1 && !next}
          onClick={() => turnBy(1)}
        >
          <IconChevronRight size={18} />
        </ActionIcon>
      </Group>
      {backButton && (
        // Above the page controls, over the end of the page.
        <Box
          pos="absolute"
          bottom={44}
          left="50%"
          style={{ transform: 'translateX(-50%)', zIndex: 3 }}
        >
          {backButton}
        </Box>
      )}
    </Box>
  );
}

/** Headings stay with what follows them and are not justified. */
const HEADING = { textAlign: 'start', breakInside: 'avoid', breakAfter: 'avoid' } as const;

const ParagraphView = memo(function ParagraphView({
  p,
  spaced,
  indent,
  active,
  mark,
  word,
  onSelect,
}: {
  p: Paragraph;
  /** A gap after the paragraph (else printed-book indents). */
  spaced: boolean;
  indent: boolean;
  /** The marked segment, when it is in this paragraph. */
  active?: number;
  /** The marked characters of the active segment. */
  mark?: [number, number];
  /** The word of the active segment being spoken. */
  word?: [number, number];
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
        {pieces(
          s.text,
          s.emphasis,
          s.id === active ? mark : undefined,
          s.id === active ? word : undefined,
        ).map((piece, k) => (
          <span
            key={k}
            // Where the voice is (for turning to its page).
            data-word={piece.word || undefined}
            data-mark={piece.marked || undefined}
            style={{
              fontStyle: piece.italic ? 'italic' : undefined,
              background: piece.word
                ? 'var(--mantine-color-yellow-light-hover)'
                : piece.marked
                  ? 'var(--mantine-color-yellow-light)'
                  : undefined,
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
      <Title order={2} ff="inherit" fw={600} mt="md" mb="xs" style={HEADING}>
        {spans.length > 0 ? spans : p.text}
      </Title>
    );
  }
  return (
    <Text
      component="p"
      m={0}
      mb={spaced ? '0.9em' : 0}
      fz="inherit"
      lh="inherit"
      ff="inherit"
      style={indent ? { textIndent: '1.5em' } : undefined}
    >
      {spans}
    </Text>
  );
});
