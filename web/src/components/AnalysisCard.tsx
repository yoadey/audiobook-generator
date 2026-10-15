import { Alert, Badge, Button, Card, Group, Progress, Stack, Text } from '@mantine/core';
import { modals } from '@mantine/modals';
import { IconPlayerPause, IconPlayerPlay, IconRefresh, IconUsers } from '@tabler/icons-react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';

import {
  type Analysis,
  analysisQuery,
  type Book,
  llmProvidersQuery,
  pauseAnalysis,
  startAnalysis,
} from '../api/client';
import { formatCompact, formatNumber } from '../lib/format';
import { notifyError } from '../lib/notify';
import { AnchorLink, ButtonLink } from './links';

const statusColor: Record<Analysis['status'], string> = {
  none: 'gray',
  queued: 'blue',
  running: 'blue',
  merging: 'violet',
  paused: 'yellow',
  done: 'green',
  failed: 'red',
};

const statusLabel: Record<Analysis['status'], string> = {
  none: 'not started',
  queued: 'queued',
  running: 'running',
  merging: 'checking duplicates',
  paused: 'paused',
  done: 'done',
  failed: 'failed',
};

/** Days the analysis needs when every enabled provider has a daily request limit. */
function daysEstimate(requests: number, dailyLimits: number[]): number | undefined {
  if (dailyLimits.length === 0 || dailyLimits.some((l) => l <= 0)) {
    return undefined;
  }
  const perDay = dailyLimits.reduce((a, b) => a + b, 0);
  return Math.max(1, Math.ceil(requests / perDay));
}

/** State and controls of a book's character analysis. */
export function AnalysisCard({ book }: { book: Book }) {
  const queryClient = useQueryClient();
  const analysis = useQuery(analysisQuery(book.id));
  const providers = useQuery(llmProvidersQuery);
  const refresh = () => queryClient.invalidateQueries({ queryKey: ['books', book.id] });

  const start = useMutation({
    mutationFn: (restart: boolean) => startAnalysis(book.id, restart),
    onSuccess: refresh,
    onError: (e) => notifyError('Could not start the analysis', e),
  });
  const pause = useMutation({
    mutationFn: () => pauseAnalysis(book.id),
    onSuccess: refresh,
    onError: (e) => notifyError('Could not pause the analysis', e),
  });

  const a = analysis.data;
  if (!a) {
    return null;
  }
  const enabled = (providers.data ?? []).filter((p) => p.enabled);
  const active = a.status === 'queued' || a.status === 'running' || a.status === 'merging';
  const started = a.status !== 'none';
  const days = daysEstimate(
    a.batchesTotal - a.batchesDone,
    enabled.map((p) => p.limits.rpd ?? 0),
  );
  const waitingUntil =
    a.job?.status === 'queued' && a.job.notBefore ? new Date(a.job.notBefore) : undefined;

  const confirmRestart = () =>
    modals.openConfirmModal({
      title: 'Restart the analysis?',
      children: (
        <Text size="sm">
          All {formatNumber(a.characterCount)} characters, including your corrections, are deleted
          and the whole book is analysed again, which uses the LLM quota again.
        </Text>
      ),
      labels: { confirm: 'Restart', cancel: 'Cancel' },
      confirmProps: { color: 'red' },
      onConfirm: () => start.mutate(true),
    });

  return (
    <Card withBorder data-testid="analysis-card">
      <Stack gap="sm">
        <Group justify="space-between">
          <Group gap="xs">
            <Text fw={600}>Character analysis</Text>
            <Badge variant="light" color={statusColor[a.status]}>
              {statusLabel[a.status]}
            </Badge>
          </Group>
          {a.characterCount > 0 && (
            <ButtonLink
              to="/books/$bookId/characters"
              params={{ bookId: String(book.id) }}
              variant="light"
              size="xs"
              leftSection={<IconUsers size={14} />}
            >
              {formatNumber(a.characterCount)} characters
            </ButtonLink>
          )}
        </Group>

        {!started && (
          <Text size="sm" c="dimmed">
            Reads the whole book once to find every character (≈ {formatNumber(a.batchesTotal)} LLM
            requests with ≈ {formatCompact(a.estimatedTokens)} tokens
            {days !== undefined &&
              `; about ${days} day${days === 1 ? '' : 's'} with your providers' daily limits`}
            ). It pauses automatically when the free quotas run out.
          </Text>
        )}
        {enabled.length === 0 && !started && (
          <Alert color="yellow" variant="light">
            No LLM provider is enabled yet.{' '}
            <AnchorLink to="/settings" size="sm">
              Add one in the settings
            </AnchorLink>{' '}
            first.
          </Alert>
        )}

        {started && a.batchesTotal > 0 && (
          <Stack gap={4}>
            <Progress
              value={(a.batchesDone / a.batchesTotal) * 100}
              animated={active}
              aria-label="Analysis progress"
            />
            <Text size="xs" c="dimmed">
              {formatNumber(a.batchesDone)} of {formatNumber(a.batchesTotal)} requests ·{' '}
              {formatCompact(a.inputTokens + a.outputTokens)} tokens used
              {days !== undefined &&
                a.status !== 'done' &&
                ` · ~${days} day${days === 1 ? '' : 's'} left`}
            </Text>
          </Stack>
        )}
        {waitingUntil && (
          <Text size="sm" c="dimmed">
            Waiting for LLM quota until {waitingUntil.toLocaleString()}
            {a.job?.error && `: ${a.job.error}`}
          </Text>
        )}
        {a.status === 'failed' && a.error && (
          <Alert color="red" title="The analysis stopped">
            {a.error}
          </Alert>
        )}

        <Group>
          {!started && (
            <Button
              leftSection={<IconPlayerPlay size={16} />}
              disabled={enabled.length === 0}
              loading={start.isPending}
              onClick={() => start.mutate(false)}
            >
              Start analysis
            </Button>
          )}
          {(a.status === 'paused' || a.status === 'failed') && (
            <Button
              leftSection={<IconPlayerPlay size={16} />}
              loading={start.isPending}
              onClick={() => start.mutate(false)}
            >
              Resume
            </Button>
          )}
          {active && (
            <Button
              variant="default"
              leftSection={<IconPlayerPause size={16} />}
              loading={pause.isPending}
              onClick={() => pause.mutate()}
            >
              Pause
            </Button>
          )}
          {started && !active && (
            <Button
              variant="subtle"
              color="red"
              leftSection={<IconRefresh size={16} />}
              onClick={confirmRestart}
            >
              Restart
            </Button>
          )}
        </Group>
      </Stack>
    </Card>
  );
}
