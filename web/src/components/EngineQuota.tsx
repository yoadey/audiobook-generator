import { Badge, Group, SimpleGrid, Stack, Text } from '@mantine/core';

import type { TTSEngineQuota } from '../api/client';
import { formatNumber, formatUntil } from '../lib/format';
import { Meter } from './Meter';

const stateColor: Record<TTSEngineQuota['state'], string> = {
  ready: 'green',
  waiting: 'yellow',
  exhausted: 'orange',
};

const stateLabel: Record<TTSEngineQuota['state'], string> = {
  ready: 'quota left',
  waiting: 'waiting',
  exhausted: 'quota exhausted',
};

/** Requests left today (undefined when no daily limit is known). */
function requestsLeft(q: TTSEngineQuota): number | undefined {
  const rpd = q.limits.rpd ?? 0;
  if (rpd <= 0) {
    return undefined;
  }
  return Math.max(0, rpd - q.today.requests);
}

/** The state of a cloud engine's quota as a badge. */
export function QuotaBadge({ quota: q }: { quota: TTSEngineQuota }) {
  return (
    <Badge variant="light" color={stateColor[q.state]}>
      {stateLabel[q.state]}
    </Badge>
  );
}

/**
 * A cloud engine's quota today, as this server counted it: Google tells
 * the limits only when one is hit, so the configured ones (or those named
 * in the last 429 answer) are what is shown as left.
 */
export function EngineQuota({ quota: q }: { quota: TTSEngineQuota }) {
  const left = requestsLeft(q);
  return (
    <Stack gap="xs">
      {q.reason && (
        <Text size="sm" c="dimmed">
          {q.reason}
          {q.availableAt && ` — rendering goes on ${formatUntil(q.availableAt)}`}
        </Text>
      )}
      <SimpleGrid cols={{ base: 2, sm: 3 }} spacing="md">
        <Meter label="Requests today" used={q.today.requests} limit={q.limits.rpd ?? 0} />
        <Meter label="Text tokens today" used={q.today.inputTokens} limit={q.limits.tpd ?? 0} />
        <Meter label="Requests / min" used={q.minuteRequests} limit={q.limits.rpm ?? 0} />
      </SimpleGrid>
      <Text size="xs" c="dimmed">
        {left !== undefined
          ? `${formatNumber(left)} requests left today`
          : 'No daily limit known: set limits.rpd as AI Studio shows it'}
        {q.limitsLearned && ' (limits as the engine named them when it refused a request)'} · resets{' '}
        {formatUntil(q.quotaResetAt)} ({q.quotaTimezone})
        {q.today.errors > 0 &&
          ` · ${q.today.errors} failed today${q.today.rateLimited > 0 ? `, ${q.today.rateLimited} rate-limited` : ''}`}
      </Text>
      {q.lastError && q.state === 'ready' && (
        <Text size="xs" c="red" lineClamp={3} title={q.lastError}>
          Last error: {q.lastError}
        </Text>
      )}
    </Stack>
  );
}

/** One line on what a cloud engine can still render today. */
export function EngineBudget({ name, quota: q }: { name: string; quota: TTSEngineQuota }) {
  const left = requestsLeft(q);
  return (
    <Group gap="xs" wrap="wrap">
      <QuotaBadge quota={q} />
      <Text size="xs" c="dimmed">
        {name}:{' '}
        {left !== undefined
          ? `${formatNumber(left)} of ${formatNumber(q.limits.rpd ?? 0)} requests left today`
          : `${formatNumber(q.today.requests)} requests today`}
        {q.state !== 'ready' && q.availableAt
          ? ` · rendering goes on ${formatUntil(q.availableAt)}`
          : ` · resets ${formatUntil(q.quotaResetAt)}`}
      </Text>
    </Group>
  );
}
