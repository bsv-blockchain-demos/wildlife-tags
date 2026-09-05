// The public dashboard. Polls two endpoints and paints them; nothing here
// touches a wallet or a key.
(function () {
  'use strict';

  const $ = (id) => document.getElementById(id);
  const num = (n) => Number(n || 0).toLocaleString();

  async function get(path) {
    const res = await fetch(path);
    if (!res.ok) throw new Error(`${path} failed (${res.status})`);
    return res.json();
  }

  // The same tag mark as the wordmark and favicon, drawn as an outline
  // rather than filled -- a table with nothing in it yet, not an error.
  const EMPTY_ICON =
    '<svg viewBox="0 0 20 20" fill="none" stroke-width="1.3" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">' +
    '<path d="M11 3H4.5A1.5 1.5 0 0 0 3 4.5V11l7.3 7.3a1.5 1.5 0 0 0 2.1 0l5.9-5.9a1.5 1.5 0 0 0 0-2.1L11 3Z"/><circle cx="7" cy="7" r="1.1"/></svg>';
  const emptyRow = (colspan, text) =>
    `<tr><td colspan="${colspan}"><div class="empty-state"><span class="icon-badge">${EMPTY_ICON}</span><span>${text}</span></div></td></tr>`;

  function statTiles(s) {
    return [
      ['tags in the water', num(s.tags_active)],
      ['reports', num(s.recaptures)],
      ['paid out', `${num(s.satoshis_paid)} sats`],
      ['locked in tags', `${num(s.satoshis_locked)} sats`],
      // Bonuses owed but not yet payable: money promised to crabbers who put a
      // crab back, waiting on that crab turning up again.
      ['bonuses pending', `${num(s.escrow_owed)} sats`],
      ['tags printed', num(s.tags_minted + s.tags_active + s.tags_cooldown + s.tags_retired)],
    ]
      .map(([k, v]) => `<div class="stat"><div class="n tabular">${v}</div><div class="k">${k}</div></div>`)
      .join('');
  }

  function eventRows(events) {
    if (!events || !events.length) {
      return emptyRow(6, 'Nothing has happened yet.');
    }
    return events
      .map((e) => {
        const kind = e.kind === 'ACT' ? 'tagged' : 'reported';
        const status = e.status === 'mined' ? '<span class="pill proven">proven</span>'
          : e.status === 'failed' ? '<span class="pill">failed</span>'
          : '<span class="pill">pending proof</span>';
        return (
          `<tr>` +
          `<td class="mono">${e.display}</td>` +
          `<td>${kind}</td>` +
          `<td class="tabular">${e.generation}</td>` +
          `<td class="tabular">${e.satoshis ? num(e.satoshis) + ' sats' : '–'}</td>` +
          `<td>${status}</td>` +
          `<td class="note">${new Date(e.at).toLocaleString()}</td>` +
          `</tr>`
        );
      })
      .join('');
  }

  let pollTimer = null;

  async function poll() {
    try {
      const data = await get('/api/stats');
      $('stats').innerHTML = statTiles(data.stats);
      $('recent').innerHTML = eventRows(data.recent);
    } catch (e) {
      $('stats').innerHTML = `<div class="stat"><div class="k">${e.message}</div></div>`;
    }
  }

  async function boot() {
    if (!$('stats')) return; // this script only has work to do on the dashboard
    try {
      const info = await get('/api/info');
      $('net').textContent = info.network;
    } catch (_) {
      // The dashboard is still worth showing without it.
    }
    poll();
    pollTimer = setInterval(poll, 5000);
  }

  // ---- dev-mode mocks, see devpanel.js -----------------------------------

  const mockStats = {
    tags_active: 214,
    recaptures: 96,
    satoshis_paid: 2_140_000,
    satoshis_locked: 4_280_000,
    escrow_owed: 612_000,
    tags_minted: 40,
    tags_cooldown: 12,
    tags_retired: 58,
  };

  const mockRecent = [
    { display: 'K2M-9Q7', kind: 'ACT', generation: 1, satoshis: 0, status: 'mined', at: new Date(Date.now() - 1000 * 60 * 4).toISOString() },
    { display: 'B7X-4RT', kind: 'REC', generation: 2, satoshis: 20000, status: 'mined', at: new Date(Date.now() - 1000 * 60 * 40).toISOString() },
    { display: 'F1P-2LC', kind: 'REC', generation: 1, satoshis: 5000, status: 'pending', at: new Date(Date.now() - 1000 * 60 * 90).toISOString() },
    { display: 'H9D-7WX', kind: 'ACT', generation: 1, satoshis: 0, status: 'mined', at: new Date(Date.now() - 1000 * 60 * 60 * 5).toISOString() },
    { display: 'Q4Z-1NB', kind: 'REC', generation: 3, satoshis: 5000, status: 'failed', at: new Date(Date.now() - 1000 * 60 * 60 * 22).toISOString() },
  ];

  // The live 5s poll would otherwise overwrite mock content almost as soon as
  // it's shown; a mock always wins until the page is reloaded.
  function stopLivePolling() {
    if (pollTimer) clearInterval(pollTimer);
  }

  window.DevMocks = window.DevMocks || {};
  window.DevMocks.index = {
    'Busy program': () => {
      stopLivePolling();
      $('stats').innerHTML = statTiles(mockStats);
      $('recent').innerHTML = eventRows(mockRecent);
    },
    'Empty program (day one)': () => {
      stopLivePolling();
      $('stats').innerHTML = statTiles({});
      $('recent').innerHTML = eventRows([]);
    },
  };

  document.addEventListener('DOMContentLoaded', boot);
})();
