import { notifications } from '@mantine/notifications';

export function notifyError(title: string, error: unknown) {
  notifications.show({
    color: 'red',
    title,
    message: error instanceof Error ? error.message : String(error),
  });
}

export function notifySuccess(message: string) {
  notifications.show({ color: 'green', message });
}
