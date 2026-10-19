import { useEffect, useState } from 'react';

/** Chrome's install prompt event (not in the DOM typings). */
type InstallPrompt = Event & { prompt: () => Promise<void> };

/** Registers the service worker that makes the app installable. */
export function registerServiceWorker() {
  if (!('serviceWorker' in navigator)) return;
  window.addEventListener('load', () => {
    navigator.serviceWorker.register('/sw.js').catch(() => {
      // Without it the app works as a page; only installing is lost.
    });
  });
}

/**
 * Installing the app, where the browser offers it (Chrome, Edge, Android):
 * a function to ask, or undefined. Safari installs through its share menu.
 */
export function useInstall(): (() => void) | undefined {
  const [prompt, setPrompt] = useState<InstallPrompt>();
  useEffect(() => {
    const offered = (e: Event) => {
      e.preventDefault(); // shown from our own button instead
      setPrompt(e as InstallPrompt);
    };
    const installed = () => setPrompt(undefined);
    window.addEventListener('beforeinstallprompt', offered);
    window.addEventListener('appinstalled', installed);
    return () => {
      window.removeEventListener('beforeinstallprompt', offered);
      window.removeEventListener('appinstalled', installed);
    };
  }, []);
  if (!prompt) return undefined;
  return () => {
    void prompt.prompt();
    setPrompt(undefined); // a prompt can be shown once
  };
}
