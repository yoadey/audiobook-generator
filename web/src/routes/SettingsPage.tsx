import { Alert, Button, Center, Group, Loader, Modal, Stack, Text, Title } from '@mantine/core';
import { IconAlertTriangle, IconPlus } from '@tabler/icons-react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { useState } from 'react';

import {
  createLLMProvider,
  type LLMProvider,
  type LLMProviderInput,
  llmProvidersQuery,
  updateLLMProvider,
} from '../api/client';
import { LLMProviderCard } from '../components/LLMProviderCard';
import { LLMProviderForm } from '../components/LLMProviderForm';
import { notifyError, notifySuccess } from '../lib/notify';

type Editing = { mode: 'create' } | { mode: 'edit'; provider: LLMProvider } | null;

export function SettingsPage() {
  const queryClient = useQueryClient();
  const providers = useQuery(llmProvidersQuery);
  const [editing, setEditing] = useState<Editing>(null);
  const refresh = () => queryClient.invalidateQueries({ queryKey: ['llm', 'providers'] });

  const save = useMutation({
    mutationFn: (body: LLMProviderInput) =>
      editing?.mode === 'edit'
        ? updateLLMProvider(editing.provider.id, body)
        : createLLMProvider(body),
    onSuccess: (p) => {
      notifySuccess(`Provider ${p.name} saved`);
      setEditing(null);
      return refresh();
    },
    onError: (e) => notifyError('Could not save the provider', e),
  });

  const move = useMutation({
    mutationFn: async ({ p, other }: { p: LLMProvider; other: LLMProvider }) => {
      // Swap positions; equal positions (ordered by creation) need a nudge.
      const up = other.position < p.position || (other.position === p.position && other.id < p.id);
      const mine = other.position === p.position ? p.position + (up ? -1 : 1) : other.position;
      await updateLLMProvider(p.id, { position: mine });
      if (other.position !== p.position) {
        await updateLLMProvider(other.id, { position: p.position });
      }
    },
    onSettled: refresh,
    onError: (e) => notifyError('Could not reorder providers', e),
  });

  const list = providers.data ?? [];
  const editable = list.filter((p) => p.source === 'ui');
  const neighbour = (p: LLMProvider, dir: -1 | 1) => editable[editable.indexOf(p) + dir];

  return (
    <Stack>
      <Title order={2}>Settings</Title>
      <Group justify="space-between" align="flex-end">
        <Stack gap={2}>
          <Title order={3}>LLM providers</Title>
          <Text size="sm" c="dimmed" maw={720}>
            Used for the character analysis and speaker attribution. Providers are tried from top to
            bottom: when one reaches its rate limit or daily quota, the next one takes over; when
            all are exhausted, jobs wait until a quota resets. Any OpenAI-compatible endpoint works.
          </Text>
        </Stack>
        <Button leftSection={<IconPlus size={16} />} onClick={() => setEditing({ mode: 'create' })}>
          Add provider
        </Button>
      </Group>

      <Alert color="yellow" variant="light" icon={<IconAlertTriangle size={18} />}>
        Book text is sent to these providers. Free tiers often allow the provider to use requests
        for training; use a paid tier or a local model if that matters to you.
      </Alert>

      {providers.isPending && (
        <Center py="xl">
          <Loader />
        </Center>
      )}
      {providers.error && (
        <Alert color="red" title="Could not load providers">
          {providers.error.message}
        </Alert>
      )}
      {providers.data && list.length === 0 && (
        <Text c="dimmed" ta="center" py="xl">
          No providers yet. Add one here or in the Helm values (llm.providers).
        </Text>
      )}
      {list.map((p) => (
        <LLMProviderCard
          key={p.id}
          provider={p}
          canMoveUp={p.source === 'ui' && !!neighbour(p, -1) && !move.isPending}
          canMoveDown={p.source === 'ui' && !!neighbour(p, 1) && !move.isPending}
          onMove={(dir) => {
            const other = neighbour(p, dir);
            if (other) {
              move.mutate({ p, other });
            }
          }}
          onEdit={() => setEditing({ mode: 'edit', provider: p })}
        />
      ))}

      <Modal
        opened={editing !== null}
        onClose={() => setEditing(null)}
        title={editing?.mode === 'edit' ? `Edit ${editing.provider.name}` : 'Add LLM provider'}
        size="lg"
      >
        {editing && (
          <LLMProviderForm
            key={editing.mode === 'edit' ? editing.provider.id : 'new'}
            provider={editing.mode === 'edit' ? editing.provider : undefined}
            submitting={save.isPending}
            onSubmit={(body) => save.mutate(body)}
          />
        )}
      </Modal>
    </Stack>
  );
}
