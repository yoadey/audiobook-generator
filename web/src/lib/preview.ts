import { useCallback, useEffect, useRef, useState } from 'react';

import { type Emotion, previewVoice } from '../api/client';
import { notifyError } from './notify';

/** Text of a preview, or a function that finds it (e.g. a quote of the character). */
export type PreviewText = string | ((signal: AbortSignal) => Promise<string | undefined>);

const isAbort = (err: unknown) => err instanceof DOMException && err.name === 'AbortError';

/**
 * Plays voice previews one at a time: starting one cancels the previous
 * (also while it is still loading), and object URLs are released when
 * playback ends, is replaced, or the component unmounts. With bookId, the
 * book's pronunciation lexicon applies to the text.
 */
export function usePreview(bookId?: number) {
  const audio = useRef<HTMLAudioElement | null>(null);
  const url = useRef<string | null>(null);
  const request = useRef<AbortController | null>(null);
  const [playing, setPlaying] = useState<string | null>(null);
  const [loading, setLoading] = useState<string | null>(null);

  const stop = useCallback(() => {
    request.current?.abort();
    request.current = null;
    if (audio.current) {
      audio.current.onended = null;
      audio.current.pause();
      audio.current = null;
    }
    if (url.current) {
      URL.revokeObjectURL(url.current);
      url.current = null;
    }
    setPlaying(null);
    setLoading(null);
  }, []);

  useEffect(() => stop, [stop]);

  /**
   * Plays voice at speed; id names the preview for `playing`/`loading`.
   * lexicon false skips the book's lexicon (text already spells it out);
   * emotion is for engines that speak emotions themselves.
   */
  const play = useCallback(
    async (
      id: string,
      voice: string,
      speed: number,
      text?: PreviewText,
      lexicon = true,
      emotion?: Emotion,
    ) => {
      stop();
      const ctrl = new AbortController();
      request.current = ctrl;
      setLoading(id);
      try {
        const sample = typeof text === 'function' ? await text(ctrl.signal) : text;
        const u = await previewVoice(
          voice,
          speed,
          sample,
          ctrl.signal,
          lexicon ? bookId : undefined,
          emotion,
        );
        if (ctrl.signal.aborted) {
          URL.revokeObjectURL(u);
          return;
        }
        url.current = u;
        const a = new Audio(u);
        audio.current = a;
        a.onended = stop;
        request.current = null;
        setLoading(null);
        setPlaying(id);
        await a.play();
      } catch (err) {
        if (ctrl.signal.aborted || isAbort(err)) {
          return; // replaced or stopped
        }
        stop();
        notifyError('Preview failed', err);
      }
    },
    [stop, bookId],
  );

  return { play, stop, playing, loading };
}
