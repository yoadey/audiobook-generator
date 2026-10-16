import { act, renderHook } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';

import { usePreview } from './preview';

class FakeAudio {
  static all: FakeAudio[] = [];
  paused = true;
  onended: (() => void) | null = null;
  constructor(public src: string) {
    FakeAudio.all.push(this);
  }
  play() {
    this.paused = false;
    return Promise.resolve();
  }
  pause() {
    this.paused = true;
  }
}

describe('usePreview', () => {
  const { createObjectURL, revokeObjectURL } = URL;
  afterEach(() => {
    URL.createObjectURL = createObjectURL;
    URL.revokeObjectURL = revokeObjectURL;
    vi.unstubAllGlobals();
    FakeAudio.all = [];
  });

  it('cancels a preview that is still loading when another starts', async () => {
    const resolvers: ((r: Response) => void)[] = [];
    vi.stubGlobal(
      'fetch',
      vi.fn(
        (_url: string, init: RequestInit) =>
          new Promise<Response>((resolve, reject) => {
            resolvers.push(resolve);
            init.signal?.addEventListener('abort', () =>
              reject(new DOMException('aborted', 'AbortError')),
            );
          }),
      ),
    );
    vi.stubGlobal('Audio', FakeAudio);
    let n = 0;
    const revoke = vi.fn();
    URL.createObjectURL = () => `blob:${++n}`;
    URL.revokeObjectURL = revoke;

    const { result, unmount } = renderHook(() => usePreview());
    let first: Promise<void> | undefined;
    act(() => {
      first = result.current.play('a', 'af_heart', 1, 'one');
    });
    expect(result.current.loading).toBe('a');
    let second: Promise<void> | undefined;
    act(() => {
      second = result.current.play('b', 'bf_emma', 1, 'two');
    });
    await act(async () => {
      await first;
    });
    expect(result.current.loading).toBe('b');
    await act(async () => {
      resolvers[1]?.(new Response('mp3'));
      await second;
    });
    expect(FakeAudio.all).toHaveLength(1);
    expect(result.current.playing).toBe('b');

    unmount();
    expect(FakeAudio.all[0]?.paused).toBe(true);
    expect(revoke).toHaveBeenCalledWith('blob:1');
  });
});
