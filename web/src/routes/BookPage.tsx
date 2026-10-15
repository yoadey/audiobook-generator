import {
  Alert,
  Breadcrumbs,
  Button,
  Center,
  Group,
  Loader,
  Progress,
  SegmentedControl,
  Select,
  Stack,
  Text,
  TextInput,
  Title,
} from '@mantine/core';
import { modals } from '@mantine/modals';
import { IconHeadphones, IconSearch, IconTrash, IconWaveSine } from '@tabler/icons-react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { useNavigate, useParams } from '@tanstack/react-router';
import { useMemo, useState } from 'react';

import {
  bookQuery,
  chaptersQuery,
  deleteBook,
  renderBookChapters,
  volumesQuery,
} from '../api/client';
import { ChapterTable } from '../components/ChapterTable';
import { AnchorLink, ButtonLink } from '../components/links';
import { NarratorSettings } from '../components/NarratorSettings';
import { StatusBadge } from '../components/StatusBadge';
import { formatListeningTime, formatNumber } from '../lib/format';
import { notifyError, notifySuccess } from '../lib/notify';

type Filter = 'all' | 'narrated' | 'excluded';

export function BookPage() {
  const { bookId: raw } = useParams({ from: '/books/$bookId/' });
  const bookId = Number(raw);
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const book = useQuery(bookQuery(bookId));
  const imported = book.data !== undefined && book.data.status !== 'importing';
  const volumes = useQuery({ ...volumesQuery(bookId), enabled: imported });
  const chapters = useQuery({ ...chaptersQuery(bookId), enabled: imported });

  const [volume, setVolume] = useState<string | null>(null);
  const [filter, setFilter] = useState<Filter>('all');
  const [search, setSearch] = useState('');

  const visible = useMemo(() => {
    const q = search.trim().toLowerCase();
    return (chapters.data?.items ?? []).filter(
      (c) =>
        (volume === null || String(c.volumeId) === volume) &&
        (filter === 'all' || (filter === 'excluded') === c.excluded) &&
        (q === '' || c.title.toLowerCase().includes(q)),
    );
  }, [chapters.data, volume, filter, search]);

  const renderNext = useMutation({
    mutationFn: () => renderBookChapters(bookId, 10),
    onSuccess: (r) => {
      notifySuccess(r.queued ? `${r.queued} chapters queued for rendering` : 'Nothing to render');
      return queryClient.invalidateQueries({ queryKey: ['books', bookId] });
    },
    onError: (err) => notifyError('Could not queue rendering', err),
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
  const narrated = (chapters.data?.items ?? []).filter((c) => !c.excluded);
  const narratedWords = narrated.reduce((sum, c) => sum + c.wordCount, 0);

  return (
    <Stack>
      <Breadcrumbs>
        <AnchorLink to="/library" size="sm">
          Library
        </AnchorLink>
        <Text size="sm">{b.title}</Text>
      </Breadcrumbs>

      <Group justify="space-between" align="flex-start">
        <Stack gap={4}>
          <Group gap="sm">
            <Title order={2}>{b.title}</Title>
            <StatusBadge status={b.status} />
          </Group>
          {b.author && <Text c="dimmed">{b.author}</Text>}
          {b.chapterCount > 0 && (
            <Text size="sm" c="dimmed">
              {formatNumber(b.chapterCount)} chapters · {formatNumber(b.wordCount)} words ·
              narrated: {formatNumber(narrated.length)} chapters, ~
              {formatListeningTime(narratedWords)}
            </Text>
          )}
        </Stack>
        <Group>
          {imported && b.status !== 'failed' && (
            <>
              <ButtonLink
                to="/books/$bookId/listen"
                params={{ bookId: String(bookId) }}
                leftSection={<IconHeadphones size={16} />}
              >
                Listen
              </ButtonLink>
              <Button
                variant="default"
                leftSection={<IconWaveSine size={16} />}
                loading={renderNext.isPending}
                onClick={() => renderNext.mutate()}
              >
                Render next 10
              </Button>
            </>
          )}
          <Button
            variant="subtle"
            color="red"
            leftSection={<IconTrash size={16} />}
            disabled={b.status === 'importing'}
            onClick={() =>
              modals.openConfirmModal({
                title: 'Delete book',
                children: (
                  <Text size="sm">
                    Delete “{b.title}” with all chapters and generated audio? This cannot be undone.
                  </Text>
                ),
                labels: { confirm: 'Delete', cancel: 'Cancel' },
                confirmProps: { color: 'red' },
                onConfirm: () => remove.mutate(),
              })
            }
          >
            Delete
          </Button>
        </Group>
      </Group>

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

      {imported && b.status !== 'failed' && (
        <>
          <NarratorSettings key={`${b.narratorVoice}:${b.narratorSpeed}:${b.lookahead}`} book={b} />
          <Group>
            <TextInput
              placeholder="Search chapters"
              leftSection={<IconSearch size={14} />}
              value={search}
              onChange={(e) => setSearch(e.currentTarget.value)}
              w={240}
            />
            {(volumes.data?.length ?? 0) > 0 && (
              <Select
                placeholder="All volumes"
                clearable
                value={volume}
                onChange={setVolume}
                data={(volumes.data ?? []).map((v) => ({
                  value: String(v.id),
                  label: `${v.title} (${v.chapterCount})`,
                }))}
                w={260}
              />
            )}
            <SegmentedControl
              value={filter}
              onChange={(v) => setFilter(v as Filter)}
              data={[
                { value: 'all', label: 'All' },
                { value: 'narrated', label: 'Narrated' },
                { value: 'excluded', label: 'Excluded' },
              ]}
            />
          </Group>
          {chapters.error && (
            <Alert color="red" title="Could not load chapters">
              {chapters.error.message}
            </Alert>
          )}
          {chapters.isPending ? (
            <Center py="xl">
              <Loader />
            </Center>
          ) : (
            <ChapterTable bookId={bookId} chapters={visible} />
          )}
        </>
      )}
    </Stack>
  );
}
