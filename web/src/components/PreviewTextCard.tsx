import { Button, Card, Group, Stack, Text, Textarea } from '@mantine/core';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { useState } from 'react';

import { type AppSettings, settingsQuery, updateSettings } from '../api/client';
import { notifyError, notifySuccess } from '../lib/notify';

/** The sample text voice previews say (books and characters may have their own). */
export function PreviewTextCard() {
  const settings = useQuery(settingsQuery);
  if (!settings.data) {
    return null;
  }
  return <PreviewTextForm key={settings.data.previewText} settings={settings.data} />;
}

function PreviewTextForm({ settings }: { settings: AppSettings }) {
  const queryClient = useQueryClient();
  const [text, setText] = useState(settings.previewText);
  const save = useMutation({
    mutationFn: (previewText: string) => updateSettings({ previewText }),
    onSuccess: (s) => {
      notifySuccess('Sample text saved');
      queryClient.setQueryData(settingsQuery.queryKey, s);
    },
    onError: (e) => notifyError('Could not save the sample text', e),
  });
  return (
    <Card withBorder>
      <Stack gap="sm">
        <Text fw={600}>Voice previews</Text>
        <Textarea
          label="Sample text"
          description="What voice previews say. A book can have its own (narrator settings); characters speak quotes from their book or a text of their own."
          placeholder={settings.defaultPreviewText}
          value={text}
          onChange={(e) => setText(e.currentTarget.value)}
          maxLength={500}
          autosize
          minRows={2}
        />
        <Group justify="flex-end">
          {settings.previewText !== '' && (
            <Button variant="subtle" onClick={() => save.mutate('')} loading={save.isPending}>
              Use the built-in text
            </Button>
          )}
          <Button
            onClick={() => save.mutate(text)}
            disabled={text.trim() === settings.previewText}
            loading={save.isPending}
          >
            Save
          </Button>
        </Group>
      </Stack>
    </Card>
  );
}
