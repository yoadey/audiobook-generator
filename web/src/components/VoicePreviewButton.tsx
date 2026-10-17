import { ActionIcon, Loader, Tooltip } from '@mantine/core';
import { IconPlayerPlay, IconPlayerStop } from '@tabler/icons-react';

import type { Emotion } from '../api/client';
import type { PreviewText, usePreview } from '../lib/preview';

/** Plays a voice; text may be fetched lazily (e.g. a quote of the character). */
export function VoicePreviewButton({
  id,
  voice,
  speed,
  text,
  preview,
  label = 'Listen',
  lexicon = true,
  disabled = false,
  emotion,
}: {
  id: string;
  voice: string;
  speed: number;
  text?: PreviewText;
  preview: ReturnType<typeof usePreview>;
  label?: string;
  /** false: the text is spoken as is, without the book's lexicon. */
  lexicon?: boolean;
  disabled?: boolean;
  /** Spoken with this emotion (engines that speak emotions themselves). */
  emotion?: Emotion;
}) {
  // Stop also cancels a preview that is still being synthesized.
  const active = preview.playing === id || preview.loading === id;
  return (
    <Tooltip label={active ? 'Stop' : label}>
      <ActionIcon
        variant="subtle"
        aria-label={active ? 'Stop preview' : label}
        disabled={!voice || (disabled && !active)}
        onClick={() =>
          active ? preview.stop() : void preview.play(id, voice, speed, text, lexicon, emotion)
        }
      >
        {preview.loading === id ? (
          <Loader size={14} />
        ) : active ? (
          <IconPlayerStop size={16} />
        ) : (
          <IconPlayerPlay size={16} />
        )}
      </ActionIcon>
    </Tooltip>
  );
}
