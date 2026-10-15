import { writeFileSync } from 'node:fs';
import { resolve } from 'node:path';

import react from '@vitejs/plugin-react';
import type { Plugin } from 'vite';
import { defineConfig } from 'vitest/config';

// The Go binary embeds this directory (internal/webui).
const outDir = resolve(import.meta.dirname, '../internal/webui/dist');

// The embed directory is committed with only a .gitkeep (so Go builds work
// without a frontend build). Vite empties it on every build; put it back.
function keepGitkeep(): Plugin {
  return {
    name: 'keep-gitkeep',
    closeBundle() {
      writeFileSync(resolve(outDir, '.gitkeep'), '');
    },
  };
}

export default defineConfig({
  plugins: [react(), keepGitkeep()],
  build: {
    outDir,
    emptyOutDir: true,
    // A single bundle (~150 kB gzipped, mostly Mantine) is fine for a LAN app.
    chunkSizeWarningLimit: 1024,
  },
  server: {
    // `go run ./cmd/audiobook-generator` listens on :8080.
    proxy: { '/api': 'http://localhost:8080' },
  },
  test: {
    environment: 'jsdom',
    setupFiles: ['./src/test/setup.ts'],
    css: false,
  },
});
