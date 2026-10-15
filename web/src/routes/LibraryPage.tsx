import {
  Alert,
  Button,
  Card,
  Center,
  FileButton,
  Group,
  Image,
  Loader,
  Progress,
  SimpleGrid,
  Stack,
  Text,
  Title,
} from '@mantine/core';
import { IconBook2, IconBooks, IconUpload } from '@tabler/icons-react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { useState } from 'react';

import { type Book, booksQuery, coverUrl, uploadBook } from '../api/client';
import { formatListeningTime, formatNumber } from '../lib/format';
import { CardLink } from '../components/links';
import { StatusBadge } from '../components/StatusBadge';
import { notifyError, notifySuccess } from '../lib/notify';

function Cover({ book }: { book: Book }) {
  const [failed, setFailed] = useState(false);
  if (!book.hasCover || failed) {
    return (
      <Center h={180} bg="var(--mantine-color-default-hover)">
        <IconBook2 size={48} stroke={1.2} />
      </Center>
    );
  }
  return (
    <Image src={coverUrl(book.id)} h={180} fit="contain" alt="" onError={() => setFailed(true)} />
  );
}

function BookCard({ book }: { book: Book }) {
  return (
    <CardLink withBorder padding="sm" to="/books/$bookId" params={{ bookId: String(book.id) }}>
      <Card.Section>
        <Cover book={book} />
      </Card.Section>
      <Stack gap={4} mt="sm">
        <Group justify="space-between" wrap="nowrap" align="flex-start">
          <Text fw={600} lineClamp={2}>
            {book.title}
          </Text>
          <StatusBadge status={book.status} />
        </Group>
        {book.author && (
          <Text size="sm" c="dimmed" lineClamp={1}>
            {book.author}
          </Text>
        )}
        {book.status === 'importing' && (
          <Progress value={book.progress * 100} animated size="sm" mt={4} />
        )}
        {book.status === 'failed' && book.error && (
          <Text size="xs" c="red" lineClamp={3}>
            {book.error}
          </Text>
        )}
        {book.chapterCount > 0 && (
          <Text size="xs" c="dimmed">
            {formatNumber(book.chapterCount)} chapters · {formatNumber(book.wordCount)} words · ~
            {formatListeningTime(book.wordCount)}
          </Text>
        )}
      </Stack>
    </CardLink>
  );
}

export function LibraryPage() {
  const queryClient = useQueryClient();
  const { data, error, isPending } = useQuery(booksQuery());
  const upload = useMutation({
    mutationFn: uploadBook,
    onSuccess: (book) => {
      notifySuccess(`“${book.title}” uploaded, importing…`);
      return queryClient.invalidateQueries({ queryKey: ['books'] });
    },
    onError: (err) => notifyError('Upload failed', err),
  });

  return (
    <Stack>
      <Group justify="space-between">
        <Title order={2}>Library</Title>
        <FileButton
          onChange={(file) => file && upload.mutate(file)}
          accept=".epub,application/epub+zip"
        >
          {(props) => (
            <Button {...props} leftSection={<IconUpload size={16} />} loading={upload.isPending}>
              Upload EPUB
            </Button>
          )}
        </FileButton>
      </Group>

      {isPending && (
        <Center py="xl">
          <Loader />
        </Center>
      )}
      {error && (
        <Alert color="red" title="Could not load books">
          {error.message}
        </Alert>
      )}
      {data && data.total === 0 && (
        <Center py="xl">
          <Stack align="center" gap="xs">
            <IconBooks size={48} stroke={1.2} />
            <Text fw={500}>No books yet</Text>
            <Text c="dimmed" size="sm">
              Upload a DRM-free EPUB to get started.
            </Text>
          </Stack>
        </Center>
      )}
      {data && data.total > 0 && (
        <SimpleGrid cols={{ base: 1, xs: 2, md: 3, lg: 4 }}>
          {data.items.map((book) => (
            <BookCard key={book.id} book={book} />
          ))}
        </SimpleGrid>
      )}
    </Stack>
  );
}
