import { Group, Progress, Stack, Text } from '@mantine/core';

import { formatCompact, formatNumber } from '../lib/format';

/** "used / limit" with a bar, or just the count when there is no limit. */
export function Meter({ label, used, limit }: { label: string; used: number; limit: number }) {
  const pct = limit > 0 ? Math.min(100, (used / limit) * 100) : 0;
  const color = pct >= 90 ? 'red' : pct >= 70 ? 'yellow' : 'teal';
  return (
    <Stack gap={4}>
      <Group justify="space-between" gap="xs" wrap="nowrap">
        <Text size="xs" c="dimmed" style={{ whiteSpace: 'nowrap' }}>
          {label}
        </Text>
        <Text size="xs" ff="monospace" style={{ whiteSpace: 'nowrap' }}>
          {limit > 0 ? `${formatCompact(used)} / ${formatCompact(limit)}` : formatCompact(used)}
        </Text>
      </Group>
      {limit > 0 ? (
        <Progress
          value={pct}
          color={color}
          size="sm"
          aria-label={`${label}: ${formatNumber(used)} of ${formatNumber(limit)}`}
        />
      ) : (
        <Text size="xs" c="dimmed">
          no limit
        </Text>
      )}
    </Stack>
  );
}
