import { Badge, Loader, Tooltip } from '@mantine/core';

import type { Chapter } from '../api/client';
import { formatDuration } from '../lib/format';

/** Audio state of a chapter: duration when ready, otherwise its status. */
export function AudioBadge({ chapter }: { chapter: Chapter }) {
  switch (chapter.audioStatus) {
    case 'ready': {
      const label = chapter.audioRerendering
        ? 'Being rendered again; the current audio stays playable until then'
        : chapter.audioError
          ? `Rendering it again failed: ${chapter.audioError}`
          : chapter.audioStale
            ? 'Outdated: the narrator, speakers or their voices changed since'
            : 'Audio ready';
      return (
        <Tooltip label={label} multiline w={320}>
          <Badge
            variant="light"
            color={chapter.audioError ? 'orange' : chapter.audioStale ? 'yellow' : 'green'}
            leftSection={chapter.audioRerendering ? <Loader size={10} color="yellow" /> : undefined}
            style={{ flexShrink: 0 }}
          >
            {formatDuration(chapter.audioDurationMs ?? 0)}
          </Badge>
        </Tooltip>
      );
    }
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
