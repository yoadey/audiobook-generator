import { MantineProvider } from '@mantine/core';
import { ModalsProvider } from '@mantine/modals';
import { Notifications } from '@mantine/notifications';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { RouterProvider } from '@tanstack/react-router';
import { useState } from 'react';

import { createAppRouter } from './router';
import { theme } from './theme';

type AppRouter = ReturnType<typeof createAppRouter>;

export function App({
  router,
  queryClient: client,
}: {
  router?: AppRouter;
  queryClient?: QueryClient;
}) {
  const [queryClient] = useState(
    () => client ?? new QueryClient({ defaultOptions: { queries: { retry: 1 } } }),
  );
  const [appRouter] = useState(() => router ?? createAppRouter());

  return (
    <MantineProvider theme={theme} defaultColorScheme="auto">
      <Notifications position="top-right" />
      <QueryClientProvider client={queryClient}>
        <ModalsProvider>
          <RouterProvider router={appRouter} />
        </ModalsProvider>
      </QueryClientProvider>
    </MantineProvider>
  );
}
