import { Card, Stack, Switch, Text } from '@mantine/core';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';

import { settingsQuery, updateSettings } from '../api/client';
import { notifyError } from '../lib/notify';

/** Whether idle Chatterbox models are unloaded to free memory. */
export function UnloadModelsCard() {
  const queryClient = useQueryClient();
  const settings = useQuery(settingsQuery);
  const save = useMutation({
    mutationFn: (unloadIdleModels: boolean) => updateSettings({ unloadIdleModels }),
    onSuccess: (s) => queryClient.setQueryData(settingsQuery.queryKey, s),
    onError: (e) => notifyError('Could not save the setting', e),
  });
  if (!settings.data) {
    return null;
  }
  const minutes = settings.data.unloadIdleMinutes;
  return (
    <Card withBorder>
      <Stack gap="sm">
        <Text fw={600}>Speech models</Text>
        <Switch
          label="Unload idle models"
          description={
            minutes > 0
              ? `Chatterbox frees its model's memory after ${minutes} minutes without speaking and loads it again (about 10 s) before it speaks next.`
              : 'Turned off by the deployment (ABG_TTS_UNLOAD_IDLE is 0).'
          }
          checked={settings.data.unloadIdleModels && minutes > 0}
          disabled={minutes === 0 || save.isPending}
          onChange={(e) => save.mutate(e.currentTarget.checked)}
        />
      </Stack>
    </Card>
  );
}
