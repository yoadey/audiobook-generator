import { QueryClient } from '@tanstack/react-query';
import { createMemoryHistory } from '@tanstack/react-router';
import { fireEvent, render, screen, within } from '@testing-library/react';
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
  narratorVoice: 'af_heart',
  narratorSpeed: 1,
  lookahead: 3,
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
    renderAt('/books/1');
    expect(await screen.findByRole('heading', { name: 'Pride and Prejudice' })).toBeInTheDocument();
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
    expect(await screen.findByRole('heading', { name: 'Chapter 2' })).toBeInTheDocument();
    expect(screen.getByText('Chapter 2 of 2')).toBeInTheDocument();
    expect(screen.getByText('This chapter has no audio yet.')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Render now' })).toBeInTheDocument();
    // Entering the chapter stores the position (which queues the lookahead).
    await vi.waitFor(() =>
      expect(fetch.mock.calls.some(([r]) => typeof r !== 'string' && r.method === 'PUT')).toBe(
        true,
      ),
    );
  });

  it('plays ready audio', async () => {
    stubApi({
      '/api/books/1': book,
      '/api/books/1/chapters': { items: chapters, total: 2 },
      'GET /api/books/1/progress': { positionMs: 0 },
      'PUT /api/books/1/progress': { chapterId: 10, positionMs: 0 },
    });
    const { container } = renderAt('/books/1/listen');
    expect(await screen.findByRole('heading', { name: 'Chapter 1' })).toBeInTheDocument();
    const audio = container.querySelector('audio');
    expect(audio).toHaveAttribute('src', expect.stringContaining('/api/chapters/10/audio'));
    expect(screen.getByRole('button', { name: 'Play' })).toBeEnabled();
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
    renderAt('/books/1');
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
    renderAt('/books/1');
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
    renderAt('/books/1');
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

  it('shows the podcast feed URL', async () => {
    stubApi({
      '/api/books/1': { ...book, feedPath: '/feed/tok/1.xml' },
      '/api/books/1/volumes': [],
      '/api/books/1/chapters': { items: [], total: 0 },
    });
    renderAt('/books/1');
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
});
