// The admin console: sign in, mint batches, arm tags, put cooled-down tags back
// in service.
(function () {
  'use strict';

  const $ = (id) => document.getElementById(id);
  const num = (n) => Number(n || 0).toLocaleString();
  const escHTML = (s) =>
    String(s).replace(/[&<>"']/g, (c) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c]));

  // Truncated in the middle, not at the end: an identity key's *end* is
  // what a person actually checks it against (the last few characters they
  // remember from their own wallet), so cutting from the end throws away
  // the part that matters most.
  const midTruncate = (k) => (k && k.length > 16 ? `${k.slice(0, 8)}…${k.slice(-6)}` : k || '');

  // The same tag mark as the wordmark and favicon, drawn as an outline
  // rather than filled -- a table with nothing in it yet, not an error.
  const EMPTY_ICON =
    '<svg viewBox="0 0 20 20" fill="none" stroke-width="1.3" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">' +
    '<path d="M11 3H4.5A1.5 1.5 0 0 0 3 4.5V11l7.3 7.3a1.5 1.5 0 0 0 2.1 0l5.9-5.9a1.5 1.5 0 0 0 0-2.1L11 3Z"/><circle cx="7" cy="7" r="1.1"/></svg>';
  const emptyRow = (colspan, text) =>
    `<tr><td colspan="${colspan}"><div class="empty-state"><span class="icon-badge">${EMPTY_ICON}</span><span>${text}</span></div></td></tr>`;

  // The same rotate glyph as the "Waiting to re-arm" tab: putting a tag
  // back in service is that tab's one action, so its button wears the
  // tab's own icon rather than a new one.
  const REARM_ICON =
    '<svg viewBox="0 0 20 20" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">' +
    '<path d="M16.5 10a6.5 6.5 0 1 1-2-4.7"/><path d="M16.5 3v4h-4"/></svg>';

  // A large, faint watermark per funding tile -- see .stat-icon in
  // style.css and app.js's identical statIcon, which this mirrors for the
  // three admin-only figures (app.js's own six cover everything public).
  const statIcon = (inner) =>
    `<span class="stat-icon" aria-hidden="true"><svg viewBox="0 0 20 20" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round">${inner}</svg></span>`;
  const FUNDING_ICONS = {
    balance: '<rect x="2.5" y="5.5" width="15" height="10" rx="2"/><path d="M2.5 8.5h15"/><circle cx="14" cy="12" r="1" fill="currentColor" stroke="none"/>',
    perTag: '<path d="M11 3H4.5A1.5 1.5 0 0 0 3 4.5V11l7.3 7.3a1.5 1.5 0 0 0 2.1 0l5.9-5.9a1.5 1.5 0 0 0 0-2.1L11 3Z"/><circle cx="7" cy="7" r="1.1"/>',
    activations: '<rect x="4" y="4" width="12" height="12" rx="2"/><path d="M7 10l2 2 4-4"/>',
  };

  const ATTEST_PROTOCOL = [2, 'wildtag observation'];
  const QUEUE_KEY = 'wildtag.armqueue.v1';
  const IDENTITY_CACHE_KEY = 'wildtag.lastIdentity';

  const state = {
    info: null,
    fix: null,
    fixAt: 0,
    session: null,
    profile: null,
    watchId: null,
    armedCount: 0,
    // rearmSelected is a Set of tag ids checked in the "waiting to re-arm"
    // table -- see the bulk-select bar built around it below.
    rearmSelected: new Set(),
  };

  async function api(path, body) {
    const res = await fetch(path, {
      method: body ? 'POST' : 'GET',
      headers: body ? { 'Content-Type': 'application/json' } : undefined,
      body: body ? JSON.stringify(body) : undefined,
    });
    const data = await res.json().catch(() => ({}));
    if (!res.ok) throw new Error(data.error || `request failed (${res.status})`);
    return data;
  }

  // isNetworkError distinguishes "the network itself is the problem" from
  // "the request reached the server and the server said no". fetch() rejects
  // with a TypeError when it cannot reach anything at all (offline, DNS
  // failure, connection refused); api() above throws a plain Error, built
  // from the server's own response, once a request has actually completed.
  // The distinction decides whether a failed arm goes back in the queue to
  // retry itself, or sits there asking a person to look at it.
  const isNetworkError = (e) => e instanceof TypeError;

  const bytesToHex = (b) => Array.from(b, (x) => x.toString(16).padStart(2, '0')).join('');
  const hexToBytes = (h) => {
    const out = new Uint8Array(h.length / 2);
    for (let i = 0; i < out.length; i++) out[i] = parseInt(h.substr(i * 2, 2), 16);
    return out;
  };

  // buzz gives an arm's outcome a channel that does not depend on somebody
  // looking at the screen at the right moment -- a boat in bright sun, wet
  // hands mid-catch. Silently a no-op wherever the Vibration API does not
  // exist (iOS Safari, most desktops), which is every reason to guard it and
  // no reason to feature-detect around it.
  const BUZZ_OK = [40];
  const BUZZ_ERR = [40, 80, 40];
  function buzz(pattern) {
    try {
      if (navigator.vibrate) navigator.vibrate(pattern);
    } catch (_) {
      /* not available; the visual feedback still stands on its own */
    }
  }

  // ---- session ------------------------------------------------------------

  async function boot() {
    state.info = await api('/api/info').catch(() => null);
    try {
      await window.Schema.load();
      fillSpeciesPickers();
    } catch (e) {
      $('loginErr').textContent = `Cannot load the species list: ${e.message}`;
    }
    if (state.info && state.info.password_login) $('pwBox').classList.remove('hidden');

    setOnline(navigator.onLine);
    window.addEventListener('online', () => {
      setOnline(true);
      flushQueue();
    });
    window.addEventListener('offline', () => setOnline(false));
    // A radio can say "online" while still too weak to reach this specific
    // server -- the 'online' event alone is not enough of a signal (so to
    // speak) to lean on.
    setInterval(() => {
      if (navigator.onLine) flushQueue();
    }, 30000);

    if (!(navigator.mediaDevices && navigator.mediaDevices.getUserMedia)) {
      $('scanTag').classList.add('hidden');
    }

    try {
      state.session = await api('/api/admin/session');
      showConsole();
    } catch (e) {
      if (isNetworkError(e)) {
        // No signal at boot is not the same fact as "not signed in", and a
        // field session should not be bounced to the login screen just
        // because the phone has no bars right now. Proceed as whoever was
        // last signed in here; individual requests queue or fail on their
        // own once there is something concrete to say about them.
        let cached = '';
        try {
          cached = localStorage.getItem(IDENTITY_CACHE_KEY) || '';
        } catch (_) {
          /* private window; there is nothing to fall back on */
        }
        if (cached) {
          state.session = { identity_key: cached };
          showConsole();
          return;
        }
      }
      $('login').classList.remove('hidden');
    }
  }

  async function walletLogin() {
    $('loginErr').textContent = '';
    try {
      if (!(await window.Wallet.available())) {
        throw new Error('No BSV wallet found. Open this page in BSV Browser, or use the password.');
      }
      const identityKey = await window.Wallet.identityKey();
      const challenge = await api('/api/admin/challenge', {});

      // Sign the nonce under the admin protocol with the server as
      // counterparty. The server derives the same type-42 child key from its
      // own private key and this identity key, so the signature verifies
      // without any shared secret and is useless to any other server.
      const sig = await window.Wallet.createSignature({
        protocolID: [challenge.security_level, challenge.protocol],
        keyID: challenge.nonce,
        counterparty: state.info.identity_key,
        data: Array.from(new TextEncoder().encode(challenge.nonce)),
      });

      state.session = await api('/api/admin/login', {
        identity_key: identityKey,
        nonce: challenge.nonce,
        signature: bytesToHex(new Uint8Array(sig)),
      });
      showConsole();
    } catch (e) {
      $('loginErr').textContent = e.message;
    }
  }

  async function passwordLogin() {
    $('loginErr').textContent = '';
    try {
      state.session = await api('/api/admin/login', { password: $('pw').value });
      showConsole();
    } catch (e) {
      $('loginErr').textContent = e.message;
    }
  }

  function showConsole() {
    $('login').classList.add('hidden');
    $('console').classList.remove('hidden');
    $('logout').classList.remove('hidden');
    $('logoutMobile').classList.remove('hidden');
    const key = state.session.identity_key || '';
    $('who').textContent =
      key === 'operator' ? 'signed in as operator (password)' : `signed in as ${midTruncate(key)}`;
    try {
      localStorage.setItem(IDENTITY_CACHE_KEY, key);
    } catch (_) {
      /* as above */
    }
    startLocationWatch();
    renderQueue();
    renderTally();
    refresh();
    flushQueue();
  }

  async function refresh() {
    await Promise.all([loadFunding(), loadBatches(), loadRearms(), loadAuditTrail()]);
  }

  // ---- signal and pace -----------------------------------------------------

  function setOnline(isOnline) {
    const el = $('netStatus');
    if (!el) return;
    // Bare "online" read as unexplained noise -- what it is a signal *for*
    // is whether an arm queued right now can actually reach the server, so
    // the label says that rather than assuming it's obvious.
    el.textContent = isOnline ? 'Signal: connected' : 'Signal: offline, will retry';
    el.title = 'Whether this console can currently reach the server. Arms queue locally and send automatically once it can.';
    el.className = 'net-status' + (isOnline ? ' good' : ' bad');
  }

  function renderTally() {
    const el = $('armTally');
    if (el) el.textContent = state.armedCount ? `${state.armedCount} armed this session` : '';
  }

  // ---- funding ----------------------------------------------------------

  async function loadFunding() {
    try {
      const f = await api('/api/admin/funding');
      $('deposit').textContent = f.deposit_address;
      $('funding').innerHTML = [
        ['balance', `${num(f.balance)} sats`, 'balance'],
        ['per tag', `${num(f.reward_per_tag)} sats`, 'perTag'],
        ['activations left', num(f.activations_left), 'activations'],
      ]
        .map(
          ([k, v, icon]) =>
            `<div class="stat">${statIcon(FUNDING_ICONS[icon])}<div class="n tabular">${v}</div><div class="k">${k}</div></div>`
        )
        .join('');
    } catch (e) {
      $('funding').innerHTML = `<div class="stat"><div class="k">${e.message}</div></div>`;
    }
  }

  // ---- batches ----------------------------------------------------------

  async function loadBatches() {
    try {
      const data = await api('/api/admin/batches');
      const rows = (data.batches || []).map(
        (b) =>
          `<tr><td class="mono">${b.ID}</td><td class="tabular">${b.TagCount}</td>` +
          `<td class="note">${new Date(b.CreatedAt).toLocaleDateString()}</td>` +
          `<td><a href="/admin/batches/${encodeURIComponent(b.ID)}/print" target="_blank" rel="noopener">print</a></td></tr>`
      );
      $('batches').innerHTML = rows.length
        ? rows.join('')
        : emptyRow(4, 'No batches yet.');
    } catch (e) {
      $('batches').innerHTML = `<tr><td colspan="4" class="err">${e.message}</td></tr>`;
    }
  }

  async function mint() {
    $('mintErr').textContent = '';
    $('mint').disabled = true;
    try {
      const count = parseInt($('count').value, 10);
      const res = await api('/api/admin/batches', { count, species: $('mintSpecies').value });
      await loadBatches();
      // Minting used to leave no trace of having happened beyond a table
      // further down the page quietly gaining a row -- easy to miss, and
      // the print link that matters right now is in that same easy-to-miss
      // place.
      window.Toast.success(`Batch created: ${num(res.batch.TagCount)} tags.`, {
        actionLabel: 'Print',
        onAction: () => window.open(`/admin/batches/${encodeURIComponent(res.batch.ID)}/print`, '_blank', 'noopener'),
      });
    } catch (e) {
      $('mintErr').textContent = e.message;
    } finally {
      $('mint').disabled = false;
    }
  }

  // ---- position -----------------------------------------------------------
  //
  // A fix used to be fetched on demand, at the very end of the form, with
  // nothing running until a biologist tapped "Use my location" and stood
  // still for up to 20 seconds. It is now requested the moment the console
  // is up -- in the background, while species, tag id and measurements are
  // still being entered -- so by the time position is the field left to
  // fill, a fix is usually already sitting there. The button becomes a
  // manual refresh rather than the only way to ask.

  function ageLabel(ts) {
    const s = Math.round((Date.now() - ts) / 1000);
    if (s < 5) return 'just now';
    if (s < 60) return `${s}s ago`;
    return `${Math.round(s / 60)}m ago`;
  }

  function renderFix() {
    const box = $('afix');
    if (!state.fix) {
      box.textContent = 'No position fix yet.';
      box.className = 'banner';
      $('alocateLabel').textContent = 'Use my location';
      return;
    }
    // aria-live="off" on the age span specifically: it re-renders every 5s
    // (see the setInterval below) purely as a visual clock, and #afix is
    // itself a live region -- without this, a screen reader would announce
    // "just now", "5s ago", "10s ago"... forever, which is worse than saying
    // nothing. The outer box's own aria-live still covers the fix arriving
    // or changing, which is the actual news here.
    box.innerHTML =
      `${state.fix.lat.toFixed(5)}, ${state.fix.lon.toFixed(5)} ` +
      `<span class="note">(&plusmn;${Math.round(state.fix.acc)} m &middot; <span data-fix-age aria-live="off">${ageLabel(state.fixAt)}</span>)</span>`;
    box.className = 'banner good';
    $('alocateLabel').textContent = 'Refresh location';
  }

  // A fix taken minutes ago and quietly reused for the next animal in the
  // same haul is exactly the point (see the sticky fields below); a fix
  // taken an hour ago and reused without anyone noticing is a mistake. This
  // keeps the age visible without forcing a re-fetch, so the choice to
  // refresh stays a person's.
  setInterval(() => {
    const el = document.querySelector('[data-fix-age]');
    if (el && state.fixAt) el.textContent = ageLabel(state.fixAt);
  }, 5000);

  function setFix(pos) {
    state.fix = { lat: pos.coords.latitude, lon: pos.coords.longitude, acc: pos.coords.accuracy || 0 };
    state.fixAt = Date.now();
    renderFix();
    checkArmable();
  }

  function startLocationWatch() {
    if (state.watchId !== null || !navigator.geolocation) return;
    state.watchId = navigator.geolocation.watchPosition(
      setFix,
      (err) => {
        // A watch that hiccups once after already producing a good fix
        // should not alarm somebody mid-form; only report a failure if
        // there is nothing on screen yet.
        if (!state.fix) {
          $('afix').textContent = `Could not get a fix: ${err.message}`;
          $('afix').className = 'banner bad';
        }
      },
      { enableHighAccuracy: true, maximumAge: 5000, timeout: 20000 }
    );
  }

  function refreshLocationNow() {
    if (!navigator.geolocation) {
      $('afix').textContent = 'This browser will not share a location.';
      $('afix').className = 'banner bad';
      return;
    }
    if (!state.fix) {
      $('afix').innerHTML = '<span class="spin"></span> Getting a fix…';
      $('afix').className = 'banner';
    }
    navigator.geolocation.getCurrentPosition(
      setFix,
      (err) => {
        if (!state.fix) {
          $('afix').textContent = `Could not get a fix: ${err.message}`;
          $('afix').className = 'banner bad';
        } else {
          renderFix(); // still have the older fix; nothing to alarm over
        }
      },
      { enableHighAccuracy: true, timeout: 15000, maximumAge: 0 }
    );
  }

  // ---- arming a tag -----------------------------------------------------

  // Species icon and gradient are keyed by code so a given species always
  // gets the same card everywhere it appears. Anything not in the map --
  // any species this deployment adds later -- falls back to a generic
  // family silhouette rather than a broken image, which is the whole point
  // of the schema being data: a new profile shouldn't need a matching icon
  // shipped in the same release.
  const SPECIES_ICON = { CALSAP: 'blue-crab.svg', SCIOCE: 'red-drum.svg' };
  const SPECIES_GRADIENT = { CALSAP: 'estuary', SCIOCE: 'sunset' };
  const FALLBACK_ICON = 'crab-generic.svg';
  const FALLBACK_GRADIENT = 'slate';

  // fillSpeciesPickers offers every profile the deployment knows about: a
  // visual card grid for the form used daily (arming), a plain <select> for
  // the one used occasionally (minting a batch, tucked into its own tab).
  function fillSpeciesPickers() {
    const profiles = window.Schema.profiles();

    const mintEl = $('mintSpecies');
    if (mintEl) {
      mintEl.innerHTML = profiles
        .map((p) => `<option value="${p.code}">${p.common} (${p.scientific})</option>`)
        .join('');
      mintEl.value = window.Schema.profile().code;
      // Enhanced once here rather than by admin.js's other enhanceAll()
      // calls: this select's icons come from the species silhouettes
      // (SPECIES_ICON), not combobox.js's own small built-in registry, and
      // it is built once at boot rather than rebuilt per species change.
      window.Combobox.enhance(mintEl, {
        iconHTML: (option) => `<img src="/vendor/animals/${SPECIES_ICON[option.value] || FALLBACK_ICON}" alt="">`,
      });
    }

    const grid = $('armSpeciesGrid');
    if (grid) {
      grid.innerHTML = profiles
        .map((p) => {
          const grad = SPECIES_GRADIENT[p.code] || FALLBACK_GRADIENT;
          const icon = SPECIES_ICON[p.code] || FALLBACK_ICON;
          return (
            `<button type="button" class="species-card" data-species="${p.code}" role="radio" aria-checked="false" aria-pressed="false">` +
            `<div class="species-card-header" style="background: var(--grad-${grad})">` +
            `<div class="card-icon-badge"><img src="/vendor/animals/${icon}" alt="" loading="lazy"></div>` +
            `</div>` +
            `<div class="species-card-body">` +
            `<div class="species-card-name">${p.common}</div>` +
            `<div class="species-card-sci">${p.scientific}</div>` +
            `</div></button>`
          );
        })
        .join('');
      grid.querySelectorAll('.species-card').forEach((btn) => {
        btn.addEventListener('click', () => selectSpeciesCard(btn.dataset.species));
      });
      const preselect = grid.querySelector(`[data-species="${window.Schema.profile().code}"]`) || grid.querySelector('.species-card');
      if (preselect) selectSpeciesCard(preselect.dataset.species);
    } else {
      onSpeciesChange();
    }
  }

  // selectSpeciesCard is the card grid's equivalent of a <select>'s change
  // event: mark the one pressed card, rebuild the form under it.
  function selectSpeciesCard(code) {
    const grid = $('armSpeciesGrid');
    grid.querySelectorAll('.species-card').forEach((btn) => {
      const on = btn.dataset.species === code;
      btn.setAttribute('aria-pressed', String(on));
      btn.setAttribute('aria-checked', String(on));
    });
    state.armSpeciesCode = code;
    onSpeciesChange();
  }

  function onSpeciesChange() {
    state.profile = window.Schema.profile(state.armSpeciesCode);
    if (!state.profile) return;

    $('armSpeciesNote').textContent =
      `${state.profile.programme} · ${state.profile.workflow}`;

    // tagging: true, so the tagger-only fields appear and the disposition does
    // not -- a tagger is releasing the animal by definition.
    window.Schema.renderFields(state.profile, $('armFields'), { tagging: true });
    addBlankOptions();
    // Every vocab select schema.js just rendered, as a searchable popover
    // with an icon per entry where species.VocabValue.Icon names one (see
    // combobox.js). Re-run on every species change: renderFields tore down
    // and rebuilt the <select> elements from scratch, so any previous
    // enhancement was on nodes that no longer exist.
    window.Combobox.enhanceAll($('armFields'));
    for (const el of $('armFields').querySelectorAll('input, select')) {
      el.addEventListener(el.tagName === 'SELECT' ? 'change' : 'input', checkArmable);
    }
    checkArmable();
  }

  // Vocab <select>s render with no blank option (schema.js is shared with
  // the one-shot redemption form, which has no need of one). The arm form
  // submits many animals in a row, so a per-animal choice like sex needs a
  // genuine blank state to return to after each arm -- a native <select>
  // cannot be shown blank by script alone unless a blank option actually
  // exists among its choices, and without one a "cleared" field would
  // either keep displaying the last animal's answer or silently snap back
  // to whichever option is listed first, which reads as an answer nobody
  // gave. Sticky fields (see clearAnimalFields) are exempt: they are
  // supposed to keep their value.
  function addBlankOptions() {
    for (const el of $('armFields').querySelectorAll('select[data-attr]')) {
      if (el.hasAttribute('data-sticky')) continue;
      if (!el.querySelector('option[value=""]')) {
        const blank = document.createElement('option');
        blank.value = '';
        blank.textContent = 'Choose…';
        blank.disabled = true;
        el.insertBefore(blank, el.firstChild);
      }
      el.value = '';
    }
  }

  // clearAnimalFields resets everything about the arm form that describes
  // the animal just tagged, and leaves alone everything that describes the
  // place and moment (see species.Measure.Sticky / Vocab.Sticky) -- a water
  // temperature or the gear a trap haul was pulled with does not change
  // crab to crab, and re-typing it a dozen times in a row is exactly the
  // friction sticky fields exist to remove.
  function clearAnimalFields() {
    for (const el of $('armFields').querySelectorAll('input, select')) {
      if (el.hasAttribute('data-sticky')) continue;
      el.value = '';
    }
    // Setting .value directly does not fire 'change', so the enhanced
    // selects' own trigger buttons would otherwise keep showing the last
    // animal's answer after the underlying <select> has already gone
    // back to blank.
    window.Combobox.refreshAll($('armFields'));
  }

  // checkArmable refuses an animal the profile says should not carry a tag, and
  // says why, rather than letting the server refuse after the boat has moved on.
  //
  // The rule is the profile's: for a blue crab it is that only hard-shell
  // animals are tagged, because a peeler sheds the tag at its next moult and
  // takes the locked reward with it. This file does not know that; it just
  // evaluates what the schema said.
  function checkArmable() {
    if (!state.profile) return;
    const { meas, attr } = window.Schema.read($('armFields'));
    const banner = $('armBanner');

    const rule = window.Schema.notTaggable(state.profile, meas, attr);
    if (rule) {
      // Only actually mutate the text when the message changes: this runs on
      // every keystroke while a disqualifying value is entered, and armBanner
      // is a live region (see admin.html) -- writing the same string on every
      // input event would still count as a DOM mutation and risks a screen
      // reader re-announcing "do not tag this one" once per character typed.
      const msg = `Do not tag this one: ${rule.reason}.`;
      if (banner.textContent !== msg) banner.textContent = msg;
      banner.className = 'banner warn';
      banner.classList.remove('hidden');
    } else if (banner.className.indexOf('good') < 0) {
      banner.classList.add('hidden');
    }

    const ready =
      state.fix &&
      !rule &&
      $('tag').value.trim() &&
      window.Schema.complete(state.profile, meas, attr, { tagging: true });
    $('arm').disabled = !ready;
  }

  // ---- confirm before it becomes permanent ---------------------------------
  //
  // Once an arm is queued it will submit itself the moment there is signal,
  // with nobody in the loop -- so the one human checkpoint against a fumbled
  // decimal point moves to here, before queueing, rather than after a
  // network round trip a person cannot see happening.

  function armSummaryHTML() {
    const p = state.profile;
    const { meas, attr } = window.Schema.read($('armFields'));
    const rows = [];
    rows.push(['Species', p.common]);
    rows.push(['Tag', $('tag').value.trim() || '(not entered)']);
    const name = $('aname').value.trim();
    if (name) rows.push(['Name', name]);
    for (const m of p.measures || []) {
      if (meas[m.key] === undefined) continue;
      rows.push([m.label, window.Schema.show(p, m.key, meas[m.key])]);
    }
    for (const v of p.vocabs || []) {
      if (!attr[v.key]) continue;
      rows.push([v.label, window.Schema.label(p, v.key, attr[v.key])]);
    }
    rows.push([
      'Position',
      state.fix
        ? `${state.fix.lat.toFixed(5)}, ${state.fix.lon.toFixed(5)} (&plusmn;${Math.round(state.fix.acc)} m, ${ageLabel(state.fixAt)})`
        : 'no fix',
    ]);
    return rows
      .map(([k, v]) => `<div class="confirm-row"><span class="confirm-k">${escHTML(k)}</span><span class="confirm-v">${v}</span></div>`)
      .join('');
  }

  // releaseConfirmTrap undoes whatever window.FocusTrap.activate() did the
  // last time this sheet opened -- see openConfirm/closeConfirm.
  let releaseConfirmTrap = null;

  function openConfirm() {
    $('confirmBody').innerHTML = armSummaryHTML();
    $('confirmScrim').hidden = false;
    $('confirmSheet').hidden = false;
    requestAnimationFrame(() => {
      $('confirmScrim').classList.add('open');
      $('confirmSheet').classList.add('open');
    });
    document.body.style.overflow = 'hidden';
    releaseConfirmTrap = window.FocusTrap.activate($('confirmSheet'));
  }

  function closeConfirm() {
    $('confirmScrim').classList.remove('open');
    $('confirmSheet').classList.remove('open');
    document.body.style.overflow = '';
    if (releaseConfirmTrap) {
      releaseConfirmTrap();
      releaseConfirmTrap = null;
    }
    window.setTimeout(() => {
      $('confirmScrim').hidden = true;
      $('confirmSheet').hidden = true;
    }, 250);
  }

  // ---- the offline queue ----------------------------------------------------
  //
  // "Arm this tag" no longer waits on a network round trip: it always queues
  // the record locally first, then tries to send it. The common case (there
  // is signal) clears the queue entry almost immediately and is
  // indistinguishable from the old synchronous behaviour; the marsh case
  // (there is not) leaves it sitting there, visible, and retried
  // automatically the moment signal returns -- rather than a tap that just
  // silently failed and cost a biologist a filled-out form.

  function readQueue() {
    try {
      return JSON.parse(localStorage.getItem(QUEUE_KEY) || '[]');
    } catch (_) {
      return [];
    }
  }
  function writeQueue(q) {
    try {
      localStorage.setItem(QUEUE_KEY, JSON.stringify(q));
    } catch (_) {
      /* a private window, or storage full -- the in-memory copy still works
         for this page load, it just will not survive a reload offline */
    }
  }

  function queueAndArm() {
    if (!state.profile || !state.fix) return;
    const { meas, attr } = window.Schema.read($('armFields'));
    const entry = {
      localId: `${Date.now()}-${Math.random().toString(36).slice(2, 8)}`,
      tagInput: $('tag').value.trim(),
      species: state.profile.code,
      speciesCommon: state.profile.common,
      lat: state.fix.lat,
      lon: state.fix.lon,
      acc: state.fix.acc,
      meas,
      attr,
      name: $('aname').value.trim(),
      queuedAt: Date.now(),
      status: 'queued',
    };
    const q = readQueue();
    q.push(entry);
    writeQueue(q);

    // Clear the per-animal part of the form immediately, whether or not
    // this ever reaches the server this second: the biologist's next crab
    // does not wait on this one's round trip.
    $('tag').value = '';
    $('aname').value = '';
    clearAnimalFields();
    checkArmable();
    renderQueue();
    flushQueue();
  }

  async function submitArm(entry) {
    if (!(await window.Wallet.available())) {
      throw new Error('A wallet is required to attest a tagging record. Open this page in BSV Browser.');
    }

    // The identity key comes first, before the record is requested: the
    // tagger's key is written *inside* the bytes they are asked to sign,
    // so asking for the record without it would produce one record to sign
    // and a different one to submit.
    const attestPub = await window.Wallet.identityKey();
    const form = {
      tag_id: entry.tagInput,
      species: entry.species,
      lat: entry.lat,
      lon: entry.lon,
      accuracy_m: entry.acc,
      meas: entry.meas,
      attr: entry.attr,
      name: entry.name,
      attest_pub: attestPub,
    };

    // Two round trips: the server assembles the canonical record, the
    // tagger's wallet signs those exact bytes, and only then is the tag
    // armed. Signing server-side instead would make every activation
    // attributable to whoever runs the server rather than to a person.
    const preview = await api('/api/admin/activate/prepare', form);

    // Sign under the canonical tag id the server just handed back, not the
    // string that was typed. A biologist enters the displayed form with a
    // dash; the server strips it. Deriving under the typed value produces a
    // different key and an attestation that cannot verify.
    const canonicalTagID = preview.tag_id || form.tag_id;

    // The payload itself, not a hash of it: createSignature applies
    // SHA-256 to `data` before signing, and the server verifies against
    // sha256(payload). Passing a digest signs it twice and the attestation
    // fails with nothing on screen to explain why.
    const sig = await window.Wallet.createSignature({
      protocolID: ATTEST_PROTOCOL,
      keyID: canonicalTagID,
      // 'anyone', so a third party can re-derive this key from the
      // tagger's published identity key and check the attestation.
      counterparty: 'anyone',
      data: Array.from(hexToBytes(preview.observation)),
    });
    const attestSig = bytesToHex(new Uint8Array(sig));

    const res = await api('/api/admin/activate', {
      ...form,
      tag_id: canonicalTagID,
      observation: preview.observation,
      attest_sig: attestSig,
      attest_pub: attestPub,
    });
    return { ...res, tag_id: canonicalTagID };
  }

  // ---- the tag-armed confirmation ------------------------------------------
  //
  // A drawn record, not a fade-in: a trace from the tag glyph to the animal
  // just tagged, and a confirmation mark, both drawing themselves rather
  // than appearing. See the ".arm-confirm" comment in style.css for the
  // motion reasoning; TAG_GLYPH here is the same path EMPTY_ICON above
  // already draws, reused rather than redefined so the one glyph this app
  // uses for "a tag" stays the one glyph.
  const TAG_GLYPH =
    '<path d="M11 3H4.5A1.5 1.5 0 0 0 3 4.5V11l7.3 7.3a1.5 1.5 0 0 0 2.1 0l5.9-5.9a1.5 1.5 0 0 0 0-2.1L11 3Z"/><circle cx="7" cy="7" r="1.1"/>';
  // .icon-badge's tones are named for what they tint, not for a species, so
  // this maps the gradient name arming already resolved (SPECIES_GRADIENT /
  // FALLBACK_GRADIENT) to the matching tone class rather than keeping a
  // second species-to-colour table in step with the first by hand.
  const TONE_BY_GRADIENT = { estuary: 'tone-estuary', sunset: 'tone-sunset', slate: 'tone-slate' };

  // pickFunFact returns one of the species profile's own fun_facts, chosen
  // at random -- looked up by the entry's own species code rather than
  // read off state.profile, since the form may already have moved on to a
  // different species by the time a queued arm actually completes. A
  // profile with none defined yet (see species.Profile.FunFacts) simply
  // gets no fact line, the same as any other schema-driven field a species
  // hasn't filled in.
  function pickFunFact(speciesCode) {
    const profile = window.Schema.profile(speciesCode);
    const facts = profile && profile.fun_facts;
    return facts && facts.length ? facts[Math.floor(Math.random() * facts.length)] : null;
  }

  function armConfirmHTML(entry, res) {
    const who = entry.name ? `"${entry.name}"` : entry.speciesCommon || 'the animal';
    const text = `Armed ${who} (tag ${res.tag_id}) with ${num(res.satoshis)} sats. Transaction ${res.txid}`;
    const grad = SPECIES_GRADIENT[entry.species] || FALLBACK_GRADIENT;
    const icon = SPECIES_ICON[entry.species] || FALLBACK_ICON;
    const tone = TONE_BY_GRADIENT[grad] || 'tone-slate';
    const fact = pickFunFact(entry.species);
    return (
      `<div class="arm-confirm">` +
      `<svg class="arm-confirm-glyph" viewBox="0 0 20 20" aria-hidden="true">${TAG_GLYPH}</svg>` +
      `<svg class="arm-confirm-trace" viewBox="0 0 32 12" aria-hidden="true"><path d="M1 6 H31"/></svg>` +
      `<span class="arm-confirm-badge icon-badge ${tone}">` +
      `<img src="/vendor/animals/${icon}" alt="">` +
      `<svg class="arm-confirm-tick draw-mark" viewBox="0 0 20 20" aria-hidden="true"><path d="M4 10.5 8.5 15 16 6"/></svg>` +
      `</span>` +
      `<span class="arm-confirm-text">${escHTML(text)}</span>` +
      `</div>` +
      // Held back until the tick above has finished drawing (see
      // .arm-confirm-fact's own delay in style.css), so this reads as the
      // sequence's last beat -- a small reward for actually reading the
      // banner -- rather than competing with the record itself for
      // attention.
      (fact
        ? `<p class="arm-confirm-fact"><span class="arm-confirm-fact-label">Did you know?</span>${escHTML(fact)}</p>`
        : '')
    );
  }

  function announceArmed(entry, res) {
    $('armBanner').className = 'banner good';
    $('armBanner').innerHTML = armConfirmHTML(entry, res);
    $('armBanner').classList.remove('hidden');
  }

  let flushing = false;
  async function flushQueue() {
    if (flushing) return;
    flushing = true;
    try {
      const q = readQueue();
      for (const entry of q) {
        if (entry.status === 'error') continue; // needs a person, not a retry loop
        entry.status = 'sending';
        writeQueue(q);
        renderQueue();
        try {
          const res = await submitArm(entry);
          entry.status = 'done';
          state.armedCount++;
          renderTally();
          buzz(BUZZ_OK);
          announceArmed(entry, res);
          loadFunding();
        } catch (e) {
          if (isNetworkError(e)) {
            entry.status = 'queued';
            writeQueue(q);
            renderQueue();
            setOnline(false);
            break; // stop here; the rest would fail the same way right now
          }
          entry.status = 'error';
          entry.error = e.message;
          buzz(BUZZ_ERR);
        }
      }
      writeQueue(q.filter((e) => e.status !== 'done'));
      renderQueue();
    } finally {
      flushing = false;
    }
  }

  function renderQueue() {
    const box = $('armQueue');
    if (!box) return;
    const q = readQueue();
    if (!q.length) {
      box.innerHTML = '';
      box.classList.add('hidden');
      return;
    }
    box.classList.remove('hidden');
    box.innerHTML = q
      .map((e) => {
        const label = `${e.speciesCommon || e.species} · tag ${e.tagInput || '(no id)'}`;
        let status, actions;
        if (e.status === 'sending') {
          status = '<span class="spin"></span> sending…';
          actions = '';
        } else if (e.status === 'error') {
          status = `<span class="err-inline">${escHTML(e.error || 'failed')}</span>`;
          actions =
            `<button type="button" data-retry="${e.localId}">retry</button>` +
            `<button type="button" data-discard="${e.localId}">discard</button>`;
        } else {
          status = 'waiting for signal…';
          actions = `<button type="button" data-discard="${e.localId}">discard</button>`;
        }
        return (
          `<div class="queue-item"><div><strong>${escHTML(label)}</strong><div class="note">${status}</div></div>` +
          `<div class="queue-actions">${actions}</div></div>`
        );
      })
      .join('');
  }

  // ---- QR scanner -----------------------------------------------------------
  //
  // A tag id used to mean leaving this page for a separate camera or QR app,
  // reading the code, and typing it in with wet or gloved hands. The camera
  // is already in the phone; this puts it in the same view as the field it
  // fills.

  let scanStream = null;
  let scanRAF = null;
  let jsQRLoading = null;

  function loadJsQR() {
    if (window.jsQR) return Promise.resolve();
    if (jsQRLoading) return jsQRLoading;
    jsQRLoading = new Promise((resolve, reject) => {
      const s = document.createElement('script');
      s.src = '/vendor/jsqr.min.js';
      s.onload = () => resolve();
      s.onerror = () => reject(new Error('could not load the QR scanner'));
      document.head.appendChild(s);
    });
    return jsQRLoading;
  }

  // extractTagID pulls the tag id out of a scanned QR payload. A tag's QR
  // encodes the full redemption URL with the secret in the fragment (see
  // tagkey.QRPayload) -- arming needs none of that, only the id between the
  // last "/t/" and the "#". The secret is never read or stored here.
  function extractTagID(text) {
    const m = /\/t\/([0-9A-Za-z]+)(?:#|$)/.exec(text);
    if (m) return m[1];
    if (/^[0-9A-Za-z-]{6,10}$/.test(text.trim())) return text.trim().replace(/-/g, '');
    return null;
  }

  function onScanned(text) {
    const id = extractTagID(text);
    if (!id) return; // not a wildtag QR; keep scanning rather than fail loudly
    closeScanner();
    $('tag').value = id;
    buzz(BUZZ_OK);
    checkArmable();
  }

  function scanNativeLoop(video) {
    const detector = new window.BarcodeDetector({ formats: ['qr_code'] });
    const tick = async () => {
      if (!scanStream) return;
      try {
        const codes = await detector.detect(video);
        if (codes.length) {
          onScanned(codes[0].rawValue);
          return;
        }
      } catch (_) {
        // A frame with nothing decodable in it is not an error.
      }
      scanRAF = requestAnimationFrame(tick);
    };
    scanRAF = requestAnimationFrame(tick);
  }

  function scanJsQRLoop(video) {
    const canvas = $('scannerCanvas');
    const ctx = canvas.getContext('2d', { willReadFrequently: true });
    const tick = () => {
      if (!scanStream) return;
      if (video.readyState === video.HAVE_ENOUGH_DATA && video.videoWidth) {
        canvas.width = video.videoWidth;
        canvas.height = video.videoHeight;
        ctx.drawImage(video, 0, 0, canvas.width, canvas.height);
        const frame = ctx.getImageData(0, 0, canvas.width, canvas.height);
        const code = window.jsQR(frame.data, frame.width, frame.height);
        if (code) {
          onScanned(code.data);
          return;
        }
      }
      scanRAF = requestAnimationFrame(tick);
    };
    scanRAF = requestAnimationFrame(tick);
  }

  // releaseScannerTrap mirrors releaseConfirmTrap, see its comment.
  let releaseScannerTrap = null;

  async function openScanner() {
    $('scannerErr').textContent = '';
    $('scannerOverlay').hidden = false;
    document.body.style.overflow = 'hidden';
    releaseScannerTrap = window.FocusTrap.activate($('scannerOverlay'));
    try {
      scanStream = await navigator.mediaDevices.getUserMedia({ video: { facingMode: 'environment' } });
      const video = $('scannerVideo');
      video.srcObject = scanStream;
      await video.play();
      if ('BarcodeDetector' in window) {
        scanNativeLoop(video);
      } else {
        await loadJsQR();
        scanJsQRLoop(video);
      }
    } catch (e) {
      $('scannerErr').textContent =
        e.name === 'NotAllowedError'
          ? 'Camera permission was refused. Allow it in the browser settings, or type the tag id.'
          : `Could not start the camera: ${e.message}`;
    }
  }

  function closeScanner() {
    $('scannerOverlay').hidden = true;
    document.body.style.overflow = '';
    if (releaseScannerTrap) {
      releaseScannerTrap();
      releaseScannerTrap = null;
    }
    if (scanRAF) cancelAnimationFrame(scanRAF);
    scanRAF = null;
    if (scanStream) {
      scanStream.getTracks().forEach((t) => t.stop());
      scanStream = null;
    }
  }

  // ---- re-arming --------------------------------------------------------

  async function loadRearms() {
    // Whatever was checked belonged to the previous render's rows; a fresh
    // load starts clean rather than carrying stale ids nothing on screen
    // still refers to.
    state.rearmSelected.clear();
    renderRearmBulkBar();
    const selectAll = $('rearmSelectAll');
    if (selectAll) selectAll.checked = false;
    try {
      const data = await api('/api/admin/tags?status=cooldown&limit=200');
      const rows = (data.tags || []).map(
        (t) =>
          `<tr><td><input type="checkbox" class="rearm-check" data-id="${t.tag_id}" aria-label="Select tag ${t.display}"></td>` +
          `<td class="mono">${t.display}</td><td class="tabular">${t.generation}</td>` +
          `<td class="tabular">${num(t.satoshis)} sats</td>` +
          `<td><button class="btn-icon" data-rearm="${t.tag_id}">${REARM_ICON}put back</button></td></tr>`
      );
      $('rearms').innerHTML = rows.length
        ? rows.join('')
        : emptyRow(5, 'Nothing is waiting.');
    } catch (e) {
      $('rearms').innerHTML = `<tr><td colspan="5" class="err">${e.message}</td></tr>`;
    }
  }

  async function rearm(tagID, button) {
    const display = button.closest('tr').querySelector('.mono').textContent;
    button.disabled = true;
    try {
      await api('/api/admin/rearm', { tag_id: tagID });
      await loadRearms();
      window.Toast.success(`Tag ${display} put back in service.`);
    } catch (e) {
      // Rather than parking the error as the button's own label -- which
      // used to leave it stuck disabled with no way to try again short of
      // reloading the page -- report it and hand the row back.
      window.Toast.error(`Could not put ${display} back in service: ${e.message}`);
      button.disabled = false;
    }
  }

  // ---- bulk re-arm --------------------------------------------------------

  function renderRearmBulkBar() {
    const bar = $('rearmBulkBar');
    if (!bar) return;
    const n = state.rearmSelected.size;
    bar.classList.toggle('hidden', n === 0);
    $('rearmBulkCount').textContent = n ? `${n} selected` : '';
  }

  // bulkRearm sends the same single-tag request loadRearms()'s individual
  // buttons already use, one at a time rather than in parallel -- a burst of
  // simultaneous re-arms is exactly the kind of load spike a console meant
  // to be reached for from a boat with one bar of signal should not create
  // -- and reports one summary rather than one toast per tag.
  async function bulkRearm(ids) {
    const btn = $('rearmBulkGo');
    if (btn) btn.disabled = true;
    let ok = 0;
    const failures = [];
    for (const id of ids) {
      try {
        await api('/api/admin/rearm', { tag_id: id });
        ok++;
      } catch (e) {
        failures.push(e.message);
      }
    }
    await loadRearms();
    if (btn) btn.disabled = false;
    if (failures.length === 0) {
      window.Toast.success(`Put ${ok} tag${ok === 1 ? '' : 's'} back in service.`);
    } else if (ok === 0) {
      window.Toast.error(`Could not put any tags back in service: ${failures[0]}`);
    } else {
      window.Toast.error(`Put ${ok} back in service; ${failures.length} failed. First error: ${failures[0]}`);
    }
  }

  // ---- audit trail --------------------------------------------------------

  // actorLabel mid-truncates an identity key the same way the "signed in
  // as" line does, but leaves the password-login actor's literal name alone
  // -- "operator" mid-truncated would just be confusing.
  const actorLabel = (a) => (a === 'operator' ? 'operator' : midTruncate(a));

  async function loadAuditTrail() {
    try {
      const data = await api('/api/admin/audit');
      const rows = (data.entries || []).map(
        (e) =>
          `<tr><td class="note">${new Date(e.At).toLocaleString()}</td>` +
          `<td class="mono">${escHTML(actorLabel(e.Actor))}</td>` +
          `<td>${escHTML(e.Action)}</td>` +
          `<td class="note">${escHTML(e.Detail || '')}</td></tr>`
      );
      $('audit').innerHTML = rows.length ? rows.join('') : emptyRow(4, 'Nothing recorded yet.');
    } catch (e) {
      $('audit').innerHTML = `<tr><td colspan="4" class="err">${e.message}</td></tr>`;
    }
  }

  // ---- wiring -----------------------------------------------------------

  document.addEventListener('DOMContentLoaded', () => {
    $('walletLogin').addEventListener('click', walletLogin);
    $('pwLogin').addEventListener('click', passwordLogin);
    const doLogout = async (e) => {
      e.preventDefault();
      if (state.watchId !== null) navigator.geolocation.clearWatch(state.watchId);
      await api('/api/admin/logout', {});
      location.reload();
    };
    $('logout').addEventListener('click', doLogout);
    $('logoutMobile').addEventListener('click', doLogout);
    $('mint').addEventListener('click', mint);
    $('alocate').addEventListener('click', refreshLocationNow);
    $('arm').addEventListener('click', openConfirm);
    $('confirmBack').addEventListener('click', closeConfirm);
    $('confirmGo').addEventListener('click', () => {
      closeConfirm();
      queueAndArm();
    });
    $('tag').addEventListener('input', checkArmable);
    $('scanTag').addEventListener('click', openScanner);
    $('scannerClose').addEventListener('click', closeScanner);
    document.addEventListener('keydown', (e) => {
      if (e.key !== 'Escape') return;
      if (!$('scannerOverlay').hidden) closeScanner();
      else if (!$('confirmSheet').hidden) closeConfirm();
    });
    $('rearms').addEventListener('click', (e) => {
      // closest, not e.target directly: the button now has an icon inside
      // it, so a click can land on the <svg> rather than the <button> that
      // carries the data attribute.
      const button = e.target.closest && e.target.closest('[data-rearm]');
      if (button) rearm(button.getAttribute('data-rearm'), button);
    });
    $('rearms').addEventListener('change', (e) => {
      const cb = e.target.closest && e.target.closest('.rearm-check');
      if (!cb) return;
      if (cb.checked) state.rearmSelected.add(cb.dataset.id);
      else state.rearmSelected.delete(cb.dataset.id);
      renderRearmBulkBar();
    });
    $('rearmSelectAll').addEventListener('change', (e) => {
      const checked = e.target.checked;
      document.querySelectorAll('#rearms .rearm-check').forEach((cb) => {
        cb.checked = checked;
        if (checked) state.rearmSelected.add(cb.dataset.id);
        else state.rearmSelected.delete(cb.dataset.id);
      });
      renderRearmBulkBar();
    });
    $('rearmBulkGo').addEventListener('click', () => {
      const ids = Array.from(state.rearmSelected);
      if (ids.length) bulkRearm(ids);
    });
    $('armQueue').addEventListener('click', (e) => {
      const discard = e.target.getAttribute && e.target.getAttribute('data-discard');
      const retry = e.target.getAttribute && e.target.getAttribute('data-retry');
      if (discard) {
        writeQueue(readQueue().filter((x) => x.localId !== discard));
        renderQueue();
      } else if (retry) {
        const q = readQueue();
        const item = q.find((x) => x.localId === retry);
        if (item) {
          item.status = 'queued';
          delete item.error;
          writeQueue(q);
          renderQueue();
          flushQueue();
        }
      }
    });
    boot();
  });

  // ---- dev-mode mocks, see devpanel.js -----------------------------------

  function mockFunding() {
    $('funding').innerHTML = [
      ['balance', '1,840,000 sats', 'balance'],
      ['per tag', '20,000 sats', 'perTag'],
      ['activations left', '92', 'activations'],
    ]
      .map(
        ([k, v, icon]) =>
          `<div class="stat">${statIcon(FUNDING_ICONS[icon])}<div class="n tabular">${v}</div><div class="k">${k}</div></div>`
      )
      .join('');
    $('deposit').textContent = '1EstuaryDemoDepositAddressXXXXXXXX';
  }

  function mockBatches() {
    const rows = [
      ['B20260901-AD87F5', 50, 'a week ago'],
      ['B20260814-113C02', 100, '3 weeks ago'],
    ];
    $('batches').innerHTML = rows.map(([id, n, when]) =>
      `<tr><td class="mono">${id}</td><td class="tabular">${n}</td>` +
      `<td class="note">${when}</td><td><a href="#">print</a></td></tr>`).join('');
  }

  function mockRearms() {
    const rows = [
      ['K2M-9Q7', 2, '20,000 sats'],
      ['B7X-4RT', 1, '5,000 sats'],
    ];
    $('rearms').innerHTML = rows.map(([id, gen, sats]) =>
      `<tr><td><input type="checkbox" class="rearm-check" data-id="${id}" aria-label="Select tag ${id}"></td>` +
      `<td class="mono">${id}</td><td class="tabular">${gen}</td>` +
      `<td class="tabular">${sats}</td><td><button class="btn-icon" data-rearm="${id}">${REARM_ICON}put back</button></td></tr>`).join('');
  }

  function mockAudit() {
    const now = Date.now();
    const rows = [
      [now - 2 * 60000, '02a1b2c3d4e5f60718293a4b5c6d7e8f9012345678', 'tag.activate', 'K2M-9Q7 · Atlantic blue crab'],
      [now - 40 * 60000, 'operator', 'batch.print', 'B20260901-AD87F5'],
      [now - 3 * 3600000, 'operator', 'batch.mint', '50 tags · Atlantic blue crab'],
      [now - 26 * 3600000, '02a1b2c3d4e5f60718293a4b5c6d7e8f9012345678', 'tag.rearm', 'B7X-4RT'],
    ];
    $('audit').innerHTML = rows
      .map(
        ([at, actor, action, detail]) =>
          `<tr><td class="note">${new Date(at).toLocaleString()}</td>` +
          `<td class="mono">${escHTML(actorLabel(actor))}</td><td>${escHTML(action)}</td>` +
          `<td class="note">${escHTML(detail)}</td></tr>`
      )
      .join('');
  }

  // Shows the signed-in shell without showConsole()'s call to refresh(),
  // which would immediately overwrite the mock rows below with the real
  // (empty, on a fresh dev deployment) API responses.
  function mockShowConsole(identityKey) {
    $('login').classList.add('hidden');
    $('console').classList.remove('hidden');
    $('logout').classList.remove('hidden');
    $('logoutMobile').classList.remove('hidden');
    $('who').textContent =
      identityKey === 'operator' ? 'signed in as operator (password)' : `signed in as ${midTruncate(identityKey)}`;
    setOnline(navigator.onLine);
  }

  window.DevMocks = window.DevMocks || {};
  window.DevMocks.admin = {
    'Signed out': () => {
      $('console').classList.add('hidden');
      $('login').classList.remove('hidden');
      $('pwBox').classList.remove('hidden');
      $('logout').classList.add('hidden');
      $('logoutMobile').classList.add('hidden');
    },
    'Signed in: wallet identity': () => {
      mockShowConsole('02a1b2c3d4e5f60718293a4b5c6d7e8f9012345678');
      mockFunding();
      mockBatches();
      mockRearms();
      mockAudit();
    },
    'Signed in: operator (password)': () => {
      mockShowConsole('operator');
      mockFunding();
      mockBatches();
      mockRearms();
      mockAudit();
    },
    'Arm: success banner': () => {
      $('armBanner').className = 'banner good';
      $('armBanner').innerHTML = armConfirmHTML(
        { name: 'Old Bertha', speciesCommon: 'Atlantic blue crab', species: 'CALSAP' },
        { tag_id: 'K2M9Q7C', satoshis: 20000, txid: '4f3c9e8a1b2d5f60718293a4b5c6d7e8f9012345678901234567890abcdef01' }
      );
      $('armBanner').classList.remove('hidden');
    },
    'Arm: not taggable warning': () => {
      $('armBanner').className = 'banner warn';
      $('armBanner').textContent = 'Do not tag this one: shell is still soft, it will shed this tag at its next moult.';
      $('armBanner').classList.remove('hidden');
    },
    'Arm: queued, no signal': () => {
      $('armQueue').classList.remove('hidden');
      $('armQueue').innerHTML =
        '<div class="queue-item"><div><strong>Atlantic blue crab · tag K2M-9Q7</strong>' +
        '<div class="note">waiting for signal…</div></div>' +
        '<div class="queue-actions"><button type="button">discard</button></div></div>';
      setOnline(false);
    },
    'Arm: needs attention': () => {
      $('armQueue').classList.remove('hidden');
      $('armQueue').innerHTML =
        '<div class="queue-item"><div><strong>Atlantic blue crab · tag K2M-9Q7</strong>' +
        '<div class="note"><span class="err-inline">arm this tag: insufficient funds to cover the reward</span></div></div>' +
        '<div class="queue-actions"><button type="button">retry</button><button type="button">discard</button></div></div>';
    },
    'Arm: request failed': () => {
      $('armErr').textContent = 'arm this tag: insufficient funds to cover the reward';
    },
    'Mint: request failed': () => {
      $('mintErr').textContent = 'create batch: species "SCIOCE" is not configured on this deployment';
    },
    'Toast: batch created': () => {
      window.Toast.success('Batch created: 50 tags.', { actionLabel: 'Print', onAction: () => {} });
    },
    'Toast: put back in service': () => {
      window.Toast.success('Tag K2M-9Q7 put back in service.');
    },
    'Toast: rearm failed': () => {
      window.Toast.error('Could not put K2M-9Q7 back in service: tag is not in cooldown.');
    },
    'Login: wrong password': () => {
      $('login').classList.remove('hidden');
      $('loginErr').textContent = 'that password is not recognised';
    },
  };
})();
