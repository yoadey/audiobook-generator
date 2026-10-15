import { MantineProvider } from '@mantine/core';
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
      <QueryClientProvider client={queryClient}>
        <RouterProvider router={appRouter} />
      </QueryClientProvider>
    </MantineProvider>
  );
}
