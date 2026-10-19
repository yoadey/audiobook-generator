import {
  Alert,
  Badge,
  Card,
  Center,
  Code,
  Group,
  Loader,
  SimpleGrid,
  Stack,
  Text,
  Title,
} from '@mantine/core';
import { useQuery } from '@tanstack/react-query';
import type { ReactNode } from 'react';

import { systemInfoQuery, ttsEnginesQuery } from '../api/client';
import { EngineBudget } from '../components/EngineQuota';

function InfoCard({ title, children }: { title: string; children: ReactNode }) {
  return (
    <Card withBorder padding="lg">
      <Text fw={600} mb="sm">
        {title}
      </Text>
      <Stack gap={6}>{children}</Stack>
    </Card>
  );
}

function Row({ label, children }: { label: string; children: ReactNode }) {
  return (
    <Group justify="space-between" wrap="nowrap" gap="md">
      <Text c="dimmed" size="sm">
        {label}
      </Text>
      <Text size="sm" component="div" ta="right">
        {children}
      </Text>
    </Group>
  );
}

export function SystemPage() {
  const { data, error, isPending } = useQuery(systemInfoQuery);
  const engines = useQuery(ttsEnginesQuery);

  return (
    <Stack>
      <Title order={2}>System</Title>
      {isPending && (
        <Center py="xl">
          <Loader />
        </Center>
      )}
      {error && (
        <Alert color="red" title="Could not load system information">
          {error.message}
        </Alert>
      )}
      {data && (
        <SimpleGrid cols={{ base: 1, md: 3 }}>
          <InfoCard title="Application">
            <Row label="Version">{data.version}</Row>
            <Row label="Commit">
              <Code>{data.commit}</Code>
            </Row>
          </InfoCard>
          <InfoCard title="Database">
            <Row label="Dialect">{data.database.dialect}</Row>
            <Row label="Schema">
              <Code>{data.database.schemaVersion}</Code>
            </Row>
          </InfoCard>
          <InfoCard title="Kokoro">
            {data.tts.configured ? (
              <>
                <Row label="Status">
                  <Badge color={data.tts.reachable ? 'green' : 'red'} variant="light">
                    {data.tts.reachable ? 'reachable' : 'unreachable'}
                  </Badge>
                </Row>
                <Row label="URL">
                  <Code>{data.tts.url}</Code>
                </Row>
                {data.tts.voiceCount !== undefined && (
                  <Row label="Voices">{data.tts.voiceCount}</Row>
                )}
                {data.tts.error && (
                  <Text c="red" size="xs">
                    {data.tts.error}
                  </Text>
                )}
              </>
            ) : (
              <Text size="sm" c="dimmed">
                Not part of this deployment.
              </Text>
            )}
            <Row label="Default narrator">
              <Code>{data.tts.defaultVoice}</Code>
            </Row>
          </InfoCard>
          {(engines.data ?? []).map((e) => (
            <InfoCard key={e.name} title={`Speech engine ${e.name}`}>
              <Row label="Status">
                <Badge color={e.error ? 'red' : 'green'} variant="light">
                  {e.error ? 'unreachable' : e.quota ? 'cloud' : 'reachable'}
                </Badge>
              </Row>
              <Row label="Kind">
                {e.kind}
                {e.variant ? ` (${e.variant})` : ''}
              </Row>
              {e.model && (
                <Row label="Model">
                  <Code>{e.model}</Code>
                </Row>
              )}
              <Row label="Voices">{e.voices.length}</Row>
              <Row label="Emotions">{e.emotions ? 'spoken by the engine' : 'as loudness'}</Row>
              {e.quota && <EngineBudget name="Quota" quota={e.quota} />}
              {e.error && (
                <Text c="red" size="xs">
                  {e.error}
                </Text>
              )}
            </InfoCard>
          ))}
        </SimpleGrid>
      )}
    </Stack>
  );
}
