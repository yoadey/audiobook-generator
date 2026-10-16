import { queryOptions } from '@tanstack/react-query';
import createClient from 'openapi-fetch';

import type { components, paths } from './schema';

export type Analysis = components['schemas']['Analysis'];
export type AnalysisStatus = components['schemas']['AnalysisStatus'];
export type AudioStatus = components['schemas']['AudioStatus'];
export type Book = components['schemas']['Book'];
export type Casting = components['schemas']['Casting'];
export type CastingStatus = components['schemas']['CastingStatus'];
export type CastingWarning = components['schemas']['CastingWarning'];
export type BookUpdate = components['schemas']['BookUpdate'];
export type BookPage = components['schemas']['BookPage'];
export type Chapter = components['schemas']['Chapter'];
export type ChapterAttribution = components['schemas']['ChapterAttribution'];
export type ChapterDetail = components['schemas']['ChapterDetail'];
export type ChapterUpdate = components['schemas']['ChapterUpdate'];
export type Character = components['schemas']['Character'];
export type Emotion = components['schemas']['Emotion'];
export type QuoteUpdate = components['schemas']['QuoteUpdate'];
export type CharacterDetail = components['schemas']['CharacterDetail'];
export type CharacterUpdate = components['schemas']['CharacterUpdate'];
export type Job = components['schemas']['Job'];
export type LLMProvider = components['schemas']['LLMProvider'];
export type LLMProviderInput = components['schemas']['LLMProviderInput'];
export type LLMProviderUpdate = components['schemas']['LLMProviderUpdate'];
export type LLMModelsRequest = components['schemas']['LLMModelsRequest'];
export type LLMTestResult = components['schemas']['LLMTestResult'];
export type Paragraph = components['schemas']['Paragraph'];
export type Progress = components['schemas']['Progress'];
export type Quote = components['schemas']['Quote'];
export type Segment = components['schemas']['Segment'];
export type SystemInfo = components['schemas']['SystemInfo'];
export type Tier = components['schemas']['Tier'];
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

const analysisActive = (s?: AnalysisStatus) => s === 'queued' || s === 'running' || s === 'merging';
export const castingActive = (s?: CastingStatus) => s === 'queued' || s === 'running';

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
    // Keep polling while an import runs (fast) or an analysis (slowly).
    refetchInterval: (q) =>
      q.state.data?.items.some((b) => b.status === 'importing')
        ? POLL_MS
        : q.state.data?.items.some((b) => analysisActive(b.analysisStatus))
          ? 15_000
          : false,
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

const audioPending = (c: { audioStatus: AudioStatus; audioRerendering: boolean }) =>
  c.audioStatus === 'queued' || c.audioStatus === 'rendering' || c.audioRerendering;

export const chaptersQuery = (bookId: number) =>
  queryOptions({
    queryKey: ['books', bookId, 'chapters'],
    queryFn: () => unwrap(api.GET('/books/{bookId}/chapters', { params: { path: { bookId } } })),
    // Keep polling while audio is being rendered.
    refetchInterval: (q) =>
      q.state.data?.items.some((c) => audioPending(c)) ? POLL_MS * 2 : false,
  });

const attributionActive = (s?: ChapterAttribution['status']) => s === 'queued' || s === 'running';

export const chapterQuery = (chapterId: number) =>
  queryOptions({
    queryKey: ['chapters', chapterId],
    queryFn: () => unwrap(api.GET('/chapters/{chapterId}', { params: { path: { chapterId } } })),
    refetchInterval: (q) => {
      const c = q.state.data;
      if (!c) {
        return false;
      }
      if (c.audioStatus === 'rendering') {
        return POLL_MS;
      }
      // An attribution deferred (LLM quota, retry) holds up the render too:
      // look again rarely.
      const until = c.attribution?.job?.notBefore;
      if (
        attributionActive(c.attributionStatus) &&
        until &&
        new Date(until).getTime() > Date.now()
      ) {
        return 60_000;
      }
      if (audioPending(c) || c.attributionStatus === 'running') {
        return POLL_MS;
      }
      return attributionActive(c.attributionStatus) ? 10_000 : false;
    },
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

export const llmProvidersQuery = queryOptions({
  queryKey: ['llm', 'providers'],
  queryFn: () => unwrap(api.GET('/llm/providers')),
  refetchInterval: 10_000,
});

export const analysisQuery = (bookId: number) =>
  queryOptions({
    queryKey: ['books', bookId, 'analysis'],
    queryFn: () => unwrap(api.GET('/books/{bookId}/analysis', { params: { path: { bookId } } })),
    refetchInterval: (q) => (analysisActive(q.state.data?.status) ? 5000 : false),
  });

export type CharacterSort = 'importance' | 'name' | 'chapters' | 'first';

export const charactersQuery = (
  bookId: number,
  search: string,
  sort: CharacterSort,
  page: number,
  pageSize = 50,
  tier?: Tier,
) =>
  queryOptions({
    queryKey: ['books', bookId, 'characters', { search, sort, page, pageSize, tier }],
    queryFn: () =>
      unwrap(
        api.GET('/books/{bookId}/characters', {
          params: {
            path: { bookId },
            query: {
              search: search || undefined,
              sort,
              tier,
              offset: (page - 1) * pageSize,
              limit: pageSize,
            },
          },
        }),
      ),
    placeholderData: (prev) => prev,
  });

export const castingQuery = (bookId: number) =>
  queryOptions({
    queryKey: ['books', bookId, 'casting'],
    queryFn: () => unwrap(api.GET('/books/{bookId}/casting', { params: { path: { bookId } } })),
    // Fast while it runs, slowly while it waits for LLM quota.
    refetchInterval: (q) => {
      const c = q.state.data;
      if (!castingActive(c?.status)) {
        return false;
      }
      const waiting = c?.job?.notBefore && new Date(c.job.notBefore).getTime() > Date.now();
      return waiting ? 30_000 : 3000;
    },
  });

export const quotesQuery = (characterId: number) =>
  queryOptions({
    queryKey: ['characters', characterId, 'quotes'],
    queryFn: () =>
      unwrap(
        api.GET('/characters/{characterId}/quotes', {
          params: { path: { characterId }, query: { limit: 10 } },
        }),
      ),
    staleTime: 10 * 60_000,
  });

export const characterQuery = (characterId: number) =>
  queryOptions({
    queryKey: ['characters', characterId],
    queryFn: () =>
      unwrap(api.GET('/characters/{characterId}', { params: { path: { characterId } } })),
  });

/** The audio of a chapter; with version, that render (kept while a newer one replaces it). */
export const audioUrl = (chapterId: number, version?: string) =>
  `${baseUrl}/chapters/${chapterId}/audio${version ? `?v=${encodeURIComponent(version)}` : ''}`;

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
export async function previewVoice(
  voice: string,
  speed: number,
  text?: string,
  signal?: AbortSignal,
): Promise<string> {
  const response = await globalThis.fetch(`${baseUrl}/voices/preview`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ voice, speed, text }),
    signal,
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

export const createLLMProvider = (body: LLMProviderInput) =>
  unwrap(api.POST('/llm/providers', { body }));

export const updateLLMProvider = (providerId: number, body: LLMProviderUpdate) =>
  unwrap(api.PATCH('/llm/providers/{providerId}', { params: { path: { providerId } }, body }));

export const deleteLLMProvider = (providerId: number) =>
  unwrap(api.DELETE('/llm/providers/{providerId}', { params: { path: { providerId } } }));

export const testLLMProvider = (providerId: number) =>
  unwrap(api.POST('/llm/providers/{providerId}/test', { params: { path: { providerId } } }));

export const listLLMModels = (body: LLMModelsRequest) => unwrap(api.POST('/llm/models', { body }));

export const startAnalysis = (bookId: number, restart = false) =>
  unwrap(api.POST('/books/{bookId}/analysis', { params: { path: { bookId } }, body: { restart } }));

export const startAttribution = (chapterId: number, force = false) =>
  unwrap(
    api.POST('/chapters/{chapterId}/attribution', {
      params: { path: { chapterId } },
      body: { force },
    }),
  );

export const updateQuote = (chapterId: number, quoteRef: number, body: QuoteUpdate) =>
  unwrap(
    api.PATCH('/chapters/{chapterId}/quotes/{quoteRef}', {
      params: { path: { chapterId, quoteRef } },
      body,
    }),
  );

export const startCasting = (bookId: number, restart = false) =>
  unwrap(api.POST('/books/{bookId}/casting', { params: { path: { bookId } }, body: { restart } }));

export const pauseAnalysis = (bookId: number) =>
  unwrap(api.POST('/books/{bookId}/analysis/pause', { params: { path: { bookId } } }));

export const updateCharacter = (characterId: number, body: CharacterUpdate) =>
  unwrap(api.PATCH('/characters/{characterId}', { params: { path: { characterId } }, body }));

export const deleteCharacter = (characterId: number) =>
  unwrap(api.DELETE('/characters/{characterId}', { params: { path: { characterId } } }));

export const mergeCharacters = (bookId: number, targetId: number, sourceIds: number[]) =>
  unwrap(
    api.POST('/books/{bookId}/characters/merge', {
      params: { path: { bookId } },
      body: { targetId, sourceIds },
    }),
  );

export const splitCharacter = (characterId: number, name: string, aliases: string[]) =>
  unwrap(
    api.POST('/characters/{characterId}/split', {
      params: { path: { characterId } },
      body: { name, aliases },
    }),
  );
