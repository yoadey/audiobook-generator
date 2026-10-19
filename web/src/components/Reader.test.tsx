import { MantineProvider } from '@mantine/core';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { act, fireEvent, render, screen } from '@testing-library/react';
import type { ComponentProps } from 'react';
import { describe, expect, it, onTestFinished, vi } from 'vitest';

import { DEFAULT_PREFS } from '../lib/readerPrefs';
import { Reader } from './Reader';

const chapter = {
  id: 10,
  bookId: 1,
  idx: 0,
  title: 'One',
  wordCount: 10,
  quoteCount: 0,
  excluded: false,
  status: 'imported',
  audioStatus: 'ready',
  audioStale: false,
} as const;

const segs = [1, 2, 3, 4].map((id) => ({
  id,
  idx: id,
  kind: 'narration' as const,
  quoteRef: 0,
  text: `Sentence number ${id}.`,
  confidence: 1,
  speakerManual: false,
}));

const cues = segs.map((s, i) => ({ segmentId: s.id, startMs: 1000 * i, endMs: 1000 * i + 900 }));

function setup() {
  const queryClient = new QueryClient();
  queryClient.setQueryData(['chapters', 10], {
    ...chapter,
    paragraphs: segs.map((s) => ({
      id: 100 + s.id,
      idx: s.id,
      kind: 'text',
      text: s.text,
      segments: [s],
    })),
  });
  const scrollTo = vi.fn();
  vi.stubGlobal('scrollTo', scrollTo);
  const props: ComponentProps<typeof Reader> = {
    chapter: chapter as never,
    cues,
    settled: true,
    positionMs: 0,
    playing: false,
    onSeek: vi.fn(),
    onRead: vi.fn(),
    onNext: vi.fn(),
    prefs: DEFAULT_PREFS,
  };
  const view = (p: Partial<typeof props>) => (
    <MantineProvider>
      <QueryClientProvider client={queryClient}>
        <Reader {...props} {...p} />
      </QueryClientProvider>
    </MantineProvider>
  );
  const r = render(view({}));
  return { scrollTo, props, rerender: (p: Partial<typeof props>) => r.rerender(view(p)) };
}

describe('Reader', () => {
  it('keeps following after play is pressed (its own scroll is no user scroll)', async () => {
    const { scrollTo, rerender } = setup();
    await screen.findByText('Sentence number 1.');
    fireEvent.keyDown(window, { key: ' ' });
    rerender({ playing: true, positionMs: 100 });
    act(() => {
      window.dispatchEvent(new Event('scroll'));
    });
    scrollTo.mockClear();
    rerender({ playing: true, positionMs: 2100 });
    expect(scrollTo).toHaveBeenCalled();
    expect(screen.queryByText('Back to the voice')).toBeNull();
  });

  it('stops following when the user scrolls with the wheel', async () => {
    const { scrollTo, rerender } = setup();
    await screen.findByText('Sentence number 1.');
    rerender({ playing: true, positionMs: 100 });
    await new Promise((r) => setTimeout(r, 5));
    act(() => {
      window.dispatchEvent(new WheelEvent('wheel'));
      window.dispatchEvent(new Event('scroll'));
    });
    scrollTo.mockClear();
    rerender({ playing: true, positionMs: 2100 });
    expect(scrollTo).not.toHaveBeenCalled();
    expect(screen.getByText('Back to the voice')).toBeInTheDocument();
  });

  const marked = () => document.querySelectorAll('[data-seg] [style*="yellow-light"]').length;

  it('marks the position while paused unless it is turned off', async () => {
    const { rerender } = setup();
    await screen.findByText('Sentence number 1.');
    rerender({ positionMs: 2100 });
    expect(marked()).toBe(1);
    rerender({ positionMs: 2100, prefs: { ...DEFAULT_PREFS, showPosition: false } });
    expect(marked()).toBe(0);
    // Playing still marks what is spoken.
    rerender({ positionMs: 2100, playing: true, prefs: { ...DEFAULT_PREFS, showPosition: false } });
    expect(marked()).toBe(1);
  });

  it('marks nothing while playing when that is chosen', async () => {
    const { rerender } = setup();
    await screen.findByText('Sentence number 1.');
    rerender({ positionMs: 2100, playing: true, prefs: { ...DEFAULT_PREFS, highlight: 'off' } });
    expect(marked()).toBe(0);
  });

  it('keeps the position in view when the text is laid out anew', async () => {
    const { scrollTo, props, rerender } = setup();
    await screen.findByText('Sentence number 1.');
    rerender({ positionMs: 2100 });
    scrollTo.mockClear();
    rerender({ positionMs: 2100, prefs: { ...DEFAULT_PREFS, size: 26, width: 'narrow' } });
    expect(scrollTo).toHaveBeenCalledTimes(1);
    // Its own scroll: not the reader moving the position.
    act(() => {
      window.dispatchEvent(new Event('scroll'));
    });
    await new Promise((r) => setTimeout(r, 800));
    expect(props.onRead).not.toHaveBeenCalled();
  });

  it('turns pages with the arrows; past the last one comes the next chapter', async () => {
    // A page of 600 by 400 (jsdom lays nothing out).
    const rect = vi
      .spyOn(HTMLElement.prototype, 'getBoundingClientRect')
      .mockReturnValue({ width: 600, height: 400, left: 0, top: 0 } as DOMRect);
    onTestFinished(() => rect.mockRestore());
    const { props, rerender } = setup();
    await screen.findByText('Sentence number 1.');
    const pages = { ...DEFAULT_PREFS, turning: 'pages' as const };
    rerender({ prefs: pages, next: { ...chapter, id: 11, title: 'Two' } as never });
    expect(screen.getByText('Page 1 of 1')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Previous page' })).toBeDisabled();
    // A held key does not run on into the next chapter.
    fireEvent.keyDown(window, { key: 'ArrowRight', repeat: true });
    expect(props.onNext).not.toHaveBeenCalled();
    fireEvent.keyDown(window, { key: 'ArrowRight' });
    expect(props.onNext).toHaveBeenCalledTimes(1);
  });

  it('leaves the voice when turning away from it while playing, and follows it again back on its page', async () => {
    // Three pages of 600 by 400; all text on the first (jsdom lays nothing out).
    const rect = vi
      .spyOn(HTMLElement.prototype, 'getBoundingClientRect')
      .mockReturnValue({ width: 600, height: 400, left: 0, top: 0 } as DOMRect);
    const width = vi.spyOn(HTMLElement.prototype, 'scrollWidth', 'get').mockReturnValue(1800);
    onTestFinished(() => {
      rect.mockRestore();
      width.mockRestore();
    });
    const { rerender } = setup();
    await screen.findByText('Sentence number 1.');
    const pages = { ...DEFAULT_PREFS, turning: 'pages' as const };
    rerender({ prefs: pages, playing: true, positionMs: 100 });
    expect(await screen.findByText('Page 1 of 3')).toBeInTheDocument();

    fireEvent.keyDown(window, { key: 'ArrowRight' });
    expect(screen.getByText('Page 2 of 3')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Back to the voice' })).toBeInTheDocument();
    rerender({ prefs: pages, playing: true, positionMs: 300 });
    expect(screen.getByText('Page 2 of 3')).toBeInTheDocument(); // stays where the reader is

    fireEvent.keyDown(window, { key: 'ArrowLeft' });
    expect(screen.getByText('Page 1 of 3')).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Back to the voice' })).toBeNull();
    rerender({ prefs: pages, playing: true, positionMs: 1100 });
    expect(screen.getByText('Page 1 of 3')).toBeInTheDocument(); // no snap away
  });

  it('lays the text out as chosen', async () => {
    const { rerender } = setup();
    await screen.findByText('Sentence number 1.');
    rerender({
      prefs: {
        ...DEFAULT_PREFS,
        font: 'literata',
        size: 24,
        lineHeight: 2,
        margin: 40,
        align: 'justify',
        paragraphs: 'indented',
      },
    });
    const reader = screen.getByTestId('reader');
    expect(reader.style.fontFamily).toContain('Literata');
    expect(reader.style.fontSize).toBe('24px');
    expect(reader.style.lineHeight).toBe('2');
    expect(reader.style.paddingInline).toBe('40px');
    expect(reader.style.textAlign).toBe('justify');
    const [first, second] = screen.getAllByText(/^Sentence number [12]\.$/);
    expect(first!.closest('p')!.style.textIndent).toBe('');
    expect(second!.closest('p')!.style.textIndent).toBe('1.5em');
  });
});
