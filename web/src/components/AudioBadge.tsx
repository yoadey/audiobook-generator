import { Badge, Loader, Tooltip } from '@mantine/core';

import type { Chapter } from '../api/client';
import { formatDuration } from '../lib/format';

/** Audio state of a chapter: duration when ready, otherwise its status. */
export function AudioBadge({ chapter }: { chapter: Chapter }) {
  switch (chapter.audioStatus) {
    case 'ready':
      return (
        <Tooltip
          label={chapter.audioStale ? 'Rendered with another narrator setting' : 'Audio ready'}
        >
          <Badge
            variant="light"
            color={chapter.audioStale ? 'yellow' : 'green'}
            style={{ flexShrink: 0 }}
          >
            {formatDuration(chapter.audioDurationMs ?? 0)}
          </Badge>
        </Tooltip>
      );
    case 'queued':
      return (
        <Badge variant="light" color="gray" style={{ flexShrink: 0 }}>
          queued
        </Badge>
      );
    case 'rendering':
      return (
        <Badge
          variant="light"
          color="blue"
          leftSection={<Loader size={10} color="blue" />}
          style={{ flexShrink: 0 }}
        >
          rendering
        </Badge>
      );
    case 'failed':
      return (
        <Tooltip label={chapter.audioError ?? 'Rendering failed'} multiline w={320}>
          <Badge variant="light" color="red" style={{ flexShrink: 0 }}>
            failed
          </Badge>
        </Tooltip>
      );
    default:
      return null;
  }
}
