import '@mantine/core/styles.css';
import '@mantine/notifications/styles.css';
import './reader.css';

import { StrictMode } from 'react';
import { createRoot } from 'react-dom/client';

import { App } from './App';
import { registerServiceWorker } from './lib/install';

const root = document.getElementById('root');
if (!root) {
  throw new Error('missing #root element');
}

if (import.meta.env.PROD) registerServiceWorker();

createRoot(root).render(
  <StrictMode>
    <App />
  </StrictMode>,
);
