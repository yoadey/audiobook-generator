import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';

import { describe, expect, it, vi } from 'vitest';

// The service worker (public/sw.js) in a stand-in for its global scope.
function load(network: (url: string) => Promise<Response>) {
  const listeners: Record<string, (e: unknown) => void> = {};
  const store = new Map<string, Response>();
  const cache = {
    add: vi.fn(async (url: string) => void store.set(url, await network(url))),
    put: vi.fn(async (url: string, res: Response) => void store.set(url, res)),
  };
  const scope = {
    addEventListener: (type: string, fn: (e: unknown) => void) => (listeners[type] = fn),
    skipWaiting: vi.fn(),
    clients: { claim: vi.fn() },
  };
  const caches = {
    open: vi.fn(async () => cache),
    keys: vi.fn(async () => []),
    delete: vi.fn(),
    match: vi.fn(async (url: string) => store.get(url)),
  };
  const src = readFileSync(resolve(import.meta.dirname, '../public/sw.js'), 'utf8');
  new Function('self', 'caches', 'fetch', src)(scope, caches, (req: Request | string) =>
    network(typeof req === 'string' ? req : req.url),
  );
  // A navigation: what the worker answers with (undefined: left alone).
  const navigate = async (url: string) => {
    let answer: Promise<Response> | undefined;
    listeners.fetch!({
      request: { method: 'GET', mode: 'navigate', url: `https://abg.example${url}` },
      respondWith: (p: Promise<Response>) => (answer = p),
    });
    return answer && (await answer);
  };
  return { navigate, store };
}

const page = (body: string, status = 200) =>
  Object.defineProperty(
    new Response(body, { status, headers: { 'content-type': 'text/html; charset=utf-8' } }),
    'type',
    { value: 'basic' },
  );

describe('service worker', () => {
  it('keeps the app page and shows it when the server cannot be reached', async () => {
    let up = true;
    const { navigate, store } = load(async () => {
      if (!up) throw new TypeError('offline');
      return page('<html>app</html>');
    });
    expect(await (await navigate('/library'))!.text()).toBe('<html>app</html>');
    await vi.waitFor(() => expect(store.has('/')).toBe(true));
    up = false;
    expect(await (await navigate('/books/1'))!.text()).toBe('<html>app</html>');
  });

  it('shows the app page when the ingress answers for a server that is down', async () => {
    let status = 200;
    const { navigate, store } = load(async () =>
      page(status === 200 ? 'app' : 'bad gateway', status),
    );
    await navigate('/library');
    await vi.waitFor(() => expect(store.has('/')).toBe(true));
    status = 502;
    expect(await (await navigate('/library'))!.text()).toBe('app');
  });

  it('leaves downloads, the API, feeds and the sign-in alone', async () => {
    const { navigate, store } = load(async () => new Response('zip'));
    for (const url of [
      '/api/books/1/export',
      '/feed/secret/1.xml',
      '/auth/logout',
      '/metrics',
      '/sw.js',
    ]) {
      expect(await navigate(url)).toBeUndefined();
    }
    expect(store.size).toBe(0);
  });

  it('never keeps a response that is not the app page', async () => {
    const { navigate, store } = load(async () =>
      Object.defineProperty(
        new Response('{}', { headers: { 'content-type': 'application/json' } }),
        'type',
        { value: 'basic' },
      ),
    );
    await navigate('/library');
    await new Promise((r) => setTimeout(r, 10));
    expect(store.has('/')).toBe(false);
  });
});
