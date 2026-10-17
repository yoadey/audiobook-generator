import { MantineProvider } from '@mantine/core';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { act, fireEvent, render, screen } from '@testing-library/react';
import type { ComponentProps } from 'react';
import { describe, expect, it, vi } from 'vitest';

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
    prefs: { size: 'M', font: 'serif' },
  };
  const view = (p: Partial<typeof props>) => (
    <MantineProvider>
      <QueryClientProvider client={queryClient}>
        <Reader {...props} {...p} />
      </QueryClientProvider>
    </MantineProvider>
  );
  const r = render(view({}));
  return { scrollTo, rerender: (p: Partial<typeof props>) => r.rerender(view(p)) };
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
});
