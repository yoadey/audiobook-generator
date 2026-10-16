import { useCallback, useEffect, useRef, useState } from 'react';

import { previewVoice } from '../api/client';
import { notifyError } from './notify';

/**
 * Plays voice previews one at a time: starting one stops the previous, and
 * object URLs are released when playback ends or the component unmounts.
 */
export function usePreview() {
  const audio = useRef<HTMLAudioElement | null>(null);
  const url = useRef<string | null>(null);
  const [playing, setPlaying] = useState<string | null>(null);
  const [loading, setLoading] = useState<string | null>(null);

  const stop = useCallback(() => {
    audio.current?.pause();
    audio.current = null;
    if (url.current) {
      URL.revokeObjectURL(url.current);
      url.current = null;
    }
    setPlaying(null);
  }, []);

  useEffect(() => stop, [stop]);

  /** Plays voice at speed; id names the preview for `playing`/`loading`. */
  const play = useCallback(
    async (
      id: string,
      voice: string,
      speed: number,
      text?: string | (() => Promise<string | undefined>),
    ) => {
      stop();
      setLoading(id);
      try {
        const sample = typeof text === 'function' ? await text() : text;
        const u = await previewVoice(voice, speed, sample);
        url.current = u;
        const a = new Audio(u);
        audio.current = a;
        a.onended = stop;
        setPlaying(id);
        await a.play();
      } catch (err) {
        stop();
        notifyError('Preview failed', err);
      } finally {
        setLoading(null);
      }
    },
    [stop],
  );

  return { play, stop, playing, loading };
}
