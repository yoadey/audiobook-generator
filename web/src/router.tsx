import {
  createRootRoute,
  createRoute,
  createRouter,
  redirect,
  type RouterHistory,
} from '@tanstack/react-router';

import { BookPage } from './routes/BookPage';
import { ChapterPage } from './routes/ChapterPage';
import { CharacterPage } from './routes/CharacterPage';
import { CharactersPage } from './routes/CharactersPage';
import { JobsPage } from './routes/JobsPage';
import { Layout } from './routes/Layout';
import { LibraryPage } from './routes/LibraryPage';
import { ListenPage } from './routes/ListenPage';
import { NotFound } from './routes/NotFound';
import { SettingsPage } from './routes/SettingsPage';
import { SystemPage } from './routes/SystemPage';
import { VoicesPage } from './routes/VoicesPage';

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

const bookRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/books/$bookId',
});

const bookIndexRoute = createRoute({
  getParentRoute: () => bookRoute,
  path: '/',
  component: BookPage,
});

const chapterRoute = createRoute({
  getParentRoute: () => bookRoute,
  path: '/chapters/$chapterId',
  component: ChapterPage,
});

const charactersRoute = createRoute({
  getParentRoute: () => bookRoute,
  path: '/characters',
});

const charactersIndexRoute = createRoute({
  getParentRoute: () => charactersRoute,
  path: '/',
  component: CharactersPage,
});

const characterRoute = createRoute({
  getParentRoute: () => charactersRoute,
  path: '/$characterId',
  component: CharacterPage,
});

const listenRoute = createRoute({
  getParentRoute: () => bookRoute,
  path: '/listen',
  component: ListenPage,
});

const jobsRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/jobs',
  component: JobsPage,
});

const systemRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/system',
  component: SystemPage,
});

const voicesRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/voices',
  component: VoicesPage,
});

const settingsRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/settings',
  component: SettingsPage,
});

const routeTree = rootRoute.addChildren([
  indexRoute,
  libraryRoute,
  bookRoute.addChildren([
    bookIndexRoute,
    chapterRoute,
    charactersRoute.addChildren([charactersIndexRoute, characterRoute]),
    listenRoute,
  ]),
  jobsRoute,
  voicesRoute,
  settingsRoute,
  systemRoute,
]);

export function createAppRouter(history?: RouterHistory) {
  return createRouter({ routeTree, history });
}

declare module '@tanstack/react-router' {
  interface Register {
    router: ReturnType<typeof createAppRouter>;
  }
}
