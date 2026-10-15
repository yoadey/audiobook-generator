import { QueryClient } from '@tanstack/react-query';
import { createMemoryHistory } from '@tanstack/react-router';
import { render, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';

import { App } from './App';
import { createAppRouter } from './router';

function stubApi(routes: Record<string, unknown>) {
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: Request | string) => {
      const url = new URL(typeof input === 'string' ? input : input.url, 'http://localhost');
      const body = routes[url.pathname];
      if (body === undefined) {
        return new Response(JSON.stringify({ message: 'not found' }), { status: 404 });
      }
      return new Response(JSON.stringify(body), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      });
    }),
  );
}

function renderAt(path: string) {
  const router = createAppRouter(createMemoryHistory({ initialEntries: [path] }));
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(<App router={router} queryClient={queryClient} />);
}

describe('App', () => {
  it('redirects to the library and shows the empty state', async () => {
    stubApi({ '/api/books': { items: [], total: 0 } });
    renderAt('/');
    expect(await screen.findByText('No books yet')).toBeInTheDocument();
    expect(screen.getByRole('heading', { name: 'Library' })).toBeInTheDocument();
  });

  it('lists books', async () => {
    stubApi({
      '/api/books': {
        items: [
          {
            id: 1,
            title: 'Pride and Prejudice',
            author: 'Jane Austen',
            status: 'imported',
            createdAt: '2026-10-15T00:00:00Z',
            updatedAt: '2026-10-15T00:00:00Z',
          },
        ],
        total: 1,
      },
    });
    renderAt('/library');
    expect(await screen.findByText('Pride and Prejudice')).toBeInTheDocument();
    expect(screen.getByText('Jane Austen')).toBeInTheDocument();
  });

  it('shows database and TTS status', async () => {
    stubApi({
      '/api/system': {
        version: '0.1.0',
        commit: 'abc123',
        database: { dialect: 'postgres', schemaVersion: '202610150001_create_books' },
        tts: { url: 'http://kokoro:8880', reachable: false, error: 'connection refused' },
      },
    });
    renderAt('/system');
    expect(await screen.findByText('postgres')).toBeInTheDocument();
    expect(screen.getByText('unreachable')).toBeInTheDocument();
    expect(screen.getByText('connection refused')).toBeInTheDocument();
  });

  it('shows API errors', async () => {
    stubApi({});
    renderAt('/library');
    expect(await screen.findByText('Could not load books')).toBeInTheDocument();
  });
});
