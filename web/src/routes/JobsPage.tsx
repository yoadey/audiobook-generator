import {
  ActionIcon,
  Alert,
  Badge,
  Center,
  Group,
  Loader,
  Progress,
  SegmentedControl,
  Stack,
  Table,
  Text,
  Title,
  Tooltip,
} from '@mantine/core';
import { IconPlayerStop, IconRefresh } from '@tabler/icons-react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { useState } from 'react';

import { cancelJob, type Job, jobsQuery, retryJob } from '../api/client';
import { notifyError } from '../lib/notify';

const statusColor: Record<Job['status'], string> = {
  queued: 'gray',
  running: 'blue',
  done: 'green',
  failed: 'red',
  canceled: 'yellow',
};

type Filter = 'active' | 'failed' | 'all';
const filters: Record<Filter, Job['status'][] | undefined> = {
  active: ['queued', 'running'],
  failed: ['failed'],
  all: undefined,
};

function when(iso?: string) {
  return iso ? new Date(iso).toLocaleString() : '';
}

export function JobsPage() {
  const [filter, setFilter] = useState<Filter>('active');
  const queryClient = useQueryClient();
  const jobs = useQuery(jobsQuery(filters[filter]));
  const onSuccess = () => queryClient.invalidateQueries({ queryKey: ['jobs'] });
  const retry = useMutation({
    mutationFn: retryJob,
    onSuccess,
    onError: (e) => notifyError('Retry failed', e),
  });
  const cancel = useMutation({
    mutationFn: cancelJob,
    onSuccess,
    onError: (e) => notifyError('Cancel failed', e),
  });

  return (
    <Stack>
      <Group justify="space-between">
        <Title order={2}>Jobs</Title>
        <SegmentedControl
          value={filter}
          onChange={(v) => setFilter(v as Filter)}
          data={[
            { value: 'active', label: 'Active' },
            { value: 'failed', label: 'Failed' },
            { value: 'all', label: 'All' },
          ]}
        />
      </Group>
      {jobs.isPending && (
        <Center py="xl">
          <Loader />
        </Center>
      )}
      {jobs.error && (
        <Alert color="red" title="Could not load jobs">
          {jobs.error.message}
        </Alert>
      )}
      {jobs.data && jobs.data.items.length === 0 && (
        <Text c="dimmed" ta="center" py="xl">
          No jobs.
        </Text>
      )}
      {jobs.data && jobs.data.items.length > 0 && (
        <Table.ScrollContainer minWidth={600} type="native">
          <Table striped>
            <Table.Thead>
              <Table.Tr>
                <Table.Th>#</Table.Th>
                <Table.Th>Type</Table.Th>
                <Table.Th>Book / chapter</Table.Th>
                <Table.Th>Status</Table.Th>
                <Table.Th>Progress</Table.Th>
                <Table.Th>Attempts</Table.Th>
                <Table.Th>Created</Table.Th>
                <Table.Th />
              </Table.Tr>
            </Table.Thead>
            <Table.Tbody>
              {jobs.data.items.map((j) => (
                <Table.Tr key={j.id}>
                  <Table.Td>{j.id}</Table.Td>
                  <Table.Td>{j.type}</Table.Td>
                  <Table.Td>
                    {j.bookId ?? '–'}
                    {j.chapterId ? ` / ${j.chapterId}` : ''}
                  </Table.Td>
                  <Table.Td>
                    <Tooltip label={j.error} disabled={!j.error} multiline w={360}>
                      <Badge variant="light" color={statusColor[j.status]}>
                        {j.status}
                      </Badge>
                    </Tooltip>
                  </Table.Td>
                  <Table.Td w={140}>
                    {j.status === 'running' ? (
                      <Progress value={j.progress * 100} animated size="sm" />
                    ) : j.notBefore ? (
                      <Tooltip label={j.error} disabled={!j.error} multiline w={360}>
                        <Text size="xs" c="dimmed">
                          waits until {when(j.notBefore)}
                        </Text>
                      </Tooltip>
                    ) : (
                      <Text size="xs" c="dimmed">
                        {j.status === 'done' ? '100%' : ''}
                      </Text>
                    )}
                  </Table.Td>
                  <Table.Td>
                    {j.attempts}/{j.maxAttempts}
                  </Table.Td>
                  <Table.Td>
                    <Text size="xs">{when(j.createdAt)}</Text>
                  </Table.Td>
                  <Table.Td>
                    {(j.status === 'failed' || j.status === 'canceled') && (
                      <ActionIcon
                        variant="subtle"
                        onClick={() => retry.mutate(j.id)}
                        aria-label={`Retry job ${j.id}`}
                      >
                        <IconRefresh size={16} />
                      </ActionIcon>
                    )}
                    {j.status === 'queued' && (
                      <ActionIcon
                        variant="subtle"
                        color="red"
                        onClick={() => cancel.mutate(j.id)}
                        aria-label={`Cancel job ${j.id}`}
                      >
                        <IconPlayerStop size={16} />
                      </ActionIcon>
                    )}
                  </Table.Td>
                </Table.Tr>
              ))}
            </Table.Tbody>
          </Table>
        </Table.ScrollContainer>
      )}
    </Stack>
  );
}
