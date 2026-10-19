import {
  ActionIcon,
  Alert,
  Box,
  Breadcrumbs,
  Center,
  Flex,
  Group,
  Loader,
  Menu,
  Progress,
  Spoiler,
  Stack,
  Text,
  Title,
} from '@mantine/core';
import { modals } from '@mantine/modals';
import { IconBook, IconDots, IconFileExport, IconHeadphones, IconTrash } from '@tabler/icons-react';
import { useIsMutating, useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { useNavigate, useParams } from '@tanstack/react-router';

import {
  bookQuery,
  chaptersQuery,
  deleteBook,
  exportUrl,
  progressQuery,
  progressSaveKey,
  volumesQuery,
} from '../api/client';
import { Cover } from '../components/Cover';
import { AnchorLink, ButtonLink } from '../components/links';
import { StatusBadge } from '../components/StatusBadge';
import { TableOfContents } from '../components/TableOfContents';
import { formatNumber, formatReadingTime } from '../lib/format';
import { notifyError } from '../lib/notify';

/**
 * A book in the library: its cover, description and contents, to read.
 * Everything about its audiobook is a page of its own.
 */
export function BookPage() {
  const { bookId: raw } = useParams({ from: '/books/$bookId/' });
  const bookId = Number(raw);
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const book = useQuery(bookQuery(bookId));
  const imported = book.data !== undefined && book.data.status !== 'importing';
  const volumes = useQuery({ ...volumesQuery(bookId), enabled: imported });
  const chapters = useQuery({ ...chaptersQuery(bookId), enabled: imported });
  // Where reading stopped: the contents open at it. While the reader still
  // stores it (just left), its answer is the stored position.
  const saving = useIsMutating({ mutationKey: progressSaveKey(bookId) }) > 0;
  const progress = useQuery({
    ...progressQuery(bookId),
    enabled: imported && !saving,
    refetchOnMount: 'always',
  });

  const remove = useMutation({
    mutationFn: () => deleteBook(bookId),
    onSuccess: async () => {
      await queryClient.invalidateQueries({ queryKey: ['books'] });
      await navigate({ to: '/library' });
    },
    onError: (err) => notifyError('Could not delete book', err),
  });

  if (book.isPending) {
    return (
      <Center py="xl">
        <Loader />
      </Center>
    );
  }
  if (book.error) {
    return (
      <Alert color="red" title="Could not load book">
        {book.error.message}
      </Alert>
    );
  }
  const b = book.data;
  const readable = imported && b.status !== 'failed';
  const toRead = (chapters.data?.items ?? []).filter((c) => !c.excluded);
  const words = toRead.reduce((sum, c) => sum + c.wordCount, 0);
  const currentId = progress.data?.chapterId;
  const current = toRead.find((c) => c.id === currentId);
  // Every chapter excluded (or none at all): nothing for the reader.
  const nothingToRead = chapters.isSuccess && toRead.length === 0;
  const started = current !== undefined && (current !== toRead[0] || progress.data!.positionMs > 0);

  return (
    <Stack gap="lg">
      <Breadcrumbs>
        <AnchorLink to="/library" size="sm">
          Library
        </AnchorLink>
        <Text size="sm">{b.title}</Text>
      </Breadcrumbs>

      <Flex gap="lg" direction={{ base: 'column', xs: 'row' }} align={{ xs: 'flex-start' }}>
        <Box w={{ base: 140, xs: 180 }} style={{ flexShrink: 0 }}>
          <Cover book={b} h={240} position="left top" />
        </Box>
        <Stack gap="xs" style={{ flex: 1, minWidth: 0 }}>
          <Group gap="sm" wrap="nowrap" align="flex-start">
            <Title order={2}>{b.title}</Title>
            {(b.status === 'importing' || b.status === 'failed') && (
              <StatusBadge status={b.status} />
            )}
          </Group>
          {b.author && (
            <Text c="dimmed" size="lg">
              {b.author}
            </Text>
          )}
          {toRead.length > 0 && (
            <Text size="sm" c="dimmed">
              {formatNumber(toRead.length)} chapters · ~{formatReadingTime(words)} to read
            </Text>
          )}
          {b.status === 'importing' && (
            <Stack gap={4}>
              <Text size="sm">Importing…</Text>
              <Progress value={b.progress * 100} animated />
            </Stack>
          )}
          {b.status === 'failed' && (
            <Alert color="red" title="Import failed">
              {b.error}
            </Alert>
          )}
          <Group gap="sm" mt="xs">
            {readable && !nothingToRead && (
              <ButtonLink
                to="/books/$bookId/listen"
                params={{ bookId: String(bookId) }}
                leftSection={<IconBook size={16} />}
              >
                {started ? 'Continue reading' : 'Read'}
              </ButtonLink>
            )}
            {readable && (
              <ButtonLink
                variant="default"
                to="/books/$bookId/audiobook"
                params={{ bookId: String(bookId) }}
                leftSection={<IconHeadphones size={16} />}
              >
                Audiobook
              </ButtonLink>
            )}
            <Menu position="bottom-end" withinPortal>
              <Menu.Target>
                <ActionIcon variant="subtle" color="gray" size="lg" aria-label="More">
                  <IconDots size={18} />
                </ActionIcon>
              </Menu.Target>
              <Menu.Dropdown>
                {readable && (
                  <Menu.Item
                    component="a"
                    href={exportUrl(bookId)}
                    download
                    leftSection={<IconFileExport size={14} />}
                  >
                    Export (without audio)
                  </Menu.Item>
                )}
                <Menu.Item
                  color="red"
                  leftSection={<IconTrash size={14} />}
                  disabled={b.status === 'importing'}
                  onClick={() =>
                    modals.openConfirmModal({
                      title: 'Delete book',
                      children: (
                        <Text size="sm">
                          Delete “{b.title}” with all chapters and generated audio? This cannot be
                          undone.
                        </Text>
                      ),
                      labels: { confirm: 'Delete', cancel: 'Cancel' },
                      confirmProps: { color: 'red' },
                      onConfirm: () => remove.mutate(),
                    })
                  }
                >
                  Delete
                </Menu.Item>
              </Menu.Dropdown>
            </Menu>
          </Group>
          {started && (
            <Text size="sm" c="dimmed">
              Reading: {current.title}
            </Text>
          )}
        </Stack>
      </Flex>

      {b.description && (
        <Spoiler maw={720} maxHeight={130} showLabel="Show more" hideLabel="Show less">
          <Text style={{ whiteSpace: 'pre-line' }}>{b.description}</Text>
        </Spoiler>
      )}

      {readable && (
        <Stack gap="xs" maw={720}>
          <Title order={4}>Contents</Title>
          {chapters.error && (
            <Alert color="red" title="Could not load the contents">
              {chapters.error.message}
            </Alert>
          )}
          {chapters.isPending ? (
            <Center py="xl">
              <Loader />
            </Center>
          ) : nothingToRead ? (
            <Text size="sm" c="dimmed">
              {(chapters.data?.items.length ?? 0) > 0
                ? "No chapters to read: all of them are excluded (see the audiobook's chapters)."
                : 'This book has no chapters.'}
            </Text>
          ) : (
            <TableOfContents
              bookId={bookId}
              chapters={toRead}
              volumes={volumes.data ?? []}
              currentId={currentId}
            />
          )}
        </Stack>
      )}
    </Stack>
  );
}
