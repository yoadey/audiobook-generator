import {
  createRootRoute,
  createRoute,
  createRouter,
  redirect,
  type RouterHistory,
} from '@tanstack/react-router';

import { Layout } from './routes/Layout';
import { LibraryPage } from './routes/LibraryPage';
import { NotFound } from './routes/NotFound';
import { SystemPage } from './routes/SystemPage';

const rootRoute = createRootRoute({
  component: Layout,
  notFoundComponent: NotFound,
});

const indexRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/',
  beforeLoad: () => {
    throw redirect({ to: '/library' });
  },
});

const libraryRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/library',
  component: LibraryPage,
});

const systemRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/system',
  component: SystemPage,
});

const routeTree = rootRoute.addChildren([indexRoute, libraryRoute, systemRoute]);

export function createAppRouter(history?: RouterHistory) {
  return createRouter({ routeTree, history });
}

declare module '@tanstack/react-router' {
  interface Register {
    router: ReturnType<typeof createAppRouter>;
  }
}
