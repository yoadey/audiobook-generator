import { Card, Stack, Switch, Text } from '@mantine/core';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';

import { settingsQuery, updateSettings } from '../api/client';
import { notifyError } from '../lib/notify';

/** "10 minutes", "90 seconds": the idle time in its largest whole unit. */
function idleTime(seconds: number) {
  if (seconds % 3600 === 0) {
    return seconds === 3600 ? '1 hour' : `${seconds / 3600} hours`;
  }
  if (seconds % 60 === 0) {
    return seconds === 60 ? '1 minute' : `${seconds / 60} minutes`;
  }
  return seconds === 1 ? '1 second' : `${seconds} seconds`;
}

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
  const seconds = settings.data.unloadIdleSeconds;
  return (
    <Card withBorder>
      <Stack gap="sm">
        <Text fw={600}>Speech models</Text>
        <Switch
          label="Unload idle models"
          description={
            seconds > 0
              ? `Chatterbox frees its model's memory after ${idleTime(seconds)} without speaking and loads it again (about 10 s) before it speaks next.`
              : 'Turned off by the deployment (ABG_TTS_UNLOAD_IDLE is 0).'
          }
          checked={settings.data.unloadIdleModels && seconds > 0}
          disabled={seconds === 0 || save.isPending}
          onChange={(e) => save.mutate(e.currentTarget.checked)}
        />
      </Stack>
    </Card>
  );
}
