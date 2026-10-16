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

import { systemInfoQuery } from '../api/client';

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
          <InfoCard title="Text-to-speech">
            <Row label="Status">
              <Badge color={data.tts.reachable ? 'green' : 'red'} variant="light">
                {data.tts.reachable ? 'reachable' : 'unreachable'}
              </Badge>
            </Row>
            <Row label="URL">
              <Code>{data.tts.url}</Code>
            </Row>
            {data.tts.voiceCount !== undefined && <Row label="Voices">{data.tts.voiceCount}</Row>}
            {data.tts.error && (
              <Text c="red" size="xs">
                {data.tts.error}
              </Text>
            )}
          </InfoCard>
        </SimpleGrid>
      )}
    </Stack>
  );
}
