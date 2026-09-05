// The admin console: sign in, mint batches, arm tags, put cooled-down tags back
// in service.
(function () {
  'use strict';

  const $ = (id) => document.getElementById(id);
  const num = (n) => Number(n || 0).toLocaleString();

  // The same tag mark as the wordmark and favicon, drawn as an outline
  // rather than filled -- a table with nothing in it yet, not an error.
  const EMPTY_ICON =
    '<svg viewBox="0 0 20 20" fill="none" stroke-width="1.3" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">' +
    '<path d="M11 3H4.5A1.5 1.5 0 0 0 3 4.5V11l7.3 7.3a1.5 1.5 0 0 0 2.1 0l5.9-5.9a1.5 1.5 0 0 0 0-2.1L11 3Z"/><circle cx="7" cy="7" r="1.1"/></svg>';
  const emptyRow = (colspan, text) =>
    `<tr><td colspan="${colspan}"><div class="empty-state"><span class="icon-badge">${EMPTY_ICON}</span><span>${text}</span></div></td></tr>`;

  const ATTEST_PROTOCOL = [2, 'wildtag observation'];

  const state = { info: null, fix: null, session: null, profile: null };

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

  const bytesToHex = (b) => Array.from(b, (x) => x.toString(16).padStart(2, '0')).join('');
  const hexToBytes = (h) => {
    const out = new Uint8Array(h.length / 2);
    for (let i = 0; i < out.length; i++) out[i] = parseInt(h.substr(i * 2, 2), 16);
    return out;
  };

  // ---- session ----------------------------------------------------------

  async function boot() {
    state.info = await api('/api/info').catch(() => null);
    try {
      await window.Schema.load();
      fillSpeciesPickers();
    } catch (e) {
      $('loginErr').textContent = `Cannot load the species list: ${e.message}`;
    }
    if (state.info && state.info.password_login) $('pwBox').classList.remove('hidden');

    try {
      state.session = await api('/api/admin/session');
      showConsole();
    } catch (_) {
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
      key === 'operator' ? 'signed in as operator (password)' : `signed in as ${key.slice(0, 16)}…`;
    refresh();
  }

  async function refresh() {
    await Promise.all([loadFunding(), loadBatches(), loadRearms()]);
  }

  // ---- funding ----------------------------------------------------------

  async function loadFunding() {
    try {
      const f = await api('/api/admin/funding');
      $('deposit').textContent = f.deposit_address;
      $('funding').innerHTML = [
        ['balance', `${num(f.balance)} sats`],
        ['per tag', `${num(f.reward_per_tag)} sats`],
        ['activations left', num(f.activations_left)],
      ]
        .map(([k, v]) => `<div class="stat"><div class="n tabular">${v}</div><div class="k">${k}</div></div>`)
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
      await api('/api/admin/batches', { count, species: $('mintSpecies').value });
      await loadBatches();
    } catch (e) {
      $('mintErr').textContent = e.message;
    } finally {
      $('mint').disabled = false;
    }
  }

  // ---- arming a tag -----------------------------------------------------

  function locate() {
    const box = $('afix');
    if (!navigator.geolocation) {
      box.textContent = 'This browser will not share a location.';
      box.className = 'banner bad';
      return;
    }
    box.innerHTML = '<span class="spin"></span> Getting a fix…';
    box.className = 'banner';
    navigator.geolocation.getCurrentPosition(
      (pos) => {
        state.fix = { lat: pos.coords.latitude, lon: pos.coords.longitude, acc: pos.coords.accuracy || 0 };
        box.innerHTML =
          `${state.fix.lat.toFixed(5)}, ${state.fix.lon.toFixed(5)} ` +
          `<span class="note">(&plusmn;${Math.round(state.fix.acc)} m)</span>`;
        box.className = 'banner good';
        checkArmable();
      },
      (err) => {
        box.textContent = `Could not get a fix: ${err.message}`;
        box.className = 'banner bad';
      },
      { enableHighAccuracy: true, timeout: 20000, maximumAge: 0 }
    );
  }

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
    for (const el of $('armFields').querySelectorAll('input, select')) {
      el.addEventListener(el.tagName === 'SELECT' ? 'change' : 'input', checkArmable);
    }
    checkArmable();
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
      banner.textContent = `Do not tag this one: ${rule.reason}.`;
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

  function armForm() {
    const { meas, attr } = window.Schema.read($('armFields'));
    return {
      tag_id: $('tag').value.trim(),
      species: state.profile.code,
      lat: state.fix.lat,
      lon: state.fix.lon,
      accuracy_m: state.fix.acc,
      meas,
      attr,
      name: $('aname').value.trim(),
    };
  }

  async function arm() {
    $('armErr').textContent = '';
    $('arm').disabled = true;
    try {
      if (!(await window.Wallet.available())) {
        throw new Error('A wallet is required to attest a tagging record. Open this page in BSV Browser.');
      }

      // The identity key comes first, before the record is requested: the
      // tagger's key is written *inside* the bytes they are asked to sign,
      // so asking for the record without it would produce one record to sign
      // and a different one to submit.
      const attestPub = await window.Wallet.identityKey();
      const form = { ...armForm(), attest_pub: attestPub };

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

      let attestSig = '';
      {
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
        attestSig = bytesToHex(new Uint8Array(sig));
      }

      const res = await api('/api/admin/activate', {
        ...form,
        tag_id: canonicalTagID,
        observation: preview.observation,
        attest_sig: attestSig,
        attest_pub: attestPub,
      });

      $('armBanner').className = 'banner good';
      $('armBanner').textContent = `Armed with ${num(res.satoshis)} sats. Transaction ${res.txid}`;
      $('armBanner').classList.remove('hidden');
      $('tag').value = '';
      $('aname').value = '';
      for (const el of $('armFields').querySelectorAll('input')) el.value = '';
      await loadFunding();
    } catch (e) {
      $('armErr').textContent = e.message;
    } finally {
      checkArmable();
    }
  }

  // ---- re-arming --------------------------------------------------------

  async function loadRearms() {
    try {
      const data = await api('/api/admin/tags?status=cooldown&limit=200');
      const rows = (data.tags || []).map(
        (t) =>
          `<tr><td class="mono">${t.display}</td><td class="tabular">${t.generation}</td>` +
          `<td class="tabular">${num(t.satoshis)} sats</td>` +
          `<td><button data-rearm="${t.tag_id}">put back</button></td></tr>`
      );
      $('rearms').innerHTML = rows.length
        ? rows.join('')
        : emptyRow(4, 'Nothing is waiting.');
    } catch (e) {
      $('rearms').innerHTML = `<tr><td colspan="4" class="err">${e.message}</td></tr>`;
    }
  }

  async function rearm(tagID, button) {
    button.disabled = true;
    try {
      await api('/api/admin/rearm', { tag_id: tagID });
      await loadRearms();
    } catch (e) {
      button.textContent = e.message;
    }
  }

  // ---- wiring -----------------------------------------------------------

  document.addEventListener('DOMContentLoaded', () => {
    $('walletLogin').addEventListener('click', walletLogin);
    $('pwLogin').addEventListener('click', passwordLogin);
    const doLogout = async (e) => {
      e.preventDefault();
      await api('/api/admin/logout', {});
      location.reload();
    };
    $('logout').addEventListener('click', doLogout);
    $('logoutMobile').addEventListener('click', doLogout);
    $('mint').addEventListener('click', mint);
    $('alocate').addEventListener('click', locate);
    $('arm').addEventListener('click', arm);
    $('tag').addEventListener('input', checkArmable);
    $('rearms').addEventListener('click', (e) => {
      const id = e.target.getAttribute && e.target.getAttribute('data-rearm');
      if (id) rearm(id, e.target);
    });
    boot();
  });

  // ---- dev-mode mocks, see devpanel.js -----------------------------------

  function mockFunding() {
    $('funding').innerHTML = [
      ['balance', '1,840,000 sats'],
      ['per tag', '20,000 sats'],
      ['activations left', '92'],
    ].map(([k, v]) => `<div class="stat"><div class="n tabular">${v}</div><div class="k">${k}</div></div>`).join('');
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
      `<tr><td class="mono">${id}</td><td class="tabular">${gen}</td>` +
      `<td class="tabular">${sats}</td><td><button data-rearm="${id}">put back</button></td></tr>`).join('');
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
      identityKey === 'operator' ? 'signed in as operator (password)' : `signed in as ${identityKey.slice(0, 16)}…`;
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
    },
    'Signed in: operator (password)': () => {
      mockShowConsole('operator');
      mockFunding();
      mockBatches();
      mockRearms();
    },
    'Arm: success banner': () => {
      $('armBanner').className = 'banner good';
      $('armBanner').textContent = 'Armed with 20,000 sats. Transaction 4f3c9e8a1b2d5f60718293a4b5c6d7e8f9012345678901234567890abcdef01';
      $('armBanner').classList.remove('hidden');
    },
    'Arm: not taggable warning': () => {
      $('armBanner').className = 'banner warn';
      $('armBanner').textContent = 'Do not tag this one: shell is still soft, it will shed this tag at its next moult.';
      $('armBanner').classList.remove('hidden');
    },
    'Arm: request failed': () => {
      $('armErr').textContent = 'arm this tag: insufficient funds to cover the reward';
    },
    'Mint: request failed': () => {
      $('mintErr').textContent = 'create batch: species "SCIOCE" is not configured on this deployment';
    },
    'Login: wrong password': () => {
      $('login').classList.remove('hidden');
      $('loginErr').textContent = 'that password is not recognised';
    },
  };
})();
