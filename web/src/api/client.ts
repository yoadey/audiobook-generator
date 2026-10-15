import { queryOptions } from '@tanstack/react-query';
import createClient from 'openapi-fetch';

import type { components, paths } from './schema';

export type AudioStatus = components['schemas']['AudioStatus'];
export type Book = components['schemas']['Book'];
export type BookUpdate = components['schemas']['BookUpdate'];
export type BookPage = components['schemas']['BookPage'];
export type Chapter = components['schemas']['Chapter'];
export type ChapterDetail = components['schemas']['ChapterDetail'];
export type ChapterUpdate = components['schemas']['ChapterUpdate'];
export type Job = components['schemas']['Job'];
export type Paragraph = components['schemas']['Paragraph'];
export type Progress = components['schemas']['Progress'];
export type Segment = components['schemas']['Segment'];
export type SystemInfo = components['schemas']['SystemInfo'];
export type Voice = components['schemas']['Voice'];
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

const audioPending = (s: AudioStatus) => s === 'queued' || s === 'rendering';

export const chaptersQuery = (bookId: number) =>
  queryOptions({
    queryKey: ['books', bookId, 'chapters'],
    queryFn: () => unwrap(api.GET('/books/{bookId}/chapters', { params: { path: { bookId } } })),
    // Keep polling while audio is being rendered.
    refetchInterval: (q) =>
      q.state.data?.items.some((c) => audioPending(c.audioStatus)) ? POLL_MS * 2 : false,
  });

export const chapterQuery = (chapterId: number) =>
  queryOptions({
    queryKey: ['chapters', chapterId],
    queryFn: () => unwrap(api.GET('/chapters/{chapterId}', { params: { path: { chapterId } } })),
    refetchInterval: (q) =>
      q.state.data && audioPending(q.state.data.audioStatus) ? POLL_MS : false,
  });

export const progressQuery = (bookId: number) =>
  queryOptions({
    queryKey: ['books', bookId, 'progress'],
    queryFn: () => unwrap(api.GET('/books/{bookId}/progress', { params: { path: { bookId } } })),
    staleTime: Infinity,
  });

export const voicesQuery = queryOptions({
  queryKey: ['voices'],
  queryFn: () => unwrap(api.GET('/voices')),
  staleTime: 5 * 60_000,
});

export const jobsQuery = (status?: Job['status'][]) =>
  queryOptions({
    queryKey: ['jobs', status ?? 'all'],
    queryFn: () => unwrap(api.GET('/jobs', { params: { query: { status, limit: 200 } } })),
    refetchInterval: 3000,
  });

export const audioUrl = (chapterId: number) => `${baseUrl}/chapters/${chapterId}/audio`;

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

export const updateBook = (bookId: number, body: BookUpdate) =>
  unwrap(api.PATCH('/books/{bookId}', { params: { path: { bookId } }, body }));

export const renderChapter = (chapterId: number, force = false) =>
  unwrap(
    api.POST('/chapters/{chapterId}/render', {
      params: { path: { chapterId }, query: { force } },
    }),
  );

export const renderBookChapters = (bookId: number, count: number, fromChapterId?: number) =>
  unwrap(
    api.POST('/books/{bookId}/render', {
      params: { path: { bookId } },
      body: { count, fromChapterId },
    }),
  );

export const saveProgress = (bookId: number, chapterId: number, positionMs: number) =>
  unwrap(
    api.PUT('/books/{bookId}/progress', {
      params: { path: { bookId } },
      body: { chapterId, positionMs: Math.max(0, Math.round(positionMs)) },
    }),
  );

export const retryJob = (jobId: number) =>
  unwrap(api.POST('/jobs/{jobId}/retry', { params: { path: { jobId } } }));

export const cancelJob = (jobId: number) =>
  unwrap(api.POST('/jobs/{jobId}/cancel', { params: { path: { jobId } } }));

/** Synthesizes a sample and returns an object URL for an <audio> element. */
export async function previewVoice(voice: string, speed: number, text?: string): Promise<string> {
  const response = await globalThis.fetch(`${baseUrl}/voices/preview`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ voice, speed, text }),
  });
  if (!response.ok) {
    const body: unknown = await response.json().catch(() => undefined);
    throw new Error(
      typeof body === 'object' && body !== null && 'message' in body
        ? String(body.message)
        : `${response.status} ${response.statusText}`,
    );
  }
  return URL.createObjectURL(await response.blob());
}
