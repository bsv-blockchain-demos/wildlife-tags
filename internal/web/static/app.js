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

  // A large, faint watermark per tile (see .stat-icon in style.css) -- pure
  // decoration, so the shapes only need to be recognisable at 10% opacity,
  // not detailed. One svg wrapper, one inner path per concept.
  const statIcon = (inner) =>
    `<span class="stat-icon" aria-hidden="true"><svg viewBox="0 0 20 20" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round">${inner}</svg></span>`;
  const STAT_ICONS = {
    tag: '<path d="M11 3H4.5A1.5 1.5 0 0 0 3 4.5V11l7.3 7.3a1.5 1.5 0 0 0 2.1 0l5.9-5.9a1.5 1.5 0 0 0 0-2.1L11 3Z"/><circle cx="7" cy="7" r="1.1"/>',
    flag: '<path d="M5 3v14"/><path d="M5 4h9l-2.5 3 2.5 3H5"/>',
    coin: '<circle cx="10" cy="10" r="6.5"/><path d="M10 7v6M12 8.3c-.4-.6-1.1-1-2-1-1.2 0-2 .6-2 1.5s.8 1.3 2 1.5c1.2.2 2 .6 2 1.5s-.8 1.5-2 1.5c-.9 0-1.6-.4-2-1"/>',
    lock: '<rect x="5" y="9" width="10" height="8" rx="1.5"/><path d="M7 9V6.5a3 3 0 0 1 6 0V9"/>',
    hourglass: '<path d="M6 3h8M6 17h8M6.5 3c.3 2.8 2 4.3 3.5 5 1.5-.7 3.2-2.2 3.5-5M6.5 17c.3-2.8 2-4.3 3.5-5 1.5.7 3.2 2.2 3.5 5"/>',
    stack: '<rect x="4" y="4" width="12" height="3" rx="1"/><rect x="4" y="8.5" width="12" height="3" rx="1"/><rect x="4" y="13" width="12" height="3" rx="1"/>',
  };

  function statTiles(s) {
    return [
      ['tags in the water', num(s.tags_active), 'tag'],
      ['reports', num(s.recaptures), 'flag'],
      ['paid out', `${num(s.satoshis_paid)} sats`, 'coin'],
      ['locked in tags', `${num(s.satoshis_locked)} sats`, 'lock'],
      // Bonuses owed but not yet payable: money promised to crabbers who put a
      // crab back, waiting on that crab turning up again.
      ['bonuses pending', `${num(s.escrow_owed)} sats`, 'hourglass'],
      ['tags printed', num(s.tags_minted + s.tags_active + s.tags_cooldown + s.tags_retired), 'stack'],
    ]
      .map(
        ([k, v, icon]) =>
          `<div class="stat">${statIcon(STAT_ICONS[icon])}<div class="n tabular">${v}</div><div class="k">${k}</div></div>`
      )
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

  // ---- species grid ---------------------------------------------------
  //
  // Built from GET /api/schema (see schema.js) rather than one hand-copied
  // card per species: the marine game fish tagging programme alone lists
  // dozens (dnr.sc.gov/marine/tagfish/tagspecies.html), and a hardcoded
  // card per species is exactly the "found in four places" problem the
  // schema was built to end. Icon/gradient mapping mirrors admin.js's
  // species-picker grid and redeem.js's hero, kept in step by hand since
  // there's no module system to share it through.
  const esc = (s) =>
    String(s).replace(/[&<>"']/g, (c) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c]));

  const SPECIES_ICON = {
    CALSAP: 'blue-crab.svg', SCIOCE: 'red-drum.svg', CARCAR: 'sea-turtle.svg',
    CARPLU: 'shark.svg', CARLIM: 'shark.svg',
  };
  const SPECIES_GRADIENT = {
    CALSAP: 'estuary', SCIOCE: 'sunset', CARCAR: 'marsh',
    CARPLU: 'tide', CARLIM: 'coral',
  };
  const FALLBACK_ICON = 'fish-generic.svg';
  // Every species this map doesn't name (nearly all of them, at programme
  // scale) cycles through the rest of style.css's --grad-* tokens rather
  // than all rendering the same fallback grey -- variety for a long grid,
  // not a claim that any color "means" a particular species.
  const GRADIENT_CYCLE = ['tide', 'sunset', 'marsh', 'dusk', 'coral', 'amber', 'estuary', 'slate'];

  function speciesCard(p, index) {
    const icon = SPECIES_ICON[p.code] || FALLBACK_ICON;
    const grad = SPECIES_GRADIENT[p.code] || GRADIENT_CYCLE[index % GRADIENT_CYCLE.length];
    const workflow = p.workflow === 'harvest' ? 'Harvest' : 'Mark-recapture';
    return (
      `<a class="grad-card" href="/about">` +
      `<div class="grad-card-header size-sm" style="background: var(--grad-${grad})">` +
      `<div class="card-icon-badge"><img src="/vendor/animals/${icon}" alt="" loading="lazy"></div>` +
      `</div>` +
      `<div class="grad-card-body">` +
      `<span class="grad-card-eyebrow">${esc(workflow)}</span>` +
      `<div class="grad-card-title">${esc(p.common)}</div>` +
      `<div class="grad-card-sub">${esc(p.scientific)}</div>` +
      `</div></a>`
    );
  }

  async function renderSpeciesGrid() {
    const grid = $('speciesGrid');
    if (!grid) return;
    try {
      await window.Schema.load();
      // By common name, not by the code species.All() sorts by on the Go
      // side (see registry.go) -- "Red drum" reads next to "Sandbar shark"
      // by name, not scattered by an internal code nobody visiting this
      // page has any reason to know.
      const profiles = window.Schema.profiles().slice().sort((a, b) => a.common.localeCompare(b.common));
      grid.innerHTML = profiles.map(speciesCard).join('');
    } catch (e) {
      grid.innerHTML = `<p class="note">Could not load the species list: ${esc(e.message)}</p>`;
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
    renderSpeciesGrid(); // independent of the stats poll; a slow schema fetch shouldn't delay it
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
