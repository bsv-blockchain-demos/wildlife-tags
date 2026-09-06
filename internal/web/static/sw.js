// The app shell's offline fallback.
//
// Everything built earlier for field resilience -- the admin arm queue, the
// redeem page's resumable receipt -- assumes the page itself already loaded.
// Neither helps a biologist or a finder who opens this app cold with zero
// bars: without this file, that request just fails, and the browser shows
// its own "you are offline" page instead of ours. This is what makes a
// second visit to a page already opened once survive that.
//
// Deliberately not a precaching, asset-manifest service worker -- there is
// no build step here to generate one from, and a hand-maintained list is a
// second copy of every page's own <script>/<link> tags that will drift the
// first time someone edits one and forgets the other (see vendor/README.md
// for the same argument made about a different list). Instead: network
// first, and whatever a real visit actually fetched gets cached as it goes.
// That is also the more honest fallback -- it can only ever serve a page
// this browser has genuinely already seen, never a guess at what today's
// deployment looks like.
//
// CACHE_VERSION is bumped by hand whenever a change is worth forcing every
// cached copy to be dropped (a broken cached response, a page restructured
// enough that partial staleness would look wrong). Nothing here reads it
// from anywhere else, because nothing generates it -- that is the same
// manual-vigilance trade this codebase makes elsewhere in exchange for not
// needing a build step at all.
const CACHE_VERSION = 'v1';
const CACHE_NAME = `wildtag-shell-${CACHE_VERSION}`;

self.addEventListener('install', () => {
  // Take over immediately rather than waiting for every open tab of the
  // previous worker to close -- this app is small enough, and visited
  // rarely enough per person, that the usual caution about a mid-session
  // swap is not worth a first-time visitor waiting an extra reload for
  // offline support to switch on.
  self.skipWaiting();
});

self.addEventListener('activate', (event) => {
  event.waitUntil(
    caches
      .keys()
      .then((names) =>
        Promise.all(
          names.filter((n) => n.startsWith('wildtag-shell-') && n !== CACHE_NAME).map((n) => caches.delete(n))
        )
      )
      .then(() => self.clients.claim())
  );
});

self.addEventListener('fetch', (event) => {
  const req = event.request;
  if (req.method !== 'GET') return; // nothing here ever mutates state; leave POSTs alone entirely

  const url = new URL(req.url);
  if (url.origin !== self.location.origin) return; // this is an app shell, not a general proxy

  // /api/* stays completely untouched. Every offline-aware flow this app
  // has (the arm queue's isNetworkError, the redeem page's pendingReceipt
  // resume) depends on fetch() rejecting with a real network error when
  // there is no signal; a cached API response, even a well-intentioned
  // one, would make a stale funding balance or a stale tag status look
  // like a live answer, which is a worse failure than no answer at all.
  if (url.pathname.startsWith('/api/')) return;

  event.respondWith(
    fetch(req)
      .then((res) => {
        // Some responses say not to keep a copy -- the print sheet most
        // pointedly, since every code on it can redeem a real tag (see its
        // own Cache-Control in handlePrintSheet) -- and this respects that
        // rather than re-deciding it here. Anything else that succeeds is
        // cached for the next time this exact request has no signal to
        // answer it.
        if (res && res.ok && !(res.headers.get('Cache-Control') || '').includes('no-store')) {
          const copy = res.clone();
          caches.open(CACHE_NAME).then((cache) => cache.put(req, copy));
        }
        return res;
      })
      .catch(() => caches.match(req))
  );
});
