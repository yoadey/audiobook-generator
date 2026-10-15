import {
  Alert,
  Badge,
  Button,
  Center,
  Group,
  Loader,
  Stack,
  Table,
  Text,
  Title,
} from '@mantine/core';
import { IconBooks, IconUpload } from '@tabler/icons-react';
import { useQuery } from '@tanstack/react-query';

import { booksQuery } from '../api/client';

export function LibraryPage() {
  const { data, error, isPending } = useQuery(booksQuery());

  return (
    <Stack>
      <Group justify="space-between">
        <Title order={2}>Library</Title>
        {/* EPUB import arrives with milestone M1. */}
        <Button leftSection={<IconUpload size={16} />} disabled>
          Upload EPUB
        </Button>
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
              Uploaded EPUBs will appear here.
            </Text>
          </Stack>
        </Center>
      )}
      {data && data.total > 0 && (
        <Table striped highlightOnHover>
          <Table.Thead>
            <Table.Tr>
              <Table.Th>Title</Table.Th>
              <Table.Th>Author</Table.Th>
              <Table.Th>Status</Table.Th>
            </Table.Tr>
          </Table.Thead>
          <Table.Tbody>
            {data.items.map((book) => (
              <Table.Tr key={book.id}>
                <Table.Td>{book.title}</Table.Td>
                <Table.Td>{book.author}</Table.Td>
                <Table.Td>
                  <Badge variant="light">{book.status}</Badge>
                </Table.Td>
              </Table.Tr>
            ))}
          </Table.Tbody>
        </Table>
      )}
    </Stack>
  );
}
