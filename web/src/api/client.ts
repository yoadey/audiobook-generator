import { queryOptions } from '@tanstack/react-query';
import createClient from 'openapi-fetch';

import type { components, paths } from './schema';

export type Book = components['schemas']['Book'];
export type BookPage = components['schemas']['BookPage'];
export type Chapter = components['schemas']['Chapter'];
export type ChapterDetail = components['schemas']['ChapterDetail'];
export type ChapterUpdate = components['schemas']['ChapterUpdate'];
export type Job = components['schemas']['Job'];
export type Paragraph = components['schemas']['Paragraph'];
export type Segment = components['schemas']['Segment'];
export type SystemInfo = components['schemas']['SystemInfo'];
export type Volume = components['schemas']['Volume'];

// Absolute, because Request() outside a browser (tests) rejects relative URLs.
const baseUrl = new URL('/api', window.location.origin).href;

export const api = createClient<paths>({
  baseUrl,
  // Resolve fetch per call (not once at import time) so tests can stub it.
  fetch: (request) => globalThis.fetch(request),
});

/** Unwraps an openapi-fetch result, throwing the API's error message. */
async function unwrap<T>(
  promise: Promise<{ data?: T; error?: { message: string } | unknown; response: Response }>,
): Promise<T> {
  const { data, error, response } = await promise;
  if (response.ok) {
    return data as T;
  }
  const message =
    typeof error === 'object' && error !== null && 'message' in error
      ? String(error.message)
      : `${response.status} ${response.statusText}`;
  throw new Error(message);
}

const POLL_MS = 2000;

// --- Queries -----------------------------------------------------------------

export const systemInfoQuery = queryOptions({
  queryKey: ['system'],
  queryFn: () => unwrap(api.GET('/system')),
  refetchInterval: 15_000,
});

export const booksQuery = () =>
  queryOptions({
    queryKey: ['books', 'list'],
    queryFn: () => unwrap(api.GET('/books', { params: { query: { limit: 500 } } })),
    // Keep polling while an import runs.
    refetchInterval: (q) =>
      q.state.data?.items.some((b) => b.status === 'importing') ? POLL_MS : false,
  });

export const bookQuery = (bookId: number) =>
  queryOptions({
    queryKey: ['books', bookId],
    queryFn: () => unwrap(api.GET('/books/{bookId}', { params: { path: { bookId } } })),
    refetchInterval: (q) => (q.state.data?.status === 'importing' ? POLL_MS : false),
  });

export const volumesQuery = (bookId: number) =>
  queryOptions({
    queryKey: ['books', bookId, 'volumes'],
    queryFn: () => unwrap(api.GET('/books/{bookId}/volumes', { params: { path: { bookId } } })),
  });

export const chaptersQuery = (bookId: number) =>
  queryOptions({
    queryKey: ['books', bookId, 'chapters'],
    queryFn: () => unwrap(api.GET('/books/{bookId}/chapters', { params: { path: { bookId } } })),
  });

export const chapterQuery = (chapterId: number) =>
  queryOptions({
    queryKey: ['chapters', chapterId],
    queryFn: () => unwrap(api.GET('/chapters/{chapterId}', { params: { path: { chapterId } } })),
  });

export const coverUrl = (bookId: number) => `${baseUrl}/books/${bookId}/cover`;

// --- Mutations ---------------------------------------------------------------

/** Uploads an EPUB (multipart; openapi-fetch's JSON body typing does not fit). */
export async function uploadBook(file: File): Promise<Book> {
  const form = new FormData();
  form.append('file', file);
  const response = await globalThis.fetch(`${baseUrl}/books`, { method: 'POST', body: form });
  const body: unknown = await response.json().catch(() => undefined);
  if (!response.ok) {
    const message =
      typeof body === 'object' && body !== null && 'message' in body
        ? String(body.message)
        : `${response.status} ${response.statusText}`;
    throw new Error(message);
  }
  return body as Book;
}

export const deleteBook = (bookId: number) =>
  unwrap(api.DELETE('/books/{bookId}', { params: { path: { bookId } } }));

export const updateChapter = (chapterId: number, body: ChapterUpdate) =>
  unwrap(api.PATCH('/chapters/{chapterId}', { params: { path: { chapterId } }, body }));

export const mergeChapterWithNext = (chapterId: number) =>
  unwrap(api.POST('/chapters/{chapterId}/merge-next', { params: { path: { chapterId } } }));

export const splitChapter = (chapterId: number, paragraphId: number, title: string) =>
  unwrap(
    api.POST('/chapters/{chapterId}/split', {
      params: { path: { chapterId } },
      body: { paragraphId, title },
    }),
  );
