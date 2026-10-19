import {
  ActionIcon,
  Alert,
  Badge,
  Button,
  Card,
  Code,
  Group,
  SimpleGrid,
  Stack,
  Switch,
  Text,
  Tooltip,
} from '@mantine/core';
import {
  IconArrowDown,
  IconArrowUp,
  IconPencil,
  IconPlugConnected,
  IconTrash,
} from '@tabler/icons-react';
import { modals } from '@mantine/modals';
import { useMutation, useQueryClient } from '@tanstack/react-query';

import {
  deleteLLMProvider,
  type LLMProvider,
  testLLMProvider,
  updateLLMProvider,
} from '../api/client';
import { formatNumber, formatUntil } from '../lib/format';
import { notifyError } from '../lib/notify';
import { Meter } from './Meter';

const stateColor: Record<LLMProvider['status']['state'], string> = {
  ready: 'green',
  waiting: 'yellow',
  exhausted: 'orange',
  disabled: 'gray',
  no_key: 'red',
};

const stateLabel: Record<LLMProvider['status']['state'], string> = {
  ready: 'ready',
  waiting: 'waiting',
  exhausted: 'quota exhausted',
  disabled: 'disabled',
  no_key: 'no API key',
};

type Props = {
  provider: LLMProvider;
  canMoveUp: boolean;
  canMoveDown: boolean;
  onMove: (direction: -1 | 1) => void;
  onEdit: () => void;
};

export function LLMProviderCard({ provider: p, canMoveUp, canMoveDown, onMove, onEdit }: Props) {
  const queryClient = useQueryClient();
  const refresh = () => queryClient.invalidateQueries({ queryKey: ['llm', 'providers'] });
  const fromConfig = p.source === 'config';
  const s = p.status;

  const test = useMutation({
    mutationFn: () => testLLMProvider(p.id),
    onSettled: refresh,
    onError: (e) => notifyError('Test failed', e),
  });
  const toggle = useMutation({
    mutationFn: (enabled: boolean) => updateLLMProvider(p.id, { enabled }),
    onSuccess: refresh,
    onError: (e) => notifyError('Could not change the provider', e),
  });
  const remove = useMutation({
    mutationFn: () => deleteLLMProvider(p.id),
    onSuccess: refresh,
    onError: (e) => notifyError('Could not delete the provider', e),
  });

  return (
    <Card withBorder padding="md" data-testid={`provider-${p.name}`}>
      <Stack gap="sm">
        <Group justify="space-between" wrap="nowrap" align="flex-start">
          <Stack gap={2} style={{ minWidth: 0 }}>
            <Group gap="xs">
              <Text fw={600}>{p.name}</Text>
              <Badge variant="light" color={stateColor[s.state]}>
                {stateLabel[s.state]}
              </Badge>
              {fromConfig && (
                <Tooltip label="Defined in the Helm values (llm.providers); read-only here">
                  <Badge variant="outline" color="gray">
                    Helm values
                  </Badge>
                </Tooltip>
              )}
            </Group>
            <Text size="sm">
              <Code>{p.model}</Code>{' '}
              <Text span size="xs" c="dimmed" style={{ wordBreak: 'break-all' }}>
                {p.baseUrl}
              </Text>
            </Text>
          </Stack>
          <Group gap={4} wrap="nowrap">
            {!fromConfig && (
              <>
                <ActionIcon
                  variant="subtle"
                  aria-label={`Move ${p.name} up`}
                  disabled={!canMoveUp}
                  onClick={() => onMove(-1)}
                >
                  <IconArrowUp size={16} />
                </ActionIcon>
                <ActionIcon
                  variant="subtle"
                  aria-label={`Move ${p.name} down`}
                  disabled={!canMoveDown}
                  onClick={() => onMove(1)}
                >
                  <IconArrowDown size={16} />
                </ActionIcon>
                <ActionIcon variant="subtle" aria-label={`Edit ${p.name}`} onClick={onEdit}>
                  <IconPencil size={16} />
                </ActionIcon>
                <ActionIcon
                  variant="subtle"
                  color="red"
                  aria-label={`Delete ${p.name}`}
                  loading={remove.isPending}
                  onClick={() =>
                    modals.openConfirmModal({
                      title: `Delete ${p.name}?`,
                      children: (
                        <Text size="sm">The provider and its usage history are removed.</Text>
                      ),
                      labels: { confirm: 'Delete', cancel: 'Cancel' },
                      confirmProps: { color: 'red' },
                      onConfirm: () => remove.mutate(),
                    })
                  }
                >
                  <IconTrash size={16} />
                </ActionIcon>
              </>
            )}
          </Group>
        </Group>

        {s.reason && (
          <Text size="sm" c={s.state === 'no_key' ? 'red' : 'dimmed'}>
            {s.reason}
            {s.availableAt && ` — available again ${formatUntil(s.availableAt)}`}
          </Text>
        )}

        <SimpleGrid cols={{ base: 2, sm: 4 }} spacing="md">
          <Meter label="Requests today" used={s.today.requests} limit={p.limits.rpd ?? 0} />
          <Meter
            label="Tokens today"
            used={s.today.inputTokens + s.today.outputTokens}
            limit={p.limits.tpd ?? 0}
          />
          <Meter label="Requests / min" used={s.minuteRequests} limit={p.limits.rpm ?? 0} />
          <Meter label="Tokens / min" used={s.minuteTokens} limit={p.limits.tpm ?? 0} />
        </SimpleGrid>
        <Text size="xs" c="dimmed">
          Daily quota resets {formatUntil(s.quotaResetAt)} ({p.quotaTimezone})
          {s.today.errors > 0 &&
            ` · ${s.today.errors} failed today${s.today.rateLimited > 0 ? `, ${s.today.rateLimited} rate-limited` : ''}`}
        </Text>

        {/* A failing provider already shows the error as its reason. */}
        {s.lastError && !s.reason?.startsWith('failing:') && (
          <Text size="xs" c="red" lineClamp={3} title={s.lastError}>
            Last error: {s.lastError}
          </Text>
        )}

        {test.data && (
          <Alert
            color={test.data.ok ? 'green' : 'red'}
            title={test.data.ok ? 'Test passed' : 'Test failed'}
            withCloseButton
            onClose={() => test.reset()}
          >
            <Text size="sm">{test.data.message}</Text>
            {test.data.latencyMs !== undefined && (
              <Text size="xs" c="dimmed">
                {test.data.model} · {formatNumber(test.data.latencyMs)} ms ·{' '}
                {test.data.inputTokens ?? 0} in / {test.data.outputTokens ?? 0} out tokens
              </Text>
            )}
          </Alert>
        )}

        <Group justify="space-between">
          <Switch
            label="Enabled"
            checked={p.enabled}
            disabled={fromConfig || toggle.isPending}
            onChange={(e) => toggle.mutate(e.currentTarget.checked)}
          />
          <Button
            variant="light"
            size="xs"
            leftSection={<IconPlugConnected size={14} />}
            loading={test.isPending}
            onClick={() => test.mutate()}
          >
            Test
          </Button>
        </Group>
      </Stack>
    </Card>
  );
}
