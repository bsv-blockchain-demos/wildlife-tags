// Registers the app-shell service worker (see sw.js), so a page that has
// already been opened once can still open with no signal at all -- the
// offline queue and the redeem page's own resume logic both already assume
// the page itself managed to load; this is what makes that assumption
// survive a cold start with zero bars.
//
// Skipped on localhost and under ?dev=1, the same two "this is a developer,
// not a finder" signals devpanel.js uses. A service worker caching an
// in-progress edit is one of the most common ways local development gets
// confusing, and neither of those checks should ever be true on a real
// deployment.
(function () {
  'use strict';
  if (!('serviceWorker' in navigator)) return;

  const isDev =
    location.hostname === 'localhost' ||
    location.hostname === '127.0.0.1' ||
    new URLSearchParams(location.search).get('dev') === '1';
  if (isDev) return;

  window.addEventListener('load', () => {
    navigator.serviceWorker.register('/sw.js').catch(() => {
      // Best-effort: a page that fails to register a worker still works
      // exactly as it did before this file existed.
    });
  });
})();
