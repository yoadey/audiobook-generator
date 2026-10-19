import { onlineManager, QueryClient } from '@tanstack/react-query';
import { createMemoryHistory } from '@tanstack/react-router';
import { act, fireEvent, render, screen, within } from '@testing-library/react';
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
  return { ...render(<App router={router} queryClient={queryClient} />), router };
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
  narratorVoice: 'af_heart',
  narratorSpeed: 1,
  lookahead: 3,
  previewText: '',
  prepareChapters: false,
  characterVoices: true,
  analysisStatus: 'none',
  castingStatus: 'none',
  createdAt: '2026-10-15T00:00:00Z',
  updatedAt: '2026-10-15T00:00:00Z',
};

const analysisBase = {
  status: 'none',
  batchesTotal: 190,
  batchesDone: 0,
  estimatedTokens: 5_600_000,
  inputTokens: 0,
  outputTokens: 0,
  characterCount: 0,
  batchTokens: 30000,
  prompt: 'character_analysis@v1',
};

const erin = {
  id: 7,
  bookId: 1,
  name: 'Erin Solstice',
  aliases: ['Erin', 'the Innkeeper'],
  gender: 'female',
  ageGroup: 'adult',
  role: 'innkeeper',
  description: 'Runs the inn.',
  speechStyle: 'warm, chatty',
  importance: 5,
  speaks: true,
  chapterCount: 812,
  firstChapterIdx: 0,
  userEdited: false,
  tier: 'main',
  tierManual: false,
  voice: 'bf_emma(0.7)+af_bella(0.3)',
  voiceSpeed: 1,
  voiceSource: 'own',
  voiceManual: false,
  voiceNote: 'warm and British',
  previewText: '',
  voiceDesign: {
    status: 'none',
    description: '',
    suggestedDescription: 'The voice of an adult woman. Speaks warm, chatty. An innkeeper.',
    error: '',
  },
};

const voices = [
  {
    id: 'af_heart',
    language: 'en-US',
    gender: 'female',
    grade: 'A',
    traits: 'warm',
    available: true,
    inCatalog: true,
  },
  {
    id: 'af_bella',
    language: 'en-US',
    gender: 'female',
    grade: 'A-',
    traits: 'bright',
    available: true,
    inCatalog: true,
  },
  {
    id: 'bf_emma',
    language: 'en-GB',
    gender: 'female',
    grade: 'B-',
    traits: 'refined',
    available: true,
    inCatalog: true,
  },
  {
    id: 'am_adam',
    language: 'en-US',
    gender: 'male',
    grade: 'F+',
    traits: 'flat',
    available: false,
    inCatalog: true,
  },
];

const castingBase = {
  status: 'done',
  mainCount: 40,
  minorCount: 300,
  voicedCount: 40,
  mainLimit: 50,
  narratorVoice: 'af_heart',
  narratorSpeed: 1,
  prompt: 'cast_voices@v1',
  warnings: [],
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
    audioStatus: 'ready',
    audioDurationMs: 245000,
    audioStale: false,
    attributionStatus: 'done',
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
    audioStatus: 'none',
    audioStale: false,
  },
];

const status = {
  state: 'ready',
  today: {
    day: '2026-10-15',
    requests: 0,
    inputTokens: 0,
    outputTokens: 0,
    errors: 0,
    rateLimited: 0,
  },
  minuteRequests: 0,
  minuteTokens: 0,
  quotaResetAt: '2099-01-01T00:00:00Z',
};

const helmProvider = {
  id: 1,
  name: 'gemini',
  source: 'config',
  position: 0,
  enabled: true,
  baseUrl: 'https://generativelanguage.googleapis.com/v1beta/openai/',
  model: 'gemini-flash',
  hasApiKey: true,
  apiKeyEnv: 'ABG_LLM_PROVIDER_0_API_KEY',
  structuredOutput: 'json_schema',
  limits: { rpm: 10, tpm: 250000, rpd: 250, tpd: 0 },
  maxOutputTokens: 0,
  timeoutSeconds: 300,
  quotaTimezone: 'America/Los_Angeles',
  createdAt: '2026-10-15T00:00:00Z',
  updatedAt: '2026-10-15T00:00:00Z',
  status: {
    ...status,
    state: 'exhausted',
    reason: 'daily limit of 250 requests reached',
    availableAt: '2099-01-01T00:00:00Z',
    today: { ...status.today, requests: 250, inputTokens: 900000, outputTokens: 40000 },
  },
};

const uiProvider = {
  ...helmProvider,
  id: 2,
  name: 'local',
  source: 'ui',
  position: 1,
  baseUrl: 'http://ollama:11434/v1/',
  model: 'qwen3:8b',
  hasApiKey: false,
  apiKeyEnv: undefined,
  limits: { rpm: 0, tpm: 0, rpd: 0, tpd: 0 },
  quotaTimezone: 'UTC',
  status: { ...status, lastError: 'HTTP 503: overloaded', lastErrorAt: '2026-10-15T10:00:00Z' },
};

describe('App', () => {
  it('redirects to the library and shows the empty state', async () => {
    stubApi({ '/api/books': { items: [], total: 0 } });
    renderAt('/');
    expect(await screen.findByText('No books yet')).toBeInTheDocument();
    expect(screen.getByRole('heading', { name: 'Library' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: /upload epub/i })).toBeEnabled();
  });

  it('offers to install the app where the browser can', async () => {
    stubApi({ '/api/books': { items: [], total: 0 } });
    renderAt('/library');
    await screen.findByText('No books yet');
    expect(screen.queryByRole('button', { name: 'Install app' })).toBeNull();

    const prompt = vi.fn(() => Promise.resolve());
    act(() => {
      window.dispatchEvent(Object.assign(new Event('beforeinstallprompt'), { prompt }));
    });
    fireEvent.click(await screen.findByRole('button', { name: 'Install app' }));
    expect(prompt).toHaveBeenCalledTimes(1);
    expect(screen.queryByRole('button', { name: 'Install app' })).toBeNull();
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
    expect(screen.getByText('2 chapters · ~8 h 8 min to read')).toBeInTheDocument();
    expect(screen.getByText('not a valid EPUB')).toBeInTheDocument();
    expect(screen.getByText('importing')).toBeInTheDocument();
  });

  it('shows a book to read: description and contents', async () => {
    const fetch = stubApi({
      '/api/books/1': { ...book, description: 'A novel of manners.\n\nSecond paragraph.' },
      '/api/books/1/volumes': [
        { id: 5, idx: 0, title: 'Volume One', chapterCount: 2, wordCount: 1240 },
      ],
      '/api/books/1/chapters': {
        items: [...chapters, { ...chapters[0], id: 12, idx: 2, title: 'Chapter 2' }],
        total: 3,
      },
      'GET /api/books/1/progress': { chapterId: 12, positionMs: 0 },
      'PUT /api/books/1/progress': { chapterId: 12, positionMs: 0 },
    });
    const { router } = renderAt('/books/1');
    expect(await screen.findByRole('heading', { name: 'Pride and Prejudice' })).toBeInTheDocument();
    expect(screen.getByText(/A novel of manners\./)).toBeInTheDocument();
    // No audiobook controls here: they are a page of their own.
    expect(screen.queryByText('Narrator')).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: /render/i })).not.toBeInTheDocument();
    expect(screen.getByRole('link', { name: 'Audiobook' })).toHaveAttribute(
      'href',
      '/books/1/audiobook',
    );

    // The contents: the chapters to read (not the excluded ones), by volume.
    const contents = await screen.findByRole('navigation', { name: 'Contents' });
    expect(within(contents).getByText('Volume One')).toBeInTheDocument();
    expect(within(contents).queryByText('Copyright')).not.toBeInTheDocument();
    await vi.waitFor(() =>
      expect(within(contents).getByRole('button', { name: /Chapter 2/ })).toHaveAttribute(
        'aria-current',
        'true',
      ),
    );
    expect(screen.getByText('2 chapters · ~10 min to read')).toBeInTheDocument();
    expect(screen.getByRole('link', { name: 'Continue reading' })).toHaveAttribute(
      'href',
      '/books/1/listen',
    );

    // A chapter opens the reader at its start (stored so); the address
    // then forgets it, so a reload goes on where reading is.
    fireEvent.click(within(contents).getByRole('button', { name: /Chapter 1/ }));
    const player = await screen.findByTestId('player');
    expect(within(player).getByText('Chapter 1')).toBeInTheDocument();
    expect(screen.getByText('1 / 2')).toBeInTheDocument();
    await vi.waitFor(() => expect(router.state.location.searchStr).toBe(''));
    expect(router.state.location.pathname).toBe('/books/1/listen');
    await vi.waitFor(async () => {
      const bodies = await Promise.all(progressPuts(fetch).map((r) => r.clone().json()));
      expect(bodies).toContainEqual({ chapterId: 10, positionMs: 0 });
    });
  });

  it('says when a book has nothing to read', async () => {
    stubApi({
      '/api/books/1': book,
      '/api/books/1/volumes': [],
      '/api/books/1/chapters': { items: [chapters[1]], total: 1 },
      'GET /api/books/1/progress': { positionMs: 0 },
    });
    renderAt('/books/1');
    expect(await screen.findByText(/No chapters to read/)).toBeInTheDocument();
    expect(screen.queryByRole('link', { name: /^(Read|Continue reading)$/ })).toBeNull();
  });

  it('shows the audiobook of a book with its chapters', async () => {
    stubApi({
      '/api/voices': [
        { id: 'af_heart', language: 'en-US', gender: 'female', grade: 'A' },
        { id: 'bm_george', language: 'en-GB', gender: 'male' },
      ],
      '/api/books/1': book,
      '/api/books/1/volumes': [
        { id: 5, idx: 0, title: 'Volume One', chapterCount: 2, wordCount: 1240 },
      ],
      '/api/books/1/chapters': { items: chapters, total: 2 },
    });
    renderAt('/books/1/audiobook');
    expect(await screen.findByRole('heading', { name: 'Audiobook' })).toBeInTheDocument();
    const rows = await screen.findAllByTestId('chapter-row');
    expect(rows).toHaveLength(2);
    expect(within(rows[0]!).getByText('Chapter 1')).toBeInTheDocument();
    expect(within(rows[0]!).getByRole('switch')).toBeChecked();
    expect(within(rows[1]!).getByRole('switch')).not.toBeChecked();
    expect(screen.getByText(/narrated: 1 chapters/)).toBeInTheDocument();
    expect(within(rows[0]!).getByText('4:05')).toBeInTheDocument();
    expect(screen.getByRole('link', { name: /listen/i })).toHaveAttribute(
      'href',
      '/books/1/listen',
    );
    expect(screen.getByText('Narrator')).toBeInTheDocument();
  });

  it('keeps the chapter list within a phone screen', async () => {
    const desktop = window.matchMedia;
    window.matchMedia = (query: string) => ({
      ...desktop(query),
      matches: query.includes('max-width'),
    });
    try {
      stubApi({
        '/api/voices': [{ id: 'af_heart', language: 'en-US', gender: 'female', grade: 'A' }],
        '/api/books/1': book,
        '/api/books/1/volumes': [],
        '/api/books/1/chapters': { items: chapters, total: 2 },
      });
      renderAt('/books/1/audiobook');
      const rows = await screen.findAllByTestId('chapter-row');
      // Title, audio, narrate and actions; no counts that widen the page.
      expect(within(rows[0]!).getByText('Chapter 1')).toBeInTheDocument();
      expect(within(rows[0]!).getByText('4:05')).toBeInTheDocument();
      expect(within(rows[0]!).getByRole('switch')).toBeChecked();
      expect(within(rows[0]!).queryByText('1,200')).not.toBeInTheDocument();
      expect(within(rows[0]!).queryByText('14')).not.toBeInTheDocument();
    } finally {
      window.matchMedia = desktop;
    }
  });

  it('plays the chapter of the saved position', async () => {
    const fetch = stubApi({
      '/api/books/1': book,
      '/api/books/1/chapters': {
        items: [
          chapters[0],
          {
            ...chapters[0],
            id: 12,
            idx: 2,
            title: 'Chapter 2',
            audioStatus: 'none',
            audioDurationMs: undefined,
          },
        ],
        total: 2,
      },
      'GET /api/books/1/progress': { chapterId: 12, positionMs: 5000 },
      'PUT /api/books/1/progress': { chapterId: 12, positionMs: 5000 },
    });
    renderAt('/books/1/listen');
    const player = await screen.findByTestId('player');
    expect(screen.getByText('2 / 2')).toBeInTheDocument();
    expect(within(player).getByText('Chapter 2')).toBeInTheDocument();
    expect(
      within(player).getByText('No audio yet; your reading position is kept.'),
    ).toBeInTheDocument();
    expect(within(player).getByRole('button', { name: 'Render now' })).toBeInTheDocument();
    // Entering the chapter stores the position (which queues the lookahead).
    await vi.waitFor(() =>
      expect(fetch.mock.calls.some(([r]) => typeof r !== 'string' && r.method === 'PUT')).toBe(
        true,
      ),
    );
  });

  it('reopens the player at the chapter listened to last', async () => {
    const two = {
      items: [
        chapters[0],
        { ...chapters[0], id: 12, idx: 2, title: 'Chapter 2', audioStatus: 'none' },
      ],
      total: 2,
    };
    const routes: Record<string, unknown> = {
      '/api/books/1': book,
      '/api/books/1/volumes': [],
      '/api/books/1/chapters': two,
      '/api/voices': voices,
      'GET /api/books/1/progress': { chapterId: 10, positionMs: 0 },
      'PUT /api/books/1/progress': { chapterId: 12, positionMs: 0 },
    };
    const fetch = stubApi(routes);
    renderAt('/books/1/listen');
    const player = await screen.findByTestId('player');
    expect(screen.getByText('1 / 2')).toBeInTheDocument();

    // The chapter list opens at the chapter playing, selected.
    fireEvent.click(within(player).getByRole('button', { name: 'Chapters' }));
    const list = await screen.findByRole('dialog', { name: 'Chapters' });
    const playingEntry = within(list).getByRole('button', { name: /Chapter 1/ });
    expect(playingEntry).toHaveAttribute('aria-current', 'true');
    await vi.waitFor(() => expect(playingEntry).toHaveFocus());
    fireEvent.click(within(list).getByRole('button', { name: /Chapter 2/ }));
    await vi.waitFor(() => expect(screen.getByText('2 / 2')).toBeInTheDocument());
    await vi.waitFor(() =>
      expect(
        fetch.mock.calls.some(
          ([r]) =>
            typeof r !== 'string' && r.method === 'PUT' && r.url.endsWith('/api/books/1/progress'),
        ),
      ).toBe(true),
    );
    routes['GET /api/books/1/progress'] = { chapterId: 12, positionMs: 0 };

    // To the book and back: the player starts where listening stopped,
    // and the contents on the book page mark that chapter.
    fireEvent.click(screen.getByRole('link', { name: 'Pride and Prejudice' }));
    const contents = await screen.findByRole('navigation', { name: 'Contents' });
    await vi.waitFor(() =>
      expect(within(contents).getByRole('button', { name: /Chapter 2/ })).toHaveAttribute(
        'aria-current',
        'true',
      ),
    );
    expect(within(contents).getByRole('button', { name: /Chapter 1/ })).not.toHaveAttribute(
      'aria-current',
    );
    fireEvent.click(screen.getByRole('link', { name: 'Continue reading' }));
    await screen.findByTestId('player');
    expect(screen.getByText('2 / 2')).toBeInTheDocument();
  });

  it('stores the position when the player is left while playing', async () => {
    const fetch = stubApi({
      '/api/books/1': book,
      '/api/books/1/volumes': [],
      '/api/books/1/chapters': { items: chapters, total: 2 },
      '/api/voices': voices,
      'GET /api/books/1/progress': { chapterId: 10, positionMs: 0 },
      'PUT /api/books/1/progress': { chapterId: 10, positionMs: 0 },
    });
    const { container } = renderAt('/books/1/listen');
    await screen.findByTestId('player');
    const audio = container.querySelector('audio')!;
    Object.defineProperty(audio, 'paused', { value: false });
    Object.defineProperty(audio, 'currentTime', { value: 42, writable: true });
    fireEvent.timeUpdate(audio);

    fireEvent.click(screen.getByRole('link', { name: 'Pride and Prejudice' }));
    await screen.findByRole('navigation', { name: 'Contents' });
    await vi.waitFor(async () => {
      const puts = fetch.mock.calls
        .map(([r]) => r)
        .filter((r): r is Request => typeof r !== 'string' && r.method === 'PUT');
      const last = puts[puts.length - 1]!;
      expect(last.keepalive).toBe(true);
      expect(await last.clone().json()).toMatchObject({ chapterId: 10, positionMs: 42000 });
    });
  });

  it('keeps a later chapter when the player is left after pausing', async () => {
    const fetch = stubApi({
      '/api/books/1': book,
      '/api/books/1/volumes': [],
      '/api/books/1/chapters': {
        items: [
          chapters[0],
          { ...chapters[0], id: 12, idx: 2, title: 'Chapter 2', audioStatus: 'none' },
        ],
        total: 2,
      },
      '/api/voices': voices,
      'GET /api/books/1/progress': { chapterId: 10, positionMs: 0 },
      'PUT /api/books/1/progress': { chapterId: 12, positionMs: 0 },
    });
    const { container } = renderAt('/books/1/listen');
    await screen.findByTestId('player');
    const audio = container.querySelector('audio')!;
    let paused = false;
    Object.defineProperty(audio, 'paused', { get: () => paused });
    Object.defineProperty(audio, 'currentTime', { value: 42, writable: true });
    fireEvent.timeUpdate(audio);
    paused = true;
    fireEvent.pause(audio);
    fireEvent.click(screen.getByRole('button', { name: 'Next chapter' }));
    await vi.waitFor(() => expect(container.querySelector('audio')).toBeNull());

    fireEvent.click(screen.getByRole('link', { name: 'Pride and Prejudice' }));
    await screen.findByRole('navigation', { name: 'Contents' });
    const puts = async () =>
      Promise.all(
        fetch.mock.calls
          .map(([r]) => r)
          .filter((r): r is Request => typeof r !== 'string' && r.method === 'PUT')
          .map((r) => r.clone().json() as Promise<{ chapterId: number }>),
      );
    await vi.waitFor(async () => expect((await puts()).at(-1)).toMatchObject({ chapterId: 12 }));
    await new Promise((r) => setTimeout(r, 50));
    expect((await puts()).at(-1)).toMatchObject({ chapterId: 12, positionMs: 0 });
  });

  // Holds the position saves until a test lets each one through or fails it.
  function holdSaves(fetch: ReturnType<typeof stubApi>) {
    const inner = fetch.getMockImplementation()!;
    const held: { body: unknown; resolve: () => void; reject: (e: Error) => void }[] = [];
    fetch.mockImplementation(async (input: Request | string) => {
      if (typeof input === 'string' || input.method !== 'PUT') return inner(input);
      const body: unknown = await input.clone().json();
      return new Promise<Response>((resolve, reject) =>
        held.push({ body, resolve: () => void inner(input).then(resolve), reject }),
      );
    });
    return held;
  }
  const settle = () => new Promise((r) => setTimeout(r, 20));
  const playerAt = async (fetch: ReturnType<typeof stubApi>, container: HTMLElement) => {
    await screen.findByTestId('player');
    const held = holdSaves(fetch);
    await settle();
    held.splice(0).forEach((h) => h.resolve());
    const audio = container.querySelector('audio')!;
    const state = { paused: true, t: 100 };
    Object.defineProperty(audio, 'paused', { get: () => state.paused });
    Object.defineProperty(audio, 'currentTime', {
      get: () => state.t,
      set: (v: number) => (state.t = v),
    });
    return { held, audio, state };
  };
  const oneChapter = () =>
    stubApi({
      '/api/books/1': book,
      '/api/books/1/volumes': [],
      '/api/books/1/chapters': { items: [chapters[0]], total: 1 },
      '/api/voices': voices,
      'GET /api/books/1/progress': { chapterId: 10, positionMs: 0 },
      'PUT /api/books/1/progress': { chapterId: 10, positionMs: 0 },
    });

  it('does not resend a failed save over a later one', async () => {
    const fetch = oneChapter();
    const { container } = renderAt('/books/1/listen');
    const { held, audio, state } = await playerAt(fetch, container);
    fireEvent.pause(audio);
    await settle();
    expect(held.map((h) => h.body)).toMatchObject([{ positionMs: 100000 }]);
    state.paused = false;
    state.t = 200;
    fireEvent.timeUpdate(audio);
    state.paused = true;
    fireEvent.pause(audio);
    await settle();
    held.shift()!.reject(new TypeError('network'));
    await settle();
    expect(held.map((h) => h.body)).toMatchObject([{ positionMs: 200000 }]);
    held.shift()!.resolve();
    await settle();

    fireEvent.click(screen.getByRole('link', { name: 'Pride and Prejudice' }));
    await screen.findByRole('navigation', { name: 'Contents' });
    await settle();
    expect(held).toEqual([]);
  });

  it('stores the position right away when the tab closes during a save', async () => {
    const fetch = oneChapter();
    const { container } = renderAt('/books/1/listen');
    const { held, audio, state } = await playerAt(fetch, container);
    fireEvent.pause(audio);
    await settle();
    state.paused = false;
    state.t = 110;
    fireEvent.timeUpdate(audio);
    window.dispatchEvent(new Event('pagehide'));
    await settle();
    expect(held.map((h) => h.body)).toMatchObject([{ positionMs: 100000 }, { positionMs: 110000 }]);
  });

  it('opens the player offline while a save waits for the network', async () => {
    const fetch = oneChapter();
    const { container } = renderAt('/books/1/listen');
    const { audio } = await playerAt(fetch, container);
    onlineManager.setOnline(false);
    try {
      fireEvent.pause(audio);
      fireEvent.click(screen.getByRole('link', { name: 'Pride and Prejudice' }));
      fireEvent.click(await screen.findByRole('link', { name: /^(Read|Continue reading)$/ }));
      expect(await screen.findByTestId('player')).toBeInTheDocument();
    } finally {
      onlineManager.setOnline(true);
    }
  });

  it('does not send a waiting save after the tab was closed and kept', async () => {
    const fetch = oneChapter();
    const { container } = renderAt('/books/1/listen');
    const { held, audio, state } = await playerAt(fetch, container);
    fireEvent.pause(audio);
    await settle();
    state.t = 105;
    fireEvent.pause(audio);
    state.paused = false;
    state.t = 110;
    fireEvent.timeUpdate(audio);
    window.dispatchEvent(new Event('pagehide'));
    await settle();
    expect(held.map((h) => h.body)).toMatchObject([{ positionMs: 100000 }, { positionMs: 110000 }]);
    held.shift()!.resolve();
    await settle();
    expect(held.map((h) => h.body)).toMatchObject([{ positionMs: 110000 }]);
  });

  it('stores the closing position later when its save failed', async () => {
    const fetch = oneChapter();
    const { container } = renderAt('/books/1/listen');
    const { held, audio, state } = await playerAt(fetch, container);
    fireEvent.pause(audio);
    await settle();
    state.paused = false;
    state.t = 110;
    fireEvent.timeUpdate(audio);
    window.dispatchEvent(new Event('pagehide'));
    await settle();
    held.pop()!.reject(new TypeError('offline'));
    await settle();
    held.shift()!.resolve();
    await settle();
    expect(held.map((h) => h.body)).toMatchObject([{ positionMs: 110000 }]);
  });

  it('plays ready audio', async () => {
    stubApi({
      '/api/books/1': book,
      '/api/books/1/chapters': { items: chapters, total: 2 },
      'GET /api/books/1/progress': { positionMs: 0 },
      'PUT /api/books/1/progress': { chapterId: 10, positionMs: 0 },
    });
    const { container } = renderAt('/books/1/listen');
    const player = await screen.findByTestId('player');
    expect(within(player).getByText('Chapter 1')).toBeInTheDocument();
    const audio = container.querySelector('audio');
    expect(audio).toHaveAttribute('src', expect.stringContaining('/api/chapters/10/audio'));
    expect(screen.getByRole('button', { name: 'Play' })).toBeEnabled();
  });

  const readText = (id: number, audioStatus: string) => ({
    ...chapters[0],
    id,
    audioStatus,
    paragraphs: [
      {
        id: 200,
        idx: 0,
        kind: 'text',
        text: 'It was late. “Come in,” she said.',
        segments: [
          {
            id: 201,
            idx: 0,
            kind: 'narration',
            quoteRef: 0,
            text: 'It was late.',
            confidence: 1,
            speakerManual: false,
          },
          {
            id: 202,
            idx: 1,
            kind: 'quote',
            quoteRef: 1,
            text: '“Come in,”',
            confidence: 1,
            speakerManual: false,
          },
          {
            id: 203,
            idx: 2,
            kind: 'narration',
            quoteRef: 0,
            text: 'she said.',
            confidence: 1,
            speakerManual: false,
          },
          {
            id: 204,
            idx: 3,
            kind: 'narration',
            quoteRef: 0,
            text: '…',
            confidence: 1,
            speakerManual: false,
          },
        ],
      },
    ],
  });
  const progressPuts = (fetch: ReturnType<typeof stubApi>) =>
    fetch.mock.calls
      .map(([r]) => r)
      .filter((r): r is Request => typeof r !== 'string' && r.method === 'PUT');

  it('changes and keeps the reading settings', async () => {
    stubApi({
      '/api/books/1': book,
      '/api/books/1/chapters': { items: chapters, total: 2 },
      '/api/chapters/10': readText(10, 'ready'),
      '/api/chapters/10/timings': { chapterId: 10, version: 'abc', exact: true, cues: [] },
      'GET /api/books/1/progress': { chapterId: 10, positionMs: 0 },
      'PUT /api/books/1/progress': { chapterId: 10, positionMs: 0 },
    });
    renderAt('/books/1/listen');
    const reader = await screen.findByTestId('reader');
    fireEvent.click(screen.getByRole('button', { name: 'Reading settings' }));
    const settings = await screen.findByRole('dialog', { name: 'Reading settings' });
    fireEvent.click(within(settings).getByRole('radio', { name: 'Justified' }));
    fireEvent.click(within(settings).getByRole('radio', { name: 'Sepia' }));
    fireEvent.click(within(settings).getByRole('switch', { name: /saved position/ }));
    expect(reader.style.textAlign).toBe('justify');
    expect(document.documentElement.dataset.paper).toBe('sepia');
    await vi.waitFor(() =>
      expect(JSON.parse(localStorage.getItem('abg.readerPrefs')!)).toMatchObject({
        align: 'justify',
        paper: 'sepia',
        showPosition: false,
      }),
    );

    fireEvent.click(within(settings).getByRole('button', { name: 'Reset to defaults' }));
    expect(reader.style.textAlign).toBe('start');
    expect(document.documentElement.dataset.paper).toBe('default');
    // The page colour is the reader's only.
    fireEvent.click(screen.getByRole('link', { name: 'Pride and Prejudice' }));
    await vi.waitFor(() => expect(document.documentElement.dataset.paper).toBeUndefined());
  });

  it('reads along: a click on a passage listens from there', async () => {
    const fetch = stubApi({
      '/api/books/1': book,
      '/api/books/1/chapters': { items: chapters, total: 2 },
      '/api/chapters/10': readText(10, 'ready'),
      '/api/chapters/10/timings': {
        chapterId: 10,
        version: 'abc',
        exact: true,
        cues: [
          { segmentId: 201, startMs: 300, endMs: 1200 },
          { segmentId: 202, startMs: 1350, endMs: 2000 },
          { segmentId: 203, startMs: 2150, endMs: 2900 },
        ],
      },
      'GET /api/books/1/progress': { chapterId: 10, positionMs: 0 },
      'PUT /api/books/1/progress': { chapterId: 10, positionMs: 0 },
    });
    renderAt('/books/1/listen');
    fireEvent.click(await screen.findByText('“Come in,”'));
    await vi.waitFor(async () => {
      const bodies = await Promise.all(progressPuts(fetch).map((r) => r.clone().json()));
      expect(bodies).toContainEqual({ chapterId: 10, positionMs: 1350, segmentId: 202 });
    });
    // A segment that is not spoken stands for its spoken neighbour.
    fireEvent.click(screen.getByText('…'));
    await vi.waitFor(async () => {
      const bodies = await Promise.all(progressPuts(fetch).map((r) => r.clone().json()));
      expect(bodies).toContainEqual({ chapterId: 10, positionMs: 2150, segmentId: 203 });
    });
  });

  it('keeps the reading position of a chapter without audio', async () => {
    const fetch = stubApi({
      '/api/books/1': book,
      '/api/books/1/chapters': {
        items: [{ ...chapters[0], id: 12, title: 'Chapter 2', audioStatus: 'none' }],
        total: 1,
      },
      '/api/chapters/12': readText(12, 'none'),
      'GET /api/books/1/progress': { chapterId: 12, positionMs: 0, segmentId: 202 },
      'PUT /api/books/1/progress': { chapterId: 12, positionMs: 0 },
    });
    renderAt('/books/1/listen');
    await screen.findByText(/No audio yet; your reading position is kept/);
    // The stored position is marked and kept when the chapter is entered.
    expect((await screen.findByText('“Come in,”')).style.background).toContain('yellow');
    await vi.waitFor(async () => {
      const bodies = await Promise.all(progressPuts(fetch).map((r) => r.clone().json()));
      expect(bodies).toContainEqual({ chapterId: 12, positionMs: 0, segmentId: 202 });
    });
    fireEvent.click(screen.getByText('she said.'));
    await vi.waitFor(async () => {
      const bodies = await Promise.all(progressPuts(fetch).map((r) => r.clone().json()));
      expect(bodies).toContainEqual({ chapterId: 12, positionMs: 0, segmentId: 203 });
    });
    expect(screen.getByText('she said.').style.background).toContain('yellow');
  });

  it('lists jobs', async () => {
    stubApi({
      '/api/jobs': {
        items: [
          {
            id: 7,
            type: 'render_chapter',
            status: 'failed',
            bookId: 1,
            chapterId: 10,
            attempts: 3,
            maxAttempts: 3,
            progress: 0.4,
            error: 'tts: 503',
            createdAt: '2026-10-15T00:00:00Z',
            updatedAt: '2026-10-15T00:00:00Z',
          },
        ],
        total: 1,
      },
    });
    renderAt('/jobs');
    expect(await screen.findByText('render_chapter')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Retry job 7' })).toBeInTheDocument();
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
        narration: 'third_person',
        povCharacterId: 7,
        characters: [
          { id: 7, name: 'Erin Solstice' },
          { id: 8, name: 'Lyonette' },
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
    const cast = screen.getByTestId('chapter-characters');
    expect(within(cast).getByText(/Third person, POV Erin Solstice/)).toBeInTheDocument();
    expect(within(cast).getByRole('link', { name: 'Lyonette' })).toHaveAttribute(
      'href',
      '/books/1/characters/8',
    );
  });

  it('shows and edits the speakers of a chapter', async () => {
    const detail = {
      ...chapters[0],
      attributionStatus: 'done',
      paragraphs: [
        {
          id: 101,
          idx: 0,
          kind: 'text',
          text: '“My dear,” said his lady. “Come.”',
          segments: [
            {
              id: 2,
              idx: 0,
              kind: 'quote',
              quoteRef: 1,
              text: '“My dear,”',
              speakerId: 7,
              emotion: 'happy',
              confidence: 0.9,
              speakerManual: false,
            },
            {
              id: 3,
              idx: 1,
              kind: 'narration',
              quoteRef: 0,
              text: 'said his lady.',
              confidence: 0,
              speakerManual: false,
            },
            {
              id: 4,
              idx: 2,
              kind: 'quote',
              quoteRef: 2,
              text: '“Come.”',
              confidence: 0.2,
              speakerManual: false,
            },
          ],
        },
        {
          id: 102,
          idx: 1,
          kind: 'text',
          text: 'Nothing else.',
          segments: [
            {
              id: 5,
              idx: 3,
              kind: 'narration',
              quoteRef: 0,
              text: 'Nothing else.',
              confidence: 0,
              speakerManual: false,
            },
          ],
        },
      ],
      characters: [
        { id: 7, name: 'Erin Solstice' },
        { id: 8, name: 'Lyonette' },
      ],
      speakers: [{ id: 7, name: 'Erin Solstice' }],
      attribution: {
        status: 'done',
        quotes: 2,
        attributed: 1,
        unsure: 1,
        unsureBelow: 0.6,
        prompt: 'attribute_chapter@v1',
      },
    };
    const fetch = stubApi({
      '/api/books/1': book,
      'GET /api/chapters/10': detail,
      'PATCH /api/chapters/10/quotes/2': detail,
    });
    renderAt('/books/1/chapters/10');
    const bar = await screen.findByTestId('attribution-bar');
    expect(within(bar).getByText(/1 of 2 quotes with a speaker, 1 to check/)).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Erin Solstice: “My dear,”' })).toHaveTextContent(
      'Erin Solstice · happy',
    );
    fireEvent.click(within(bar).getByRole('button', { name: 'Show only those to check' }));
    expect(screen.queryByText('Nothing else.')).not.toBeInTheDocument();
    expect(within(bar).getByRole('button', { name: 'Show all' })).toBeInTheDocument();

    fireEvent.click(screen.getByRole('button', { name: 'Narrator (to check): “Come.”' }));
    fireEvent.click((await screen.findAllByLabelText('Speaker'))[0]!);
    fireEvent.click(await screen.findByRole('option', { name: 'Lyonette', hidden: true }));
    // (The popover is still fading in under jsdom, hence hidden.)
    fireEvent.click(screen.getByRole('button', { name: 'Save', hidden: true }));
    await vi.waitFor(() =>
      expect(fetch.mock.calls.some(([r]) => typeof r !== 'string' && r.method === 'PATCH')).toBe(
        true,
      ),
    );
    const patch = fetch.mock.calls.find(([r]) => typeof r !== 'string' && r.method === 'PATCH')!;
    expect(await (patch[0] as Request).json()).toEqual({ speakerId: 8, emotion: 'neutral' });
  });

  it('lets the narrator read everything', async () => {
    const fetch = stubApi({
      '/api/books/1': book,
      '/api/books/1/chapters': { items: [], total: 0 },
      '/api/voices': voices,
      'PATCH /api/books/1': { ...book, characterVoices: false },
    });
    renderAt('/books/1/audiobook');
    const voicesSwitch = await screen.findByRole('switch', { name: /Character voices/ });
    expect(voicesSwitch).toBeChecked();
    fireEvent.click(voicesSwitch);
    const card = voicesSwitch.closest('.mantine-Card-root') as HTMLElement;
    fireEvent.click(within(card).getByRole('button', { name: 'Save' }));
    await vi.waitFor(() =>
      expect(fetch.mock.calls.some(([r]) => typeof r !== 'string' && r.method === 'PATCH')).toBe(
        true,
      ),
    );
    const patch = fetch.mock.calls.find(([r]) => typeof r !== 'string' && r.method === 'PATCH')!;
    expect(await (patch[0] as Request).json()).toMatchObject({ characterVoices: false });
  });

  it('shows database and TTS status', async () => {
    stubApi({
      '/api/system': {
        version: '0.1.0',
        commit: 'abc123',
        maxUploadBytes: 536870912,
        database: { dialect: 'postgres', schemaVersion: '202610150002_book_content_and_jobs' },
        tts: {
          url: 'http://kokoro:8880',
          reachable: false,
          configured: true,
          voiceDesign: false,
          defaultVoice: 'af_heart',
          error: 'connection refused',
        },
      },
    });
    renderAt('/system');
    expect(await screen.findByText('postgres')).toBeInTheDocument();
    expect(screen.getByText('unreachable')).toBeInTheDocument();
    expect(screen.getByText('connection refused')).toBeInTheDocument();
  });

  it('shows a deployment without Kokoro', async () => {
    stubApi({
      '/api/system': {
        version: '0.1.0',
        commit: 'abc123',
        maxUploadBytes: 536870912,
        database: { dialect: 'sqlite', schemaVersion: 'x' },
        tts: {
          url: '',
          reachable: false,
          configured: false,
          voiceDesign: false,
          defaultVoice: 'chatterbox:Emily.wav',
        },
      },
      '/api/tts/engines': [
        {
          name: 'chatterbox',
          kind: 'chatterbox',
          variant: 'nano',
          emotions: false,
          speed: false,
          voices: ['Emily.wav'],
          defaultVoice: 'chatterbox:Emily.wav',
        },
      ],
    });
    renderAt('/system');
    expect(await screen.findByText('Not part of this deployment.')).toBeInTheDocument();
    expect(screen.getByText('chatterbox:Emily.wav')).toBeInTheDocument();
    expect(await screen.findByText('Speech engine chatterbox')).toBeInTheDocument();
    expect(screen.getByText('chatterbox (nano)')).toBeInTheDocument();
  });

  it('shows LLM providers with their quotas', async () => {
    stubApi({ '/api/llm/providers': [helmProvider, uiProvider] });
    renderAt('/settings');
    const helm = await screen.findByTestId('provider-gemini');
    expect(within(helm).getByText('quota exhausted')).toBeInTheDocument();
    expect(within(helm).getByText('Helm values')).toBeInTheDocument();
    expect(within(helm).getByText(/daily limit of 250 requests reached/)).toBeInTheDocument();
    expect(within(helm).getByText('250 / 250')).toBeInTheDocument();
    expect(within(helm).queryByRole('button', { name: /Edit/ })).not.toBeInTheDocument();
    expect(within(helm).getByRole('switch', { name: 'Enabled' })).toBeDisabled();

    const local = screen.getByTestId('provider-local');
    expect(within(local).getByText('ready')).toBeInTheDocument();
    expect(within(local).getAllByText('no limit')).toHaveLength(4);
    expect(within(local).getByText(/Last error: HTTP 503/)).toBeInTheDocument();
    expect(within(local).getByRole('button', { name: 'Move local up' })).toBeDisabled();
    expect(screen.getByText(/Book text is sent to these providers/)).toBeInTheDocument();
  });

  it('tests a provider', async () => {
    stubApi({
      '/api/llm/providers': [uiProvider],
      'POST /api/llm/providers/2/test': {
        ok: true,
        message: 'structured output works',
        model: 'qwen3:8b',
        latencyMs: 1234,
        inputTokens: 80,
        outputTokens: 12,
      },
    });
    renderAt('/settings');
    fireEvent.click(await screen.findByRole('button', { name: 'Test' }));
    expect(await screen.findByText('Test passed')).toBeInTheDocument();
    expect(screen.getByText('structured output works')).toBeInTheDocument();
    expect(screen.getByText(/1,234 ms/)).toBeInTheDocument();
  });

  it('adds a provider from a preset', async () => {
    const fetch = stubApi({
      'GET /api/llm/providers': [],
      'POST /api/llm/providers': { ...uiProvider, id: 3, name: 'groq' },
    });
    renderAt('/settings');
    expect(await screen.findByText(/No providers yet/)).toBeInTheDocument();
    fireEvent.click(screen.getAllByRole('button', { name: 'Add provider' })[0]!);
    const dialog = await screen.findByRole('dialog');
    fireEvent.change(within(dialog).getByLabelText(/^Base URL/), {
      target: { value: 'https://api.groq.com/openai/v1/' },
    });
    fireEvent.change(within(dialog).getByLabelText(/^Name/), { target: { value: 'groq' } });
    fireEvent.change(within(dialog).getByLabelText(/^Model/), { target: { value: 'llama-x' } });
    fireEvent.change(within(dialog).getByLabelText(/^API key/), { target: { value: 'gsk_1' } });
    fireEvent.change(within(dialog).getByLabelText('Requests / day'), {
      target: { value: '1000' },
    });
    fireEvent.click(within(dialog).getByRole('button', { name: 'Add provider' }));

    await vi.waitFor(() =>
      expect(fetch.mock.calls.some(([r]) => typeof r !== 'string' && r.method === 'POST')).toBe(
        true,
      ),
    );
    const post = fetch.mock.calls.find(([r]) => typeof r !== 'string' && r.method === 'POST')!;
    const body = (await (post[0] as Request).json()) as Record<string, unknown>;
    expect(body).toMatchObject({
      name: 'groq',
      baseUrl: 'https://api.groq.com/openai/v1/',
      model: 'llama-x',
      apiKey: 'gsk_1',
      structuredOutput: 'json_schema',
      limits: { rpm: 0, tpm: 0, rpd: 1000, tpd: 0 },
    });
  });

  it('asks for the key again when the base URL changes', async () => {
    stubApi({ '/api/llm/providers': [{ ...uiProvider, hasApiKey: true }] });
    renderAt('/settings');
    fireEvent.click(await screen.findByRole('button', { name: 'Edit local' }));
    const dialog = await screen.findByRole('dialog');
    const save = within(dialog).getByRole('button', { name: 'Save' });
    expect(save).toBeEnabled();
    fireEvent.change(within(dialog).getByLabelText(/^Base URL/), {
      target: { value: 'http://elsewhere:11434/v1/' },
    });
    expect(within(dialog).getByText(/Enter the key again/)).toBeInTheDocument();
    expect(save).toBeDisabled();
    fireEvent.change(within(dialog).getByLabelText(/^API key/), { target: { value: 'new' } });
    expect(save).toBeEnabled();
  });

  it('estimates and starts the character analysis', async () => {
    const fetch = stubApi({
      '/api/voices': [],
      '/api/books/1': book,
      '/api/books/1/volumes': [],
      '/api/books/1/chapters': { items: chapters, total: 2 },
      'GET /api/books/1/analysis': analysisBase,
      'POST /api/books/1/analysis': { ...analysisBase, status: 'queued' },
      '/api/llm/providers': [
        { ...helmProvider, limits: { rpm: 10, tpm: 250000, rpd: 100, tpd: 0 } },
      ],
    });
    renderAt('/books/1/audiobook');
    const card = await screen.findByTestId('analysis-card');
    expect(await within(card).findByText(/≈ 190\s+LLM requests/)).toBeInTheDocument();
    expect(within(card).getByText(/about 2 days/)).toBeInTheDocument();
    expect(within(card).getByText('not started')).toBeInTheDocument();
    fireEvent.click(within(card).getByRole('button', { name: 'Start analysis' }));
    await vi.waitFor(() =>
      expect(fetch.mock.calls.some(([r]) => typeof r !== 'string' && r.method === 'POST')).toBe(
        true,
      ),
    );
  });

  it('shows a running analysis waiting for quota', async () => {
    stubApi({
      '/api/voices': [],
      '/api/books/1': { ...book, analysisStatus: 'running', analysisProgress: 0.25 },
      '/api/books/1/volumes': [],
      '/api/books/1/chapters': { items: chapters, total: 2 },
      '/api/books/1/analysis': {
        ...analysisBase,
        status: 'running',
        batchesDone: 40,
        batchesTotal: 160,
        inputTokens: 1_200_000,
        outputTokens: 80_000,
        characterCount: 312,
        job: {
          id: 3,
          type: 'analyze_book',
          status: 'queued',
          attempts: 0,
          maxAttempts: 5,
          progress: 0.24,
          error: 'all LLM providers are busy',
          notBefore: '2099-01-01T00:00:00Z',
          createdAt: '2026-10-15T00:00:00Z',
          updatedAt: '2026-10-15T00:00:00Z',
        },
      },
      '/api/llm/providers': [],
    });
    renderAt('/books/1/audiobook');
    const card = await screen.findByTestId('analysis-card');
    expect(await within(card).findByText(/40 of 160 requests/)).toBeInTheDocument();
    expect(within(card).getByText(/1.3M tokens used/)).toBeInTheDocument();
    expect(within(card).getByText(/Waiting for LLM quota until/)).toBeInTheDocument();
    expect(within(card).getByRole('button', { name: 'Pause' })).toBeInTheDocument();
    expect(within(card).getByRole('link', { name: /312 characters/ })).toHaveAttribute(
      'href',
      '/books/1/characters',
    );
  });

  it('lists characters and merges a selection', async () => {
    const fetch = stubApi({
      '/api/books/1': book,
      '/api/books/1/analysis': { ...analysisBase, status: 'done', characterCount: 2 },
      'GET /api/books/1/characters': {
        items: [
          erin,
          { ...erin, id: 8, name: 'Erin', aliases: [], chapterCount: 3, importance: 2 },
        ],
        total: 2,
      },
      'POST /api/books/1/characters/merge': { ...erin, chapters: [] },
    });
    renderAt('/books/1/characters');
    expect(await screen.findByRole('link', { name: 'Erin Solstice' })).toHaveAttribute(
      'href',
      '/books/1/characters/7',
    );
    expect(screen.getByText('Erin, the Innkeeper')).toBeInTheDocument();
    fireEvent.click(screen.getByRole('checkbox', { name: 'Select Erin Solstice' }));
    fireEvent.click(screen.getByRole('checkbox', { name: 'Select Erin' }));
    fireEvent.click(screen.getByRole('button', { name: 'Merge 2 selected' }));
    const dialog = await screen.findByRole('dialog');
    expect(
      within(dialog).getByRole('radio', { name: /Erin Solstice \(812 chapters\)/ }),
    ).toBeChecked();
    fireEvent.click(within(dialog).getByRole('button', { name: 'Merge' }));
    await vi.waitFor(() =>
      expect(fetch.mock.calls.some(([r]) => typeof r !== 'string' && r.method === 'POST')).toBe(
        true,
      ),
    );
    const post = fetch.mock.calls.find(([r]) => typeof r !== 'string' && r.method === 'POST')!;
    expect(await (post[0] as Request).json()).toEqual({ targetId: 7, sourceIds: [8] });
  });

  it('edits a character', async () => {
    const fetch = stubApi({
      '/api/books/1': book,
      'GET /api/characters/7': {
        ...erin,
        chapters: [{ id: 10, idx: 0, title: 'Chapter 1' }],
      },
      'PATCH /api/characters/7': { ...erin, chapters: [] },
    });
    renderAt('/books/1/characters/7');
    expect(await screen.findByRole('heading', { name: 'Erin Solstice' })).toBeInTheDocument();
    expect(screen.getByRole('link', { name: 'Chapter 1' })).toHaveAttribute(
      'href',
      '/books/1/chapters/10',
    );
    const save = screen.getByRole('button', { name: 'Save' });
    expect(save).toBeDisabled();
    fireEvent.change(screen.getByLabelText('Speech style', { exact: false }), {
      target: { value: 'brisk' },
    });
    expect(save).toBeEnabled();
    fireEvent.click(save);
    await vi.waitFor(() =>
      expect(fetch.mock.calls.some(([r]) => typeof r !== 'string' && r.method === 'PATCH')).toBe(
        true,
      ),
    );
    const patch = fetch.mock.calls.find(([r]) => typeof r !== 'string' && r.method === 'PATCH')!;
    expect(await (patch[0] as Request).json()).toMatchObject({
      name: 'Erin Solstice',
      speechStyle: 'brisk',
      aliases: ['Erin', 'the Innkeeper'],
    });
  });

  it('shows the casting with voices that are hard to tell apart', async () => {
    const fetch = stubApi({
      '/api/books/1': { ...book, analysisStatus: 'done', castingStatus: 'done' },
      '/api/books/1/analysis': { ...analysisBase, status: 'done', characterCount: 340 },
      '/api/books/1/volumes': [],
      '/api/books/1/chapters': { items: chapters, total: 2 },
      '/api/voices': voices,
      '/api/books/1/casting': {
        ...castingBase,
        warnings: [
          { kind: 'narrator', characterId: 7, characterName: 'Erin Solstice', distance: 0.3 },
          {
            kind: 'similar',
            characterId: 8,
            characterName: 'Lyonette',
            otherId: 7,
            otherName: 'Erin Solstice',
            distance: 0.1,
          },
        ],
      },
      'POST /api/books/1/casting': { ...castingBase, status: 'queued' },
    });
    renderAt('/books/1/audiobook');
    const card = await screen.findByTestId('casting-card');
    expect(
      within(card).getByText('40 of 40 main characters voiced · 300 with pool voices'),
    ).toBeInTheDocument();
    expect(within(card).getByText('sounds like the narrator')).toBeInTheDocument();
    expect(within(card).getByRole('link', { name: 'Lyonette' })).toHaveAttribute(
      'href',
      '/books/1/characters/8',
    );
    fireEvent.click(within(card).getByRole('button', { name: 'Recast all' }));
    fireEvent.click(await screen.findByRole('button', { name: 'Recast' }));
    await vi.waitFor(() =>
      expect(fetch.mock.calls.some(([r]) => typeof r !== 'string' && r.method === 'POST')).toBe(
        true,
      ),
    );
    const post = fetch.mock.calls.find(([r]) => typeof r !== 'string' && r.method === 'POST')!;
    expect(await (post[0] as Request).json()).toEqual({ restart: true });
  });

  it('shows voices in the character list and filters by tier', async () => {
    const fetch = stubApi({
      '/api/books/1': book,
      '/api/books/1/analysis': { ...analysisBase, status: 'done', characterCount: 2 },
      'GET /api/books/1/characters': {
        items: [
          erin,
          {
            ...erin,
            id: 8,
            name: 'Guard',
            aliases: [],
            tier: 'minor',
            gender: 'male',
            voice: 'am_michael',
            voiceSpeed: 0.92,
            voiceSource: 'pool',
            ageGroup: 'elderly',
          },
        ],
        total: 2,
      },
    });
    renderAt('/books/1/characters');
    expect(await screen.findByText('bf_emma(0.7)+af_bella(0.3)')).toBeInTheDocument();
    expect(screen.getByText('cast')).toBeInTheDocument();
    expect(screen.getByText('am_michael · 0.92×')).toBeInTheDocument();
    expect(screen.getByText('pool voice (m, elderly)')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Listen to Guard' })).toBeEnabled();
    fireEvent.click(screen.getByRole('radio', { name: 'Main' }));
    await vi.waitFor(() =>
      expect(
        fetch.mock.calls.some(
          ([r]) => typeof r !== 'string' && new URL(r.url).searchParams.get('tier') === 'main',
        ),
      ).toBe(true),
    );
  });

  it('edits the voice of a character', async () => {
    const fetch = stubApi({
      '/api/books/1': book,
      '/api/voices': voices,
      'GET /api/characters/7': { ...erin, chapters: [] },
      'GET /api/characters/7/quotes': [
        { text: '“Welcome to the inn!”', chapterId: 10, chapterIdx: 0, chapterTitle: 'Chapter 1' },
      ],
      'PATCH /api/characters/7': { ...erin, chapters: [] },
    });
    renderAt('/books/1/characters/7');
    const editor = await screen.findByTestId('voice-editor');
    expect(await within(editor).findByText('“Welcome to the inn!”')).toBeInTheDocument();
    expect(within(editor).getByText('Cast automatically: warm and British')).toBeInTheDocument();
    const save = within(editor).getByRole('button', { name: 'Save voice' });
    expect(save).toBeDisabled();
    fireEvent.click(within(editor).getByRole('button', { name: 'Remove af_bella' }));
    expect(within(editor).getByText('bf_emma')).toBeInTheDocument();
    fireEvent.click(within(editor).getByRole('radio', { name: 'Main' }));
    fireEvent.click(save);
    await vi.waitFor(() =>
      expect(fetch.mock.calls.some(([r]) => typeof r !== 'string' && r.method === 'PATCH')).toBe(
        true,
      ),
    );
    const patch = fetch.mock.calls.find(([r]) => typeof r !== 'string' && r.method === 'PATCH')!;
    expect(await (patch[0] as Request).json()).toEqual({
      tier: 'main',
      voice: 'bf_emma',
      voiceSpeed: 1,
    });
  });

  it('gives a character a sample text of its own', async () => {
    const fetch = stubApi({
      '/api/books/1': book,
      '/api/voices': voices,
      'GET /api/characters/7': { ...erin, chapters: [] },
      'GET /api/characters/7/quotes': [
        { text: '“Welcome to the inn!”', chapterId: 10, chapterIdx: 0, chapterTitle: 'Chapter 1' },
      ],
      'PATCH /api/characters/7': { ...erin, chapters: [] },
    });
    renderAt('/books/1/characters/7');
    const editor = await screen.findByTestId('voice-editor');
    await within(editor).findByText('“Welcome to the inn!”');
    fireEvent.click(within(editor).getByRole('radio', { name: 'Own sample text' }));
    fireEvent.change(within(editor).getByRole('textbox', { name: 'Sample text' }), {
      target: { value: 'Mind the stairs.' },
    });
    fireEvent.click(within(editor).getByRole('button', { name: 'Save voice' }));
    await vi.waitFor(() =>
      expect(fetch.mock.calls.some(([r]) => typeof r !== 'string' && r.method === 'PATCH')).toBe(
        true,
      ),
    );
    const patch = fetch.mock.calls.find(([r]) => typeof r !== 'string' && r.method === 'PATCH')!;
    expect(await (patch[0] as Request).json()).toEqual({ previewText: 'Mind the stairs.' });
    // The preview says it.
    fireEvent.click(within(editor).getByRole('button', { name: 'Listen to this voice' }));
    await vi.waitFor(() =>
      expect(
        fetch.mock.calls.some(([r]) => typeof r === 'string' && r.endsWith('/voices/preview')),
      ).toBe(true),
    );
    const call = fetch.mock.calls.find(
      ([r]) => typeof r === 'string' && r.endsWith('/voices/preview'),
    )! as unknown as [string, RequestInit];
    expect(JSON.parse(call[1].body as string)).toMatchObject({ text: 'Mind the stairs.' });
  });

  it('saves the sample text of voice previews', async () => {
    const fetch = stubApi({
      '/api/llm/providers': [],
      'GET /api/settings': {
        previewText: '',
        defaultPreviewText: 'It was a bright cold day.',
        unloadIdleModels: true,
        unloadIdleSeconds: 600,
      },
      'PATCH /api/settings': {
        previewText: 'Call me Ishmael.',
        defaultPreviewText: 'It was a bright cold day.',
        unloadIdleModels: true,
        unloadIdleSeconds: 600,
      },
    });
    renderAt('/settings');
    const input = await screen.findByRole('textbox', { name: 'Sample text' });
    expect(input).toHaveAttribute('placeholder', 'It was a bright cold day.');
    fireEvent.change(input, { target: { value: 'Call me Ishmael.' } });
    fireEvent.click(screen.getByRole('button', { name: 'Save' }));
    await vi.waitFor(() =>
      expect(fetch.mock.calls.some(([r]) => typeof r !== 'string' && r.method === 'PATCH')).toBe(
        true,
      ),
    );
    const patch = fetch.mock.calls.find(([r]) => typeof r !== 'string' && r.method === 'PATCH')!;
    expect(await (patch[0] as Request).json()).toEqual({ previewText: 'Call me Ishmael.' });
    expect(
      await screen.findByRole('button', { name: 'Use the built-in text' }),
    ).toBeInTheDocument();
  });

  it('turns off unloading idle speech models', async () => {
    const fetch = stubApi({
      '/api/llm/providers': [],
      'GET /api/settings': {
        previewText: '',
        defaultPreviewText: 'It was a bright cold day.',
        unloadIdleModels: true,
        unloadIdleSeconds: 600,
      },
      'PATCH /api/settings': {
        previewText: '',
        defaultPreviewText: 'It was a bright cold day.',
        unloadIdleModels: false,
        unloadIdleSeconds: 600,
      },
    });
    renderAt('/settings');
    const toggle = await screen.findByRole('switch', { name: /Unload idle models/ });
    expect(toggle).toBeChecked();
    expect(screen.getByText(/after 10 minutes without speaking/)).toBeInTheDocument();
    fireEvent.click(toggle);
    await vi.waitFor(() =>
      expect(fetch.mock.calls.some(([r]) => typeof r !== 'string' && r.method === 'PATCH')).toBe(
        true,
      ),
    );
    const patch = fetch.mock.calls.find(([r]) => typeof r !== 'string' && r.method === 'PATCH')!;
    expect(await (patch[0] as Request).json()).toEqual({ unloadIdleModels: false });
    await vi.waitFor(() => expect(toggle).not.toBeChecked());
  });

  it('lists the voice catalogue', async () => {
    stubApi({ '/api/voices': voices });
    renderAt('/voices');
    expect(await screen.findByText('refined')).toBeInTheDocument();
    expect(screen.getByText('not on the server')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Listen to af_heart' })).toBeEnabled();
    expect(screen.getByRole('button', { name: 'Listen to am_adam' })).toBeDisabled();
  });

  it('shows when a deferred job runs again', async () => {
    stubApi({
      '/api/jobs': {
        items: [
          {
            id: 8,
            type: 'analyze_batch',
            status: 'queued',
            attempts: 0,
            maxAttempts: 3,
            progress: 0,
            error: 'all LLM providers are busy',
            notBefore: '2099-01-01T00:00:00Z',
            createdAt: '2026-10-15T00:00:00Z',
            updatedAt: '2026-10-15T00:00:00Z',
          },
        ],
        total: 1,
      },
    });
    renderAt('/jobs');
    expect(await screen.findByText(/waits until/)).toBeInTheDocument();
  });

  it('builds and offers M4B downloads per volume', async () => {
    const fetch = stubApi({
      '/api/books/1': book,
      '/api/books/1/volumes': [],
      '/api/books/1/chapters': { items: [], total: 0 },
      'GET /api/books/1/downloads': [
        {
          volumeId: 5,
          title: 'Book One',
          chapterCount: 12,
          renderedCount: 12,
          status: 'ready',
          hasFile: true,
          outdated: false,
          sizeBytes: 300 << 20,
          durationMs: 3_725_000,
        },
        {
          volumeId: 6,
          title: 'Book Two',
          chapterCount: 10,
          renderedCount: 4,
          status: 'none',
          hasFile: false,
          outdated: false,
        },
      ],
      'POST /api/books/1/downloads/6': {
        volumeId: 6,
        title: 'Book Two',
        chapterCount: 10,
        renderedCount: 4,
        status: 'queued',
        hasFile: false,
        outdated: false,
      },
    });
    renderAt('/books/1/audiobook');
    const card = await screen.findByTestId('downloads-card');
    expect(within(card).getByText(/1:02:05 · 300 MB/)).toBeInTheDocument();
    expect(within(card).getByRole('link', { name: 'Download Book One' })).toHaveAttribute(
      'href',
      'http://localhost:3000/api/books/1/downloads/5/file',
    );
    expect(within(card).queryByRole('link', { name: 'Download Book Two' })).toBeNull();
    fireEvent.click(within(card).getByRole('button', { name: 'Build' }));
    await vi.waitFor(() =>
      expect(
        fetch.mock.calls.some(
          ([r]) => typeof r !== 'string' && r.method === 'POST' && r.url.endsWith('/downloads/6'),
        ),
      ).toBe(true),
    );
  });

  it('shows the signed-in user and signs in again when the session ends', async () => {
    const assign = vi.fn();
    vi.stubGlobal('location', { ...window.location, assign, pathname: '/jobs', search: '' });
    const fetch = vi.fn(async (input: Request | string) => {
      const url = new URL(typeof input === 'string' ? input : input.url);
      if (url.pathname === '/api/me') {
        return new Response(JSON.stringify({ authEnabled: true, name: 'Erin Solstice' }), {
          status: 200,
          headers: { 'Content-Type': 'application/json' },
        });
      }
      return new Response(JSON.stringify({ message: 'sign-in required' }), { status: 401 });
    });
    vi.stubGlobal('fetch', fetch);
    renderAt('/jobs');
    expect(await screen.findByText('Erin Solstice')).toBeInTheDocument();
    expect(screen.getByRole('link', { name: 'Sign out' })).toHaveAttribute('href', '/auth/logout');
    await vi.waitFor(() => expect(assign).toHaveBeenCalledWith('/auth/login?redirect=%2Fjobs'));
    vi.unstubAllGlobals();
  });

  it('imports a book export', async () => {
    const fetch = stubApi({
      '/api/books': { items: [], total: 0 },
      'POST /api/import': { ...book, id: 9, status: 'importing' },
    });
    const { container } = renderAt('/library');
    await screen.findByRole('button', { name: 'Import export' });
    const input = container.querySelector('input[type="file"][accept=".zip,application/zip"]')!;
    fireEvent.change(input, {
      target: { files: [new File(['zip'], 'book.abg.zip', { type: 'application/zip' })] },
    });
    await vi.waitFor(() =>
      expect(fetch.mock.calls.some(([r]) => String(r).endsWith('/api/import'))).toBe(true),
    );
  });

  it('links the export of a book', async () => {
    stubApi({
      '/api/books/1': book,
      '/api/books/1/volumes': [],
      '/api/books/1/chapters': { items: [], total: 0 },
    });
    renderAt('/books/1');
    fireEvent.click(await screen.findByRole('button', { name: 'More' }));
    expect(await screen.findByRole('menuitem', { name: 'Export (without audio)' })).toHaveAttribute(
      'href',
      'http://localhost:3000/api/books/1/export',
    );
  });

  it('shows the podcast feed URL', async () => {
    stubApi({
      '/api/books/1': { ...book, feedPath: '/feed/tok/1.xml' },
      '/api/books/1/volumes': [],
      '/api/books/1/chapters': { items: [], total: 0 },
    });
    renderAt('/books/1/audiobook');
    fireEvent.click(await screen.findByRole('button', { name: 'Podcast' }));
    expect(await screen.findByLabelText('Feed URL')).toHaveValue(
      'http://localhost:3000/feed/tok/1.xml',
    );
  });

  it('lists, adds and previews pronunciations', async () => {
    const fetch = stubApi({
      '/api/books/1': book,
      'GET /api/books/1/lexicon': [
        { id: 3, bookId: 1, term: 'Ryoka', replacement: 'ɹiˈoʊkə', phonemes: true },
      ],
      'POST /api/books/1/lexicon': {
        id: 4,
        bookId: 1,
        term: 'Erin',
        replacement: 'Air-in',
        phonemes: false,
      },
      'DELETE /api/lexicon/3': {},
    });
    renderAt('/books/1/lexicon');
    expect(await screen.findByText('ɹiˈoʊkə')).toBeInTheDocument();
    expect(screen.getByText('IPA')).toBeInTheDocument();

    fireEvent.change(screen.getByLabelText('Word or name'), { target: { value: ' Erin ' } });
    fireEvent.change(screen.getByLabelText('Say instead'), { target: { value: 'Air-in' } });
    fireEvent.click(screen.getByRole('button', { name: 'Add' }));
    await vi.waitFor(() =>
      expect(fetch.mock.calls.some(([r]) => typeof r !== 'string' && r.method === 'POST')).toBe(
        true,
      ),
    );
    const post = fetch.mock.calls.find(([r]) => typeof r !== 'string' && r.method === 'POST')!;
    expect(await (post[0] as Request).json()).toEqual({
      term: 'Erin',
      replacement: 'Air-in',
      phonemes: false,
    });

    // A saved entry is previewed through the book's lexicon.
    fireEvent.click(screen.getByRole('button', { name: 'Listen to Ryoka' }));
    await vi.waitFor(() =>
      expect(fetch.mock.calls.some(([r]) => String(r).endsWith('/voices/preview'))).toBe(true),
    );
    const call = fetch.mock.calls.find(([r]) => String(r).endsWith('/voices/preview'))!;
    const init = (call as unknown as [string, RequestInit])[1];
    expect(JSON.parse(String(init.body))).toMatchObject({
      voice: 'af_heart',
      text: 'Ryoka',
      bookId: 1,
    });
  });

  it('shows API errors', async () => {
    stubApi({});
    renderAt('/library');
    expect(await screen.findByText('Could not load books')).toBeInTheDocument();
  });
  it('designs a voice for a character of a book of the cloning engine', async () => {
    const system = {
      version: '0.1.0',
      commit: 'abc123',
      maxUploadBytes: 536870912,
      database: { dialect: 'sqlite', schemaVersion: 'x' },
      tts: {
        url: '',
        reachable: false,
        configured: false,
        voiceDesign: true,
        voiceDesignEngine: 'chatterbox',
        defaultVoice: 'chatterbox:Emily.wav',
      },
    };
    const fetch = stubApi({
      '/api/system': system,
      '/api/books/1': { ...book, narratorVoice: 'chatterbox:Emily.wav', engine: 'chatterbox' },
      '/api/voices': voices,
      'GET /api/characters/7': { ...erin, voice: 'chatterbox:Abigail.wav', chapters: [] },
      'GET /api/characters/7/quotes': [],
      'POST /api/characters/7/voice-design': { ...erin.voiceDesign, status: 'queued' },
    });
    renderAt('/books/1/characters/7');
    const card = await screen.findByTestId('voice-design');
    const text = within(card).getByRole('textbox', { name: 'Voice description' });
    expect(text).toHaveValue(erin.voiceDesign.suggestedDescription);
    fireEvent.change(text, { target: { value: '  A deep, gravelly voice. ' } });
    fireEvent.click(within(card).getByRole('button', { name: 'Design voice' }));
    await vi.waitFor(() =>
      expect(fetch.mock.calls.some(([r]) => typeof r !== 'string' && r.method === 'POST')).toBe(
        true,
      ),
    );
    const post = fetch.mock.calls.find(([r]) => typeof r !== 'string' && r.method === 'POST')!;
    expect(await (post[0] as Request).json()).toEqual({ description: 'A deep, gravelly voice.' });
  });

  it('designs no voices for a book of another engine', async () => {
    stubApi({
      '/api/system': {
        version: '0.1.0',
        commit: 'abc123',
        maxUploadBytes: 536870912,
        database: { dialect: 'sqlite', schemaVersion: 'x' },
        tts: {
          url: 'http://kokoro:8880',
          reachable: true,
          configured: true,
          voiceDesign: true,
          voiceDesignEngine: 'chatterbox',
          defaultVoice: 'af_heart',
        },
      },
      '/api/books/1': { ...book, engine: 'kokoro' },
      '/api/voices': voices,
      'GET /api/characters/7': { ...erin, chapters: [] },
      'GET /api/characters/7/quotes': [],
    });
    renderAt('/books/1/characters/7');
    const card = await screen.findByTestId('voice-design');
    expect(
      within(card).getByText(
        'This book speaks with kokoro: switch it to chatterbox to design voices.',
      ),
    ).toBeInTheDocument();
    expect(within(card).getByRole('button', { name: 'Design voice' })).toBeDisabled();
  });
});
