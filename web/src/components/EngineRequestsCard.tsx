import { Button, Card, Group, NumberInput, Stack, Switch, Text } from '@mantine/core';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { useState } from 'react';

import { type TTSEngine, ttsEnginesQuery, updateTTSEngine } from '../api/client';
import { notifyError } from '../lib/notify';

/** How long one engine's requests get. */
function EngineRequests({ engine: e }: { engine: TTSEngine }) {
  const queryClient = useQueryClient();
  const [maxChars, setMaxChars] = useState<number | string>(e.requests.maxChars);
  const save = useMutation({
    mutationFn: (body: Parameters<typeof updateTTSEngine>[1]) => updateTTSEngine(e.name, body),
    onSuccess: (updated) => {
      queryClient.setQueryData(ttsEnginesQuery.queryKey, (all) =>
        all?.map((x) => (x.name === updated.name ? updated : x)),
      );
      setMaxChars(updated.requests.maxChars);
    },
    onError: (err) => notifyError('Could not save the setting', err),
  });
  const valid = typeof maxChars === 'number' && maxChars >= 100 && maxChars <= e.maxChars;
  const changed = valid && maxChars !== e.requests.maxChars;
  return (
    <Stack gap="xs" data-testid={`engine-requests-${e.name}`}>
      <Text fw={500}>
        {e.name}
        {e.quota && (
          <Text span size="xs" c="dimmed">
            {' '}
            · its quota counts requests
          </Text>
        )}
      </Text>
      <Switch
        label="Read across paragraphs"
        description="Consecutive paragraphs in one voice are spoken in one request, with the engine's own pause between them. Off: only paragraphs the chapter preparation found to flow on."
        checked={e.requests.joinParagraphs}
        disabled={save.isPending}
        onChange={(ev) => save.mutate({ joinParagraphs: ev.currentTarget.checked })}
      />
      <Group align="flex-end" gap="xs">
        <NumberInput
          label="Longest request"
          description="characters of text in one request"
          min={100}
          max={e.maxChars}
          step={500}
          value={maxChars}
          onChange={setMaxChars}
          error={!valid && `100 to ${e.maxChars}`}
          w={220}
        />
        <Button
          variant="light"
          disabled={!changed}
          loading={save.isPending}
          onClick={() => valid && save.mutate({ maxChars })}
        >
          Save
        </Button>
        {e.requestsCustom && (
          <Button
            variant="subtle"
            disabled={save.isPending}
            onClick={() => save.mutate({ reset: true })}
          >
            Reset to {e.defaults.maxChars} characters
            {e.defaults.joinParagraphs ? ', across paragraphs' : ''}
          </Button>
        )}
      </Group>
    </Stack>
  );
}

/**
 * How long the requests of the engines besides Kokoro get: longer
 * requests mean fewer of them, which matters for an engine with a quota
 * (Gemini).
 */
export function EngineRequestsCard() {
  const engines = useQuery(ttsEnginesQuery);
  if (!engines.data?.length) {
    return null;
  }
  return (
    <Card withBorder>
      <Stack gap="md">
        <Stack gap={2}>
          <Text fw={600}>Speech requests</Text>
          <Text size="sm" c="dimmed">
            The text of a voice is sent in pieces of up to this length. Longer pieces need fewer
            requests. Every change of voice or emotion still starts a new request, so a book without
            character voices needs the fewest. Applies to chapters rendered from now on; a chapter
            in the middle of rendering speaks its remaining text anew.
          </Text>
        </Stack>
        {engines.data.map((e) => (
          <EngineRequests key={`${e.name}:${e.requests.maxChars}`} engine={e} />
        ))}
      </Stack>
    </Card>
  );
}
