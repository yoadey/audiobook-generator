// The installed app's service worker: it keeps the app's page for when the
// server cannot be reached (the app then says so) and leaves everything
// else to the network and the browser's cache. Only the app's own page is
// kept: never the API, audio, downloads, feeds or the sign-in, which are
// personal and change.
const SHELL = 'abg-shell-v2';

// Paths that are not the app's page (opened directly, they stay theirs).
const NOT_APP = /^\/(api|feed|auth|metrics|healthz|readyz|assets|icons)(\/|$)|\.[a-z0-9]+$/i;

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

const isPage = (res) =>
  res.ok && res.type === 'basic' && (res.headers.get('content-type') ?? '').startsWith('text/html');

self.addEventListener('fetch', (event) => {
  const req = event.request;
  if (req.method !== 'GET' || req.mode !== 'navigate') return;
  if (NOT_APP.test(new URL(req.url).pathname)) return;
  const offline = () => caches.match('/').then((res) => res ?? Response.error());
  event.respondWith(
    fetch(req)
      .then((res) => {
        if (isPage(res)) {
          const copy = res.clone();
          void caches.open(SHELL).then((c) => c.put('/', copy));
          return res;
        }
        // The server is not there (the ingress answers for it).
        if ([502, 503, 504].includes(res.status)) return offline().then((o) => (o.ok ? o : res));
        return res;
      })
      .catch(offline),
  );
});
