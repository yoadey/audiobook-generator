import { Badge, Button, Card, Group, Progress, Stack, Table, Text } from '@mantine/core';
import { IconDownload, IconPackage } from '@tabler/icons-react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';

import {
  assembleDownload,
  type Book,
  type Download,
  downloadFileUrl,
  downloadsQuery,
} from '../api/client';
import { formatDuration, formatNumber } from '../lib/format';
import { notifyError } from '../lib/notify';

const statusColor: Record<Download['status'], string> = {
  none: 'gray',
  queued: 'blue',
  assembling: 'blue',
  ready: 'green',
  failed: 'red',
};

function formatSize(bytes: number): string {
  const mb = bytes / (1 << 20);
  return mb >= 1024 ? `${(mb / 1024).toFixed(1)} GB` : `${Math.max(1, Math.round(mb))} MB`;
}

function State({ d }: { d: Download }) {
  const waiting = d.status === 'queued' && d.renderedCount < d.chapterCount;
  return (
    <Stack gap={2}>
      <Group gap="xs">
        <Badge variant="light" color={statusColor[d.status]}>
          {d.status === 'none' ? 'not built' : d.status}
        </Badge>
        {d.hasFile && d.outdated && (
          <Badge variant="light" color="yellow">
            outdated
          </Badge>
        )}
      </Group>
      {waiting && (
        <Text size="xs" c="dimmed">
          rendering chapters first ({formatNumber(d.renderedCount)} of{' '}
          {formatNumber(d.chapterCount)} ready)
        </Text>
      )}
      {d.status === 'assembling' && (
        <Progress value={(d.job?.progress ?? 0) * 100} size="sm" animated w={120} />
      )}
      {d.status === 'failed' && d.error && (
        <Text size="xs" c="red" lineClamp={2}>
          {d.error}
        </Text>
      )}
    </Stack>
  );
}

/** M4B files with chapter markers, one per volume. */
export function DownloadsCard({ book }: { book: Book }) {
  const queryClient = useQueryClient();
  const downloads = useQuery(downloadsQuery(book.id));
  const build = useMutation({
    mutationFn: (volumeId: number) => assembleDownload(book.id, volumeId),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ['books', book.id, 'downloads'] }),
    onError: (e) => notifyError('Could not build the file', e),
  });

  const ds = downloads.data ?? [];
  if (ds.length === 0) {
    return null;
  }
  return (
    <Card withBorder data-testid="downloads-card">
      <Stack gap="sm">
        <Text fw={600}>Downloads</Text>
        <Text size="sm" c="dimmed">
          One M4B audiobook file with chapter markers per volume. Chapters that are not rendered yet
          are rendered first, after the ones you are listening to.
        </Text>
        <Table.ScrollContainer minWidth={300} type="native">
          <Table>
            <Table.Tbody>
              {ds.map((d) => {
                const busy = d.status === 'queued' || d.status === 'assembling';
                return (
                  <Table.Tr key={d.volumeId}>
                    <Table.Td>
                      <Text size="sm">{d.title}</Text>
                      <Text size="xs" c="dimmed">
                        {formatNumber(d.chapterCount)} chapters
                        {d.hasFile &&
                          d.durationMs !== undefined &&
                          ` · ${formatDuration(d.durationMs)}`}
                        {d.hasFile && d.sizeBytes !== undefined && ` · ${formatSize(d.sizeBytes)}`}
                      </Text>
                    </Table.Td>
                    <Table.Td>
                      <State d={d} />
                    </Table.Td>
                    <Table.Td>
                      <Group gap="xs" justify="flex-end" wrap="nowrap">
                        {(!d.hasFile || d.outdated || d.status === 'failed') && (
                          <Button
                            size="xs"
                            variant="default"
                            leftSection={<IconPackage size={14} />}
                            disabled={busy}
                            loading={build.isPending && build.variables === d.volumeId}
                            onClick={() => build.mutate(d.volumeId)}
                          >
                            {d.hasFile ? 'Rebuild' : 'Build'}
                          </Button>
                        )}
                        {d.hasFile && (
                          <Button
                            size="xs"
                            component="a"
                            href={downloadFileUrl(book.id, d.volumeId)}
                            download
                            leftSection={<IconDownload size={14} />}
                            aria-label={`Download ${d.title}`}
                          >
                            Download
                          </Button>
                        )}
                      </Group>
                    </Table.Td>
                  </Table.Tr>
                );
              })}
            </Table.Tbody>
          </Table>
        </Table.ScrollContainer>
      </Stack>
    </Card>
  );
}
