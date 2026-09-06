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
    // Two overlapping coins reads as "money" unambiguously even at 10%
    // opacity; the previous version tried to draw a $ out of one curved
    // path and mostly read as a squiggle at this size.
    coin: '<circle cx="7.5" cy="12.5" r="5"/><circle cx="12.5" cy="7.5" r="5"/>',
    lock: '<rect x="5" y="9" width="10" height="8" rx="1.5"/><path d="M7 9V6.5a3 3 0 0 1 6 0V9"/>',
    // Straight lines pinching to a point at center, the standard hourglass
    // silhouette -- the previous version used curves trying for the same
    // shape and read closer to an eye than a timer.
    hourglass: '<path d="M6 3h8M6 17h8M6 3l4 7-4 7M14 3l-4 7 4 7"/>',
    // Three cards staggered on the diagonal, not stacked flush -- flush
    // equal-width bars is this app's own hamburger-menu shape (see
    // .hamburger-line in index.html), and a "things collected" glyph
    // shouldn't double as a navigation one.
    stack: '<rect x="3.5" y="4" width="10" height="6.5" rx="1.2"/><rect x="6" y="7" width="10" height="6.5" rx="1.2"/><rect x="8.5" y="10" width="10" height="6.5" rx="1.2"/>',
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
    const search = `${p.common} ${p.scientific} ${p.code}`.toLowerCase();
    // A <button>, not a link to /about -- see initSpeciesSheet. data-grad
    // rides along so the sheet reuses the exact color the card already
    // resolved rather than re-deriving it (and risking GRADIENT_CYCLE
    // landing on a different one if this were ever called with a
    // different index).
    return (
      `<button type="button" class="grad-card" data-search="${esc(search)}" data-code="${esc(p.code)}" data-grad="${esc(grad)}">` +
      `<div class="grad-card-header size-sm" style="background: var(--grad-${grad})">` +
      `<div class="card-icon-badge"><img src="/vendor/animals/${icon}" alt="" loading="lazy"></div>` +
      `</div>` +
      `<div class="grad-card-body">` +
      `<span class="grad-card-eyebrow">${esc(workflow)}</span>` +
      `<div class="grad-card-title">${esc(p.common)}</div>` +
      `<div class="grad-card-sub">${esc(p.scientific)}</div>` +
      `</div></button>`
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
      applySpeciesFilter(); // re-assert any chips pinned before this (re)render
    } catch (e) {
      grid.innerHTML = `<p class="note">Could not load the species list: ${esc(e.message)}</p>`;
    }
  }

  // ---- species search: type to narrow, Enter to pin as a removable chip -
  //
  // Chips are an OR, not an AND: pinning "shark" and then "tuna" widens the
  // grid to both, since the point on a browse-only page like this one is
  // building up a shortlist of families to compare, not narrowing to one
  // exact match the way the admin arm panel's search does.
  let chipTerms = [];

  function applySpeciesFilter() {
    const grid = $('speciesGrid');
    const search = $('speciesSearch');
    if (!grid || !search) return;
    const live = search.value.trim().toLowerCase();
    const terms = live ? chipTerms.concat(live) : chipTerms;
    let anyVisible = false;
    grid.querySelectorAll('.grad-card').forEach((card) => {
      // Skeleton placeholders (see index.html) have no data-search yet --
      // still present in the grid for an instant before renderSpeciesGrid
      // replaces them, and a fast Enter could reach here before that.
      const haystack = card.dataset.search || '';
      const match = !terms.length || terms.some((t) => haystack.includes(t));
      card.classList.toggle('hidden', !match);
      if (match) anyVisible = true;
    });
    const empty = $('speciesEmpty');
    if (empty) empty.classList.toggle('hidden', anyVisible || !terms.length);
  }

  function renderChips() {
    const row = $('speciesChips');
    if (!row) return;
    row.classList.toggle('hidden', !chipTerms.length);
    const profiles = window.Schema.profiles ? window.Schema.profiles() : [];
    row.innerHTML = chipTerms
      .map((term) => {
        // The chip's icon is whichever matching species sorts first, same
        // as the grid it's filtering -- a broad term like "shark" still
        // lands on a real animal, not a placeholder, and every shark in
        // this programme already shares one icon (see SPECIES_ICON) so it
        // rarely even matters which one wins.
        const hit = profiles.find((p) => `${p.common} ${p.scientific} ${p.code}`.toLowerCase().includes(term));
        const icon = hit ? SPECIES_ICON[hit.code] || FALLBACK_ICON : FALLBACK_ICON;
        return (
          `<span class="chip">` +
          `<span class="chip-icon"><img src="/vendor/animals/${icon}" alt="" loading="lazy"></span>` +
          `<span>${esc(term)}</span>` +
          `<button type="button" class="chip-remove" data-term="${esc(term)}" aria-label="Remove filter: ${esc(term)}">&times;</button>` +
          `</span>`
        );
      })
      .join('');
    row.querySelectorAll('.chip-remove').forEach((btn) => {
      btn.addEventListener('click', () => {
        chipTerms = chipTerms.filter((t) => t !== btn.dataset.term);
        renderChips();
        applySpeciesFilter();
      });
    });
  }

  function initSpeciesSearch() {
    const search = $('speciesSearch');
    if (!search) return;
    search.addEventListener('input', applySpeciesFilter);
    search.addEventListener('keydown', (e) => {
      if (e.key !== 'Enter') return;
      e.preventDefault();
      const term = search.value.trim().toLowerCase();
      if (!term || chipTerms.includes(term)) return;
      chipTerms.push(term);
      search.value = '';
      renderChips();
      applySpeciesFilter();
    });
  }

  // ---- species detail sheet: tap a card, see what it actually covers -----
  //
  // Reuses the arm flow's own confirm-sheet chrome (.modal-scrim,
  // .confirm-sheet, FocusTrap) rather than inventing a second modal
  // pattern -- this is the same "review, then dismiss" shape, just with
  // nothing to confirm, so it also gets a backdrop-click-to-close and no
  // Enter-to-confirm shortcut (there is no default action to fire).
  let releaseSpeciesSheetTrap = null;

  function speciesSheetHTML(p, grad) {
    const icon = SPECIES_ICON[p.code] || FALLBACK_ICON;
    const workflow = p.workflow === 'harvest' ? 'Harvest' : 'Mark-recapture';
    const measures = (p.measures || [])
      .map((m) => {
        // Same unscaling as schema.js's own (unexported) bounds(): a
        // measure stored with scale 100 means the field reads 15.0-40.0
        // but the record carries 1500-4000.
        const lo = m.scale > 1 ? m.min / m.scale : m.min;
        const hi = m.scale > 1 ? m.max / m.scale : m.max;
        const unit = m.unit ? ` ${esc(m.unit)}` : '';
        return (
          `<div class="confirm-row"><span class="confirm-k">${esc(m.label)}</span>` +
          `<span class="confirm-v">${lo.toLocaleString()}&ndash;${hi.toLocaleString()}${unit}</span></div>`
        );
      })
      .join('');
    // must_release's reason strings are already written as standalone
    // sentences (see the species profiles), so they read fine as bullets
    // with no threshold number restated alongside them.
    const limits = (p.must_release || []).map((r) => `<li>${esc(r.reason)}</li>`).join('');
    const facts = (p.fun_facts || []).map((f) => `<li>${esc(f)}</li>`).join('');
    return (
      `<div class="crab-hero" style="background: var(--grad-${grad})">` +
      `<div class="card-icon-badge"><img src="/vendor/animals/${icon}" alt="" loading="lazy"></div>` +
      `<div class="crab-name">${esc(p.common)}</div>` +
      `<div class="crab-sub">${esc(p.scientific)} &middot; ${esc(workflow)}</div>` +
      `</div>` +
      // Wrapped so .confirm-row:last-child (see style.css) drops the border
      // under the last measure specifically -- unwrapped, it would drop
      // the border off whichever element actually sits last in the whole
      // sheet instead, which is a heading or fact list, not a measure row.
      (measures ? `<h3 class="sub-head">What gets measured</h3><div>${measures}</div>` : '') +
      (limits ? `<h3 class="sub-head">Size limits</h3><ul class="note-list">${limits}</ul>` : '') +
      (facts ? `<h3 class="sub-head">Did you know</h3><ul class="note-list">${facts}</ul>` : '')
    );
  }

  function openSpeciesSheet(code, grad) {
    const p = window.Schema.profile(code);
    if (!p) return;
    $('speciesSheetBody').innerHTML = speciesSheetHTML(p, grad);
    $('speciesSheet').setAttribute('aria-label', `${p.common} details`);
    $('speciesScrim').hidden = false;
    $('speciesSheet').hidden = false;
    requestAnimationFrame(() => {
      $('speciesScrim').classList.add('open');
      $('speciesSheet').classList.add('open');
    });
    document.body.style.overflow = 'hidden';
    releaseSpeciesSheetTrap = window.FocusTrap.activate($('speciesSheet'));
  }

  function closeSpeciesSheet() {
    $('speciesScrim').classList.remove('open');
    $('speciesSheet').classList.remove('open');
    document.body.style.overflow = '';
    if (releaseSpeciesSheetTrap) {
      releaseSpeciesSheetTrap();
      releaseSpeciesSheetTrap = null;
    }
    window.setTimeout(() => {
      $('speciesScrim').hidden = true;
      $('speciesSheet').hidden = true;
    }, 250);
  }

  function initSpeciesSheet() {
    const grid = $('speciesGrid');
    if (!grid || !$('speciesSheet')) return;
    grid.addEventListener('click', (e) => {
      const card = e.target.closest('.grad-card');
      if (!card || !card.dataset.code) return;
      openSpeciesSheet(card.dataset.code, card.dataset.grad);
    });
    $('speciesSheetClose').addEventListener('click', closeSpeciesSheet);
    $('speciesSheetAbout').addEventListener('click', () => {
      location.href = '/about';
    });
    $('speciesScrim').addEventListener('click', closeSpeciesSheet);
    document.addEventListener('keydown', (e) => {
      if (e.key === 'Escape' && !$('speciesSheet').hidden) closeSpeciesSheet();
    });
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
    initSpeciesSearch();
    initSpeciesSheet();
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
