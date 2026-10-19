// The installed app's service worker: it keeps the app's page for when the
// server cannot be reached (the app then says so) and leaves everything
// else to the network and the browser's cache. The API, audio, feeds and
// the sign-in are never cached: they are personal and change.
const SHELL = 'abg-shell-v1';

self.addEventListener('install', (event) => {
  event.waitUntil(
    caches
      .open(SHELL)
      .then((c) => c.add('/'))
      .catch(() => {}),
  );
  self.skipWaiting();
});

self.addEventListener('activate', (event) => {
  event.waitUntil(
    caches
      .keys()
      .then((keys) => Promise.all(keys.filter((k) => k !== SHELL).map((k) => caches.delete(k))))
      .then(() => self.clients.claim()),
  );
});

self.addEventListener('fetch', (event) => {
  const req = event.request;
  if (req.method !== 'GET' || req.mode !== 'navigate') return;
  event.respondWith(
    fetch(req)
      .then((res) => {
        // The page itself (not a sign-in redirect) is kept for offline use.
        if (res.ok && res.type === 'basic' && !res.redirected) {
          const copy = res.clone();
          void caches.open(SHELL).then((c) => c.put('/', copy));
        }
        return res;
      })
      .catch(() => caches.match('/').then((res) => res ?? Response.error())),
  );
});
