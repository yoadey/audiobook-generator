import { QueryClient } from '@tanstack/react-query';
import { createMemoryHistory } from '@tanstack/react-router';
import { render, screen, within } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';

import { App } from './App';
import { createAppRouter } from './router';

type Routes = Record<string, unknown>;

function stubApi(routes: Routes) {
  const fetch = vi.fn(async (input: Request | string) => {
    const req = typeof input === 'string' ? new Request(new URL(input, 'http://localhost')) : input;
    const url = new URL(req.url);
    const body = routes[`${req.method} ${url.pathname}`] ?? routes[url.pathname];
    if (body === undefined) {
      return new Response(JSON.stringify({ message: 'not found' }), { status: 404 });
    }
    return new Response(JSON.stringify(body), {
      status: 200,
      headers: { 'Content-Type': 'application/json' },
    });
  });
  vi.stubGlobal('fetch', fetch);
  return fetch;
}

function renderAt(path: string) {
  const router = createAppRouter(createMemoryHistory({ initialEntries: [path] }));
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(<App router={router} queryClient={queryClient} />);
}

const book = {
  id: 1,
  title: 'Pride and Prejudice',
  author: 'Jane Austen',
  language: 'en',
  status: 'imported',
  progress: 1,
  wordCount: 122000,
  chapterCount: 2,
  hasCover: false,
  createdAt: '2026-10-15T00:00:00Z',
  updatedAt: '2026-10-15T00:00:00Z',
};

const chapters = [
  {
    id: 10,
    bookId: 1,
    volumeId: 5,
    idx: 0,
    title: 'Chapter 1',
    wordCount: 1200,
    quoteCount: 14,
    excluded: false,
    status: 'imported',
  },
  {
    id: 11,
    bookId: 1,
    volumeId: 5,
    idx: 1,
    title: 'Copyright',
    wordCount: 40,
    quoteCount: 0,
    excluded: true,
    status: 'imported',
  },
];

describe('App', () => {
  it('redirects to the library and shows the empty state', async () => {
    stubApi({ '/api/books': { items: [], total: 0 } });
    renderAt('/');
    expect(await screen.findByText('No books yet')).toBeInTheDocument();
    expect(screen.getByRole('heading', { name: 'Library' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: /upload epub/i })).toBeEnabled();
  });

  it('lists books with progress and errors', async () => {
    stubApi({
      '/api/books': {
        items: [
          book,
          {
            ...book,
            id: 2,
            title: 'Importing Book',
            status: 'importing',
            progress: 0.4,
            chapterCount: 0,
          },
          {
            ...book,
            id: 3,
            title: 'Broken',
            status: 'failed',
            error: 'not a valid EPUB',
            chapterCount: 0,
          },
        ],
        total: 3,
      },
    });
    renderAt('/library');
    expect(await screen.findByText('Pride and Prejudice')).toBeInTheDocument();
    expect(screen.getAllByText('Jane Austen')).toHaveLength(3);
    expect(screen.getByText(/2 chapters · 122,000 words · ~13 h 7 min/)).toBeInTheDocument();
    expect(screen.getByText('not a valid EPUB')).toBeInTheDocument();
    expect(screen.getByText('importing')).toBeInTheDocument();
  });

  it('shows a book with its chapters', async () => {
    stubApi({
      '/api/books/1': book,
      '/api/books/1/volumes': [
        { id: 5, idx: 0, title: 'Volume One', chapterCount: 2, wordCount: 1240 },
      ],
      '/api/books/1/chapters': { items: chapters, total: 2 },
    });
    renderAt('/books/1');
    expect(await screen.findByRole('heading', { name: 'Pride and Prejudice' })).toBeInTheDocument();
    const rows = await screen.findAllByTestId('chapter-row');
    expect(rows).toHaveLength(2);
    expect(within(rows[0]!).getByText('Chapter 1')).toBeInTheDocument();
    expect(within(rows[0]!).getByRole('switch')).toBeChecked();
    expect(within(rows[1]!).getByRole('switch')).not.toBeChecked();
    expect(screen.getByText(/narrated: 1 chapters/)).toBeInTheDocument();
  });

  it('shows chapter text with quotes highlighted', async () => {
    stubApi({
      '/api/books/1': book,
      '/api/chapters/10': {
        ...chapters[0],
        nextChapterId: 11,
        paragraphs: [
          {
            id: 100,
            idx: 0,
            kind: 'heading',
            text: 'Chapter 1',
            segments: [{ id: 1, idx: 0, kind: 'narration', quoteRef: 0, text: 'Chapter 1' }],
          },
          {
            id: 101,
            idx: 1,
            kind: 'text',
            text: '“My dear,” said his lady.',
            segments: [
              { id: 2, idx: 1, kind: 'quote', quoteRef: 1, text: '“My dear,”' },
              { id: 3, idx: 2, kind: 'narration', quoteRef: 0, text: 'said his lady.' },
            ],
          },
          { id: 102, idx: 2, kind: 'scene_break', text: '* * *', segments: [] },
        ],
      },
    });
    const { container } = renderAt('/books/1/chapters/10');
    expect(await screen.findByDisplayValue('Chapter 1')).toBeInTheDocument();
    expect(screen.getByText('“My dear,”')).toHaveAttribute('data-quote', '1');
    expect(screen.getByText('said his lady.')).toBeInTheDocument();
    expect(container.textContent).toContain('⁂');
    // Splitting is offered from the second paragraph on.
    expect(screen.getAllByRole('button', { name: 'Split here' })).toHaveLength(2);
    expect(screen.getByRole('button', { name: 'Previous chapter' })).toBeDisabled();
    expect(screen.getByRole('button', { name: 'Next chapter' })).toBeEnabled();
  });

  it('shows database and TTS status', async () => {
    stubApi({
      '/api/system': {
        version: '0.1.0',
        commit: 'abc123',
        maxUploadBytes: 536870912,
        database: { dialect: 'postgres', schemaVersion: '202610150002_book_content_and_jobs' },
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
