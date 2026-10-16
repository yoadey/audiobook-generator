import { ActionIcon, Loader, Tooltip } from '@mantine/core';
import { IconPlayerPlay, IconPlayerStop } from '@tabler/icons-react';

import type { PreviewText, usePreview } from '../lib/preview';

/** Plays a voice; text may be fetched lazily (e.g. a quote of the character). */
export function VoicePreviewButton({
  id,
  voice,
  speed,
  text,
  preview,
  label = 'Listen',
}: {
  id: string;
  voice: string;
  speed: number;
  text?: PreviewText;
  preview: ReturnType<typeof usePreview>;
  label?: string;
}) {
  // Stop also cancels a preview that is still being synthesized.
  const active = preview.playing === id || preview.loading === id;
  return (
    <Tooltip label={active ? 'Stop' : label}>
      <ActionIcon
        variant="subtle"
        aria-label={active ? 'Stop preview' : label}
        disabled={!voice}
        onClick={() => (active ? preview.stop() : void preview.play(id, voice, speed, text))}
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
