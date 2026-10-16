import { Text } from '@mantine/core';
import { modals } from '@mantine/modals';

import type { Chapter } from '../api/client';

export function confirmMerge(chapter: Chapter, next: Chapter | undefined, onConfirm: () => void) {
  modals.openConfirmModal({
    title: 'Merge chapters',
    children: (
      <Text size="sm">
        Append “{next?.title ?? 'the next chapter'}” to “{chapter.title}”? You can split it again
        later.
      </Text>
    ),
    labels: { confirm: 'Merge', cancel: 'Cancel' },
    onConfirm,
  });
}
