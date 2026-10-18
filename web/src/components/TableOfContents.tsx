import { Badge, Group, ScrollArea, Stack, Text, UnstyledButton } from '@mantine/core';
import { useNavigate } from '@tanstack/react-router';
import { Fragment, useEffect, useRef } from 'react';

import type { Chapter, Volume } from '../api/client';
import { formatReadingTime } from '../lib/format';

/**
 * A book's contents to read: the chapters (by volume), each opening the
 * reader at its start; the one being read is marked and in view.
 */
export function TableOfContents({
  bookId,
  chapters,
  volumes,
  currentId,
}: {
  bookId: number;
  /** The chapters to read (not the excluded ones), in order. */
  chapters: Chapter[];
  volumes: Volume[];
  currentId?: number;
}) {
  const navigate = useNavigate();
  const viewport = useRef<HTMLDivElement>(null);
  const current = useRef<HTMLButtonElement>(null);
  const titles = new Map(volumes.map((v) => [v.id, v.title]));

  // Once per chapter: later changes (a refetch) do not move the list.
  const shownFor = useRef<number | undefined>(undefined);
  useEffect(() => {
    const el = current.current;
    const vp = viewport.current;
    if (currentId === undefined || shownFor.current === currentId || !el || !vp) return;
    shownFor.current = currentId;
    vp.scrollTop = el.offsetTop - vp.clientHeight / 2 + el.clientHeight / 2;
  }, [currentId, chapters]);

  return (
    <ScrollArea.Autosize mah="60vh" viewportRef={viewport} type="auto" offsetScrollbars>
      <Stack gap={0} pos="relative" component="nav" aria-label="Contents">
        {chapters.map((c, i) => {
          const volume = c.volumeId !== undefined ? titles.get(c.volumeId) : undefined;
          const newVolume = volume !== undefined && chapters[i - 1]?.volumeId !== c.volumeId;
          const isCurrent = c.id === currentId;
          return (
            <Fragment key={c.id}>
              {newVolume && (
                <Text
                  size="xs"
                  fw={600}
                  tt="uppercase"
                  c="dimmed"
                  mt={i > 0 ? 'md' : 0}
                  px="sm"
                  py={4}
                >
                  {volume}
                </Text>
              )}
              <UnstyledButton
                ref={isCurrent ? current : undefined}
                aria-current={isCurrent ? 'true' : undefined}
                onClick={() =>
                  void navigate({
                    to: '/books/$bookId/listen',
                    params: { bookId: String(bookId) },
                    search: isCurrent ? {} : { chapter: c.id },
                  })
                }
                px="sm"
                py={8}
                style={{ borderRadius: 4 }}
                bg={isCurrent ? 'var(--mantine-color-default-hover)' : undefined}
              >
                <Group justify="space-between" wrap="nowrap" gap="sm">
                  <Text size="sm" fw={isCurrent ? 600 : undefined} truncate>
                    {c.title}
                  </Text>
                  <Group gap="xs" wrap="nowrap" style={{ flexShrink: 0 }}>
                    {isCurrent && (
                      <Badge size="xs" variant="light">
                        Reading
                      </Badge>
                    )}
                    <Text size="xs" c="dimmed">
                      {formatReadingTime(c.wordCount)}
                    </Text>
                  </Group>
                </Group>
              </UnstyledButton>
            </Fragment>
          );
        })}
      </Stack>
    </ScrollArea.Autosize>
  );
}
