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

  // The four counts a tag can be in, the same ones "tags printed" on the
  // tile grid already sums -- drawn as a ring instead of read as numbers.
  // Colors reuse the vocabulary the admin console's status pills already
  // established (active = good, cooldown = warn, retired = dim), so a
  // person who has seen either page reads this one without a legend.
  // "minted" gets a fixed blue rather than --accent: --accent and --good
  // are both teal-leaning greens (see :root's palette), close enough in
  // hue that two ring segments in those colors are genuinely hard to tell
  // apart at this size. Blue has no other meaning on this page to collide
  // with.
  const CHART_SEGMENTS = [
    { key: 'tags_active', label: 'in the water', color: 'var(--good)' },
    { key: 'tags_cooldown', label: 'cooling down', color: 'var(--warn)' },
    { key: 'tags_minted', label: 'printed, not yet armed', color: '#3b82f6' },
    { key: 'tags_retired', label: 'retired', color: 'var(--ink-dim)' },
  ];

  // renderStatsChart draws exactly what the tiles say and nothing else --
  // no trend, no history, because this deployment has no time series to
  // draw one from honestly (see nexus-repo's own price chart, which drops
  // the same feature for a token with no real closes rather than fake
  // one). A ring built from four real point-in-time counts is the chart
  // this data actually supports.
  function renderStatsChart(s) {
    const box = $('statsChart');
    if (!box) return;
    const total = CHART_SEGMENTS.reduce((sum, seg) => sum + (s[seg.key] || 0), 0);
    if (!total) {
      box.classList.add('hidden');
      box.innerHTML = '';
      return;
    }

    const size = 148, stroke = 20, r = (size - stroke) / 2, c = size / 2;
    const circumference = 2 * Math.PI * r;
    let drawn = 0;
    const arcs = CHART_SEGMENTS.filter((seg) => s[seg.key])
      .map((seg) => {
        const len = (s[seg.key] / total) * circumference;
        // Circles start at 3 o'clock; -90deg turns that into 12 o'clock, the
        // usual top-of-the-clock start for a ring chart.
        const rotation = (drawn / circumference) * 360 - 90;
        drawn += len;
        return (
          `<circle cx="${c}" cy="${c}" r="${r}" fill="none" stroke="${seg.color}" stroke-width="${stroke}" ` +
          `stroke-dasharray="${len} ${circumference - len}" transform="rotate(${rotation} ${c} ${c})"></circle>`
        );
      })
      .join('');

    const legend = CHART_SEGMENTS.filter((seg) => s[seg.key])
      .map(
        (seg) =>
          `<div class="chart-legend-row"><span class="chart-dot" style="background:${seg.color}"></span>` +
          `<span>${num(s[seg.key])} ${seg.label}</span></div>`
      )
      .join('');

    box.innerHTML =
      `<div class="chart-ring-wrap">` +
      `<svg viewBox="0 0 ${size} ${size}" width="${size}" height="${size}" role="img" aria-label="Tags by status">${arcs}</svg>` +
      `<div class="chart-total"><div class="chart-total-n tabular">${num(total)}</div><div class="chart-total-k">printed</div></div>` +
      `</div>` +
      `<div class="chart-legend">${legend}</div>`;
    box.classList.remove('hidden');
    requestAnimationFrame(() => box.classList.add('in'));
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
      renderStatsChart(data.stats);
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
      renderStatsChart(mockStats);
      $('recent').innerHTML = eventRows(mockRecent);
    },
    'Empty program (day one)': () => {
      stopLivePolling();
      $('stats').innerHTML = statTiles({});
      renderStatsChart({});
      $('recent').innerHTML = eventRows([]);
    },
  };

  document.addEventListener('DOMContentLoaded', boot);
})();
