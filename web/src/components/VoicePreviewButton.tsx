import { ActionIcon, Tooltip } from '@mantine/core';
import { IconPlayerPlay, IconPlayerStop } from '@tabler/icons-react';

import type { usePreview } from '../lib/preview';

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
  text?: string | (() => Promise<string | undefined>);
  preview: ReturnType<typeof usePreview>;
  label?: string;
}) {
  const playing = preview.playing === id;
  return (
    <Tooltip label={playing ? 'Stop' : label}>
      <ActionIcon
        variant="subtle"
        aria-label={playing ? 'Stop preview' : label}
        loading={preview.loading === id}
        disabled={!voice}
        onClick={() => (playing ? preview.stop() : void preview.play(id, voice, speed, text))}
      >
        {playing ? <IconPlayerStop size={16} /> : <IconPlayerPlay size={16} />}
      </ActionIcon>
    </Tooltip>
  );
}
