import { ActionIcon, Box, Group, Menu, Switch, Text } from '@mantine/core';
import { IconDots, IconGitMerge, IconWaveSine } from '@tabler/icons-react';
import { useMutation, useQueryClient } from '@tanstack/react-query';
import { useMediaQuery } from '@mantine/hooks';
import { useVirtualizer } from '@tanstack/react-virtual';
import { useRef } from 'react';

import { type Chapter, mergeChapterWithNext, renderChapter, updateChapter } from '../api/client';
import { formatNumber } from '../lib/format';
import { confirmMerge } from '../lib/confirm';
import { AudioBadge } from './AudioBadge';
import { AnchorLink } from './links';
import { notifyError } from '../lib/notify';

const ROW_HEIGHT = 44;
const columns = '56px minmax(0, 1fr) 90px 70px 80px 100px 80px 44px';
// Phones: title, audio, narrate and actions only, so the page never gets
// wider than the screen (a wider page also pushes dialogs off it).
const compactColumns = 'minmax(0, 1fr) 96px 40px 32px';
const headers = ['#', 'Title', 'Words', 'Quotes', 'Speakers', 'Audio', 'Narrate', ''];
const compactHeaders = ['Title', 'Audio', '', ''];

const speakerLabels: Record<Chapter['attributionStatus'], [string, string]> = {
  none: ['–', 'dimmed'],
  queued: ['queued', 'blue'],
  running: ['…', 'blue'],
  done: ['✔', 'green'],
  failed: ['failed', 'red'],
};

function useChapterMutations(bookId: number) {
  const queryClient = useQueryClient();
  const invalidate = () =>
    Promise.all([
      queryClient.invalidateQueries({ queryKey: ['books', bookId] }),
      queryClient.invalidateQueries({ queryKey: ['chapters'] }),
    ]);
  const toggle = useMutation({
    mutationFn: ({ id, excluded }: { id: number; excluded: boolean }) =>
      updateChapter(id, { excluded }),
    onSuccess: invalidate,
    onError: (err) => notifyError('Could not update chapter', err),
  });
  const merge = useMutation({
    mutationFn: mergeChapterWithNext,
    onSuccess: invalidate,
    onError: (err) => notifyError('Could not merge chapters', err),
  });
  const render = useMutation({
    mutationFn: (id: number) => renderChapter(id, true),
    onSuccess: invalidate,
    onError: (err) => notifyError('Could not queue rendering', err),
  });
  return { toggle, merge, render };
}

/** Virtualized chapter list: books can have thousands of chapters. */
export function ChapterTable({ bookId, chapters }: { bookId: number; chapters: Chapter[] }) {
  const parentRef = useRef<HTMLDivElement>(null);
  const { toggle, merge, render } = useChapterMutations(bookId);
  const compact = useMediaQuery('(max-width: 40em)') ?? false;
  const grid = {
    display: 'grid',
    gridTemplateColumns: compact ? compactColumns : columns,
    gap: compact ? 'var(--mantine-spacing-xs)' : 'var(--mantine-spacing-sm)',
  };
  // TanStack Virtual returns new functions every render, which the React
  // Compiler cannot memoize; that is expected for this component.
  // eslint-disable-next-line react-hooks/incompatible-library
  const virtualizer = useVirtualizer({
    count: chapters.length,
    getScrollElement: () => parentRef.current,
    estimateSize: () => ROW_HEIGHT,
    overscan: 12,
    // jsdom (tests) has no layout; render a first screen anyway.
    initialRect: { width: 1000, height: 800 },
  });

  return (
    <Box>
      <Box
        px="xs"
        py={6}
        style={{ ...grid, borderBottom: '1px solid var(--mantine-color-default-border)' }}
      >
        {(compact ? compactHeaders : headers).map((h, i) => (
          <Text
            key={i}
            size="xs"
            fw={600}
            c="dimmed"
            ta={!compact && i >= 2 && i <= 3 ? 'right' : 'left'}
          >
            {h}
          </Text>
        ))}
      </Box>
      <Box
        ref={parentRef}
        style={{ height: 'calc(100dvh - 300px)', minHeight: 320, overflow: 'auto' }}
      >
        <Box style={{ height: virtualizer.getTotalSize(), position: 'relative' }}>
          {virtualizer.getVirtualItems().map((row) => {
            const c = chapters[row.index];
            if (!c) return null;
            const next = chapters[row.index + 1];
            return (
              <Box
                key={c.id}
                data-testid="chapter-row"
                px="xs"
                style={{
                  position: 'absolute',
                  top: 0,
                  left: 0,
                  width: '100%',
                  height: ROW_HEIGHT,
                  transform: `translateY(${row.start}px)`,
                  ...grid,
                  alignItems: 'center',
                  borderBottom: '1px solid var(--mantine-color-default-border)',
                  opacity: c.excluded ? 0.55 : 1,
                }}
              >
                {!compact && (
                  <Text size="sm" c="dimmed">
                    {c.idx + 1}
                  </Text>
                )}
                <AnchorLink
                  to="/books/$bookId/chapters/$chapterId"
                  params={{ bookId: String(bookId), chapterId: String(c.id) }}
                  size="sm"
                  truncate
                >
                  {c.title}
                </AnchorLink>
                {!compact && (
                  <>
                    <Text size="sm" ta="right">
                      {formatNumber(c.wordCount)}
                    </Text>
                    <Text size="sm" ta="right">
                      {formatNumber(c.quoteCount)}
                    </Text>
                    <Text
                      size="sm"
                      c={(speakerLabels[c.attributionStatus] ?? speakerLabels.none)[1]}
                      title={c.attributionError ?? `Speakers: ${c.attributionStatus}`}
                    >
                      {c.quoteCount > 0
                        ? (speakerLabels[c.attributionStatus] ?? speakerLabels.none)[0]
                        : ''}
                    </Text>
                  </>
                )}
                <Group>
                  <AudioBadge chapter={c} />
                </Group>
                <Group>
                  <Switch
                    size="xs"
                    checked={!c.excluded}
                    aria-label={`Narrate ${c.title}`}
                    onChange={(e) =>
                      toggle.mutate({ id: c.id, excluded: !e.currentTarget.checked })
                    }
                  />
                </Group>
                <Menu position="bottom-end" withinPortal>
                  <Menu.Target>
                    <ActionIcon variant="subtle" color="gray" aria-label={`Actions for ${c.title}`}>
                      <IconDots size={16} />
                    </ActionIcon>
                  </Menu.Target>
                  <Menu.Dropdown>
                    <Menu.Item
                      leftSection={<IconWaveSine size={14} />}
                      onClick={() => render.mutate(c.id)}
                    >
                      {c.audioStatus === 'ready' ? 'Render audio again' : 'Render audio'}
                    </Menu.Item>
                    <Menu.Item
                      leftSection={<IconGitMerge size={14} />}
                      disabled={!next}
                      onClick={() => confirmMerge(c, next, () => merge.mutate(c.id))}
                    >
                      Merge with next chapter
                    </Menu.Item>
                  </Menu.Dropdown>
                </Menu>
              </Box>
            );
          })}
        </Box>
      </Box>
    </Box>
  );
}
