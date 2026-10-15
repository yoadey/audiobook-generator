import { queryOptions } from '@tanstack/react-query';
import createClient from 'openapi-fetch';

import type { components, paths } from './schema';

export type Book = components['schemas']['Book'];
export type BookPage = components['schemas']['BookPage'];
export type SystemInfo = components['schemas']['SystemInfo'];

export const api = createClient<paths>({
  // Absolute, because Request() outside a browser (tests) rejects relative URLs.
  baseUrl: new URL('/api', window.location.origin).href,
  // Resolve fetch per call (not once at import time) so tests can stub it.
  fetch: (request) => globalThis.fetch(request),
});

/** Unwraps an openapi-fetch result, throwing the API's error message. */
async function unwrap<T>(
  promise: Promise<{ data?: T; error?: { message: string } | unknown; response: Response }>,
): Promise<T> {
  const { data, error, response } = await promise;
  if (data !== undefined) {
    return data;
  }
  const message =
    typeof error === 'object' && error !== null && 'message' in error
      ? String(error.message)
      : `${response.status} ${response.statusText}`;
  throw new Error(message);
}

export const systemInfoQuery = queryOptions({
  queryKey: ['system'],
  queryFn: () => unwrap(api.GET('/system')),
  refetchInterval: 15_000,
});

export const booksQuery = (offset = 0, limit = 50) =>
  queryOptions({
    queryKey: ['books', { offset, limit }],
    queryFn: () => unwrap(api.GET('/books', { params: { query: { offset, limit } } })),
  });
