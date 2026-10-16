import { Button, Stack, Text, Title } from '@mantine/core';
import { Link } from '@tanstack/react-router';

export function NotFound() {
  return (
    <Stack align="flex-start">
      <Title order={2}>Page not found</Title>
      <Text c="dimmed">This page does not exist.</Text>
      <Button component={Link} to="/library" variant="light">
        Back to the library
      </Button>
    </Stack>
  );
}
