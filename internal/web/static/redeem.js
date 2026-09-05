// The redemption page.
//
// The interesting part is verifyPayout(). Everything else is a form.
//
// This page holds the tag's private key, derived from the fragment of its own
// URL, and uses it to sign a transaction the server built. That is only safe if
// the page checks what it is signing -- otherwise the server could hand it a
// transaction paying somebody else entirely, and the page would sign it. The
// check is possible without ever putting a finder's private key in the page:
// BRC-29 payment outputs are derived with type-42, so the finder's own wallet
// can be asked to derive the key output zero *should* be locked to, and the
// page compares that against the transaction byte for byte.
(function () {
  'use strict';

  const $ = (id) => document.getElementById(id);
  const { Transaction, PrivateKey, PublicKey, Hash, Utils, TransactionSignature } = window.bsv;

  // BRC-29's protocol tuple. Security level 2, and the protocol id is the
  // BRC-29 identifier itself; the key id is the derivation prefix and suffix
  // joined by a single space. Getting any of these wrong yields a different
  // derived key and the payout check fails for the wrong reason.
  const BRC29_PROTOCOL = [2, '3241645161d8'];

  // The protocol a finder's wallet attests their report under.
  const ATTEST_PROTOCOL = [2, 'wildtag observation'];

  const state = {
    tagID: null,
    secret: null,
    tagKey: null,
    info: null,
    fix: null,
    quote: null,
    identityKey: null,
    canName: false,
    // profile is the species this tag was armed for, fetched from
    // /api/schema. Every field on the form comes from it.
    profile: null,
    // walletAvailable is checked once at boot, in parallel with the tag
    // fetch, so a finder with no compatible wallet learns that before
    // filling out the whole form -- not at the moment they hit submit,
    // which is where this used to surface and is the worst possible time
    // for it.
    walletAvailable: true,
    // receipt is set the instant /api/redeem/complete returns. From that
    // point the payment has already broadcast; see redeem()'s own comment
    // on why nothing downstream may ever repeat steps 1-5 once this is set.
    receipt: null,
    pendingBonusSats: null,
    // retryGoesToForm/pendingRetry decide what the "Try again" button (and
    // the automatic retry on reconnect) actually do -- see fail().
    retryGoesToForm: true,
    pendingRetry: false,
  };

  // ---- tag identity, out of the URL fragment ----------------------------

  function readFragment() {
    // The fragment never reaches the server. That is the whole reason the
    // secret lives here rather than in the path: it stays out of access logs,
    // Referer headers and anything sitting in front of this app.
    const raw = location.hash.replace(/^#/, '').trim();
    if (!raw) return null;
    try {
      return base64urlToBytes(raw);
    } catch (_) {
      return null;
    }
  }

  function base64urlToBytes(s) {
    const pad = s.length % 4 === 0 ? '' : '='.repeat(4 - (s.length % 4));
    const bin = atob(s.replace(/-/g, '+').replace(/_/g, '/') + pad);
    const out = new Uint8Array(bin.length);
    for (let i = 0; i < bin.length; i++) out[i] = bin.charCodeAt(i);
    if (out.length !== 16) throw new Error('wrong length');
    return out;
  }

  // The tag's spending key, derived from the secret exactly as the server
  // derives it from the master seed. Keep the domain string in step with
  // internal/tagkey.
  function tagKeyFrom(secret) {
    const prefix = Utils.toArray('wildtag-v1-key|', 'utf8');
    const material = prefix.concat(Array.from(secret));
    return new PrivateKey(Hash.sha256(material));
  }

  // ---- small helpers ----------------------------------------------------

  const bytesToHex = (b) => Array.from(b, (x) => x.toString(16).padStart(2, '0')).join('');
  const hexToBytes = (h) => {
    const out = new Uint8Array(h.length / 2);
    for (let i = 0; i < out.length; i++) out[i] = parseInt(h.substr(i * 2, 2), 16);
    return out;
  };

  const sats = (n) => `${Number(n).toLocaleString()} sats`;

  // t reaches into i18n.js's dictionary for the handful of strings this file
  // builds itself rather than leaves as static markup (see i18n.js's own
  // comment on why coverage stops at data-i18n). Guarded rather than assumed
  // present: i18n.js is the last script on the page, but a page that somehow
  // loaded without it should still read correctly in English.
  const t = (key, fallback) => (window.I18n ? window.I18n.t(key) : fallback);

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
  // "the request reached the server and the server said no" -- identical to
  // admin.js's own isNetworkError, and for the same reason: fetch() rejects
  // with a TypeError when it cannot reach anything at all, and api() above
  // throws a plain Error once a request has actually completed. The
  // distinction decides whether a failed step retries itself once there is
  // a signal, or sits there asking a person to look at it.
  const isNetworkError = (e) => e instanceof TypeError;

  // ---- resuming an interrupted payment -----------------------------------
  //
  // /api/redeem/complete broadcasts the payment. Once that call returns, the
  // reward has moved on chain no matter what happens next -- so the receipt
  // is written here, to disk, before the wallet handoff (step 6) is even
  // attempted. If that handoff fails, or the tab is closed before it
  // finishes, the next visit to this exact tag resumes at the one step left
  // instead of asking an already-redeemed tag for a fresh quote, which the
  // server would refuse in a way that reads as "something is wrong" to
  // someone who has, in fact, already been paid.
  const PENDING_KEY = 'wildtag.pendingReceipt.v1';
  const PENDING_MAX_AGE_MS = 24 * 60 * 60 * 1000;

  function persistPendingReceipt(tagID, receipt, bonusSats) {
    try {
      localStorage.setItem(PENDING_KEY, JSON.stringify({ tagID, receipt, bonusSats, at: Date.now() }));
    } catch (_) {
      // Private window, or storage blocked. The in-memory state.receipt still
      // covers this page load; only a reload before the handoff finishes
      // would be unrecoverable, and that was already true before this existed.
    }
  }
  function readPendingReceipt(tagID) {
    try {
      const raw = JSON.parse(localStorage.getItem(PENDING_KEY) || 'null');
      if (!raw || raw.tagID !== tagID) return null;
      // Past a day, whatever happened has almost certainly already been
      // resolved one way or another (or needs a person, not a silent
      // auto-resume) -- stale entries are dropped rather than replayed.
      if (Date.now() - raw.at > PENDING_MAX_AGE_MS) return null;
      return raw;
    } catch (_) {
      return null;
    }
  }
  function clearPendingReceipt() {
    try {
      localStorage.removeItem(PENDING_KEY);
    } catch (_) {
      /* as above */
    }
  }

  function step(name, cls) {
    const li = document.querySelector(`#steps li[data-step="${name}"]`);
    if (!li) return;
    li.classList.remove('doing', 'done');
    if (cls) li.classList.add(cls);
  }

  function fail(msg) {
    $('payErr').textContent = msg;
    $('retry').classList.remove('hidden');
  }

  // ---- boot -------------------------------------------------------------

  async function boot() {
    state.tagID = location.pathname.split('/').filter(Boolean).pop() || '';
    const secret = readFragment();

    if (!secret) {
      $('loading').classList.add('hidden');
      $('noSecretTag').textContent = state.tagID;
      $('noSecret').classList.remove('hidden');
      return;
    }
    state.secret = secret;
    state.tagKey = tagKeyFrom(secret);

    // Kicked off now, in parallel with the tag fetch, rather than waited on
    // at the moment of submission: "there is no wallet here" used to surface
    // only after a finder had already filled in every field, which is the
    // single worst place to learn it. availability() never rejects on its
    // own, but the catch is cheap insurance against a provider that does.
    const walletCheck = window.Wallet.available().catch(() => false);
    const pending = readPendingReceipt(state.tagID);

    try {
      state.info = await api(`/api/tag/${encodeURIComponent(state.tagID)}`);
    } catch (e) {
      $('loading').innerHTML = `<h2>Tag not found</h2><p class="err">${e.message}</p>`;
      return;
    }

    // The species profile decides what the form asks for. It is loaded after
    // the tag rather than before, because which profile applies is the tag's
    // business: a report naming a different species is refused by the server.
    try {
      await window.Schema.load();
      state.profile = window.Schema.profile(state.info.provenance.species);
    } catch (e) {
      $('loading').innerHTML =
        `<h2>Cannot read the field guide</h2><p class="err">${e.message}</p>` +
        `<p class="note">This page needs the list of what to record for this species, ` +
        `and could not reach the server or find a cached copy.</p>`;
      return;
    }

    state.walletAvailable = await walletCheck;

    $('loading').classList.add('hidden');
    // Naming is offered only when nobody has done it, and only while the tag is
    // still claimable -- offering it on a retired tag would be a dead end.
    state.canName = !state.info.provenance.name && state.info.tag.status === 'active';
    if (state.canName) $('nameField').classList.remove('hidden');
    buildForm();
    renderTag();
    renderProvenance(state.info.provenance);

    // A previous visit got as far as broadcasting the payment and never
    // finished handing it to the wallet. Resume there directly: showing the
    // form again would let the tag be "reported" a second time, which the
    // server will now refuse since it is no longer active, and that refusal
    // would read as a failure to someone who has, in fact, already been paid.
    if (pending) {
      state.receipt = pending.receipt;
      state.pendingBonusSats = pending.bonusSats;
      $('form').classList.add('hidden');
      $('pay').classList.remove('hidden');
      attemptRedeem();
    }
  }

  // buildForm renders the profile's fields and wires them up.
  function buildForm() {
    window.Schema.renderFields(state.profile, $('fields'), { tagging: false });
    // Every vocab select schema.js just rendered, as a searchable popover
    // with an icon per entry where one is defined (see combobox.js).
    window.Combobox.enhanceAll($('fields'));
    for (const el of $('fields').querySelectorAll('input, select')) {
      el.addEventListener(el.tagName === 'SELECT' ? 'change' : 'input', checkRules);
    }
    checkRules();
  }

  function renderTag() {
    const tag = state.info.tag;
    $('tagDisplay').textContent = tag.display;
    $('tagState').classList.remove('hidden');

    const status = $('tagStatus');
    switch (tag.status) {
      case 'active':
        status.innerHTML =
          `<p>This tag is live. Reporting it pays <strong>${sats(state.info.base_satoshis)}</strong>` +
          `, and putting the ${animal()} back with the tag on is worth ` +
          `<strong>${sats(state.info.bonus_satoshis)}</strong> more.</p>` +
          (state.walletAvailable ? '' : noWalletBannerHTML());
        $('form').classList.remove('hidden');
        break;
      case 'cooldown':
        status.innerHTML =
          `<div class="banner warn">This tag was reported recently and is waiting for SCDNR to put it back in service. ` +
          `Its history is below.</div>`;
        break;
      case 'redeeming':
        status.innerHTML =
          `<div class="banner warn">Somebody is redeeming this tag right now. If that was you and it stalled, ` +
          `wait a minute and reload.</div>`;
        break;
      case 'minted':
        status.innerHTML =
          `<div class="banner">This tag has not been put on an animal yet, so there is nothing to claim.</div>`;
        break;
      default:
        status.innerHTML =
          `<div class="banner">This tag has been retired. Its history is below.</div>`;
    }
  }

  // noWalletBannerHTML is shown up front, above the form, rather than
  // waiting until submit to say so -- which is where this notice used to
  // live, as a single line inside redeem()'s own thrown error, discovered
  // only after a finder had already filled in every field. It does not
  // block filling the form in now: a finder might open a compatible wallet
  // mid-visit, or share the link to finish on another device, and either way
  // redeem() checks again for real at the moment it matters.
  function noWalletBannerHTML() {
    const title = t('noWalletTitle', 'No compatible wallet detected on this device.');
    const body = t(
      'noWalletBody',
      'Collecting the reward needs a BRC-100 wallet open in this browser tab -- the BSV Browser app ' +
        'on a phone, or a desktop wallet running locally. You can still fill this in now; it will be ' +
        'checked again when you submit.'
    );
    return `<div class="banner warn"><strong>${title}</strong> ${body}</div>`;
  }

  // ---- the form ---------------------------------------------------------

  function wireForm() {
    $('locate').addEventListener('click', locate);
    $('form').addEventListener('submit', onSubmit);
    $('animalname').addEventListener('input', checkRules);
    $('share').addEventListener('click', share);
    // What "Try again" does depends on why the last attempt stopped -- see
    // fail(). A real failure sends a finder back to the form, in case
    // something needs fixing; a dropped signal has nothing to fix, so it
    // just retries the network call directly.
    $('retry').addEventListener('click', () => {
      $('payErr').textContent = '';
      $('retry').classList.add('hidden');
      state.pendingRetry = false;
      if (state.retryGoesToForm) {
        $('pay').classList.add('hidden');
        $('form').classList.remove('hidden');
      } else {
        attemptRedeem();
      }
    });
    // The marsh case: signal comes back on its own, and a finder should not
    // have to notice and tap a button for that -- see admin.js's identical
    // reasoning for its own offline queue.
    window.addEventListener('online', () => {
      if (!state.pendingRetry) return;
      state.pendingRetry = false;
      $('payErr').textContent = '';
      $('retry').classList.add('hidden');
      attemptRedeem();
    });
  }

  // animal is what to call the thing in a sentence.
  const animal = () => (state.profile ? state.profile.common.toLowerCase() : 'animal');

  // checkRules evaluates the profile's own rules in the page, so a finder
  // learns before they submit rather than after. The server enforces them
  // regardless; this is courtesy, not security.
  //
  // The rules are the profile's, not this file's. Before the schema endpoint
  // existed, "females carrying eggs" and "under 127 mm" were written out here
  // in JavaScript and again in Go, and the two could drift -- which would mean
  // telling somebody they may keep an animal the server then refuses, after
  // they have already killed it.
  function checkRules() {
    if (!state.profile) return;
    const { meas, attr } = window.Schema.read($('fields'));
    const dispKey = window.Schema.dispositionKey();
    const banner = $('mustRelease');

    const rule = window.Schema.mustRelease(state.profile, meas, attr);
    const disp = $(`f_${dispKey}`);
    if (rule && disp) {
      // Only mutate when the message actually changes: this runs on every
      // keystroke while a must-release value is entered, and mustRelease is
      // a live region (see redeem.html) -- re-writing an identical string on
      // every input event would still count as a mutation and risks a
      // screen reader re-announcing it once per character typed.
      const msg = `${capitalise(rule.reason)}. Select "put it back" to continue.`;
      if (banner.textContent !== msg) banner.textContent = msg;
      banner.classList.remove('hidden');
      disp.value = 'RELEASED';
      const kept = disp.querySelector('option[value="HARVESTED"]');
      if (kept) kept.disabled = true;
    } else {
      banner.classList.add('hidden');
      const kept = disp && disp.querySelector('option[value="HARVESTED"]');
      if (kept) kept.disabled = false;
    }

    const note = $('fields').querySelector('[data-disp-note]');
    if (note) {
      note.textContent =
        (disp && disp.value) === 'RELEASED'
          ? `The bonus is held and paid to you if this ${animal()} is caught again, ` +
            'which is what confirms you really put it back.'
          : 'Keeping it pays the base reward and retires the tag.';
    }

    $('submit').disabled = !(state.fix && window.Schema.complete(state.profile, meas, attr, { tagging: false }));
  }

  const capitalise = (s) => (s ? s.charAt(0).toUpperCase() + s.slice(1) : s);

  function locate() {
    const box = $('fix');
    // #fix carries data-i18n="waitingFix" in the markup for its initial
    // "waiting for a position fix" placeholder (see i18n.js). Every branch
    // below replaces that placeholder with something real -- a spinner, an
    // actual fix, or an error -- and none of that is the translatable
    // placeholder string anymore, so the attribute comes off here. Left in
    // place, switching language after a fix arrives would call I18n.apply()
    // and silently overwrite real coordinates back to "waiting for a fix".
    box.removeAttribute('data-i18n');
    if (!navigator.geolocation) {
      box.textContent = 'This browser will not share a location.';
      box.className = 'banner bad';
      return;
    }
    box.innerHTML = '<span class="spin"></span> Getting a fix…';
    box.className = 'banner';

    navigator.geolocation.getCurrentPosition(
      (pos) => {
        state.fix = {
          lat: pos.coords.latitude,
          lon: pos.coords.longitude,
          acc: pos.coords.accuracy || 0,
        };
        box.innerHTML =
          `${state.fix.lat.toFixed(5)}, ${state.fix.lon.toFixed(5)} ` +
          `<span class="note">(&plusmn;${Math.round(state.fix.acc)} m)</span>`;
        box.className = 'banner good';
        // Now that we know where they are, put it on the map.
        if (state.info && state.info.provenance) renderMap(state.info.provenance);
        checkRules();
      },
      (err) => {
        box.textContent =
          err.code === err.PERMISSION_DENIED
            ? 'Location permission was refused. SCDNR needs the position to make this report useful.'
            : `Could not get a fix: ${err.message}`;
        box.className = 'banner bad';
      },
      { enableHighAccuracy: true, timeout: 20000, maximumAge: 0 }
    );
  }

  function formValues() {
    const { meas, attr } = window.Schema.read($('fields'));
    return {
      name: state.canName ? $('animalname').value.trim() : '',
      tag_id: state.tagID,
      lat: state.fix.lat,
      lon: state.fix.lon,
      accuracy_m: state.fix.acc,
      meas,
      attr,
    };
  }

  async function onSubmit(ev) {
    ev.preventDefault();
    $('formErr').textContent = '';
    $('form').classList.add('hidden');
    $('pay').classList.remove('hidden');
    $('payErr').textContent = '';
    $('retry').classList.add('hidden');
    await attemptRedeem();
  }

  // attemptRedeem is the one place redeem() is ever called from, so a
  // dropped signal and a real failure are always sorted the same way
  // regardless of whether this is the first attempt, a tap on "Try again",
  // or an automatic retry once the network comes back.
  async function attemptRedeem() {
    try {
      await redeem();
    } catch (e) {
      const network = isNetworkError(e);
      state.retryGoesToForm = !network;
      state.pendingRetry = network;
      fail(
        network
          ? t(
              'networkRetry',
              'No signal right now. Nothing has been lost -- this will retry automatically the moment you have one, or tap Try again.'
            )
          : e.message
      );
    }
  }

  // ---- the redemption itself -------------------------------------------

  async function redeem() {
    if (!(await window.Wallet.available())) {
      throw new Error(
        t('noWalletSubmitError', 'No BSV wallet found on this device. Open this page in BSV Browser to collect the reward.')
      );
    }

    // A previous attempt on this device already got as far as step 5 below
    // and only failed handing the payment to the wallet -- a local step, not
    // a network one. The reward has already moved at that point (see
    // persistPendingReceipt's comment), so resume there directly rather than
    // repeating steps that would now fail against an already-redeemed tag.
    if (state.receipt) {
      step('quote', 'done');
      step('attest', 'done');
      step('build', 'done');
      step('verify', 'done');
      step('sign', 'done');
      step('receive', 'doing');
      await internalizeReceipt(state.receipt);
      step('receive', 'done');
      clearPendingReceipt();
      showPaid(state.receipt, state.pendingBonusSats);
      return;
    }

    // 1. What are we owed, and what exactly does the wallet sign?
    step('quote', 'doing');
    state.identityKey = await window.Wallet.identityKey();
    const form = { ...formValues(), payee: state.identityKey };
    const quote = await api('/api/redeem/quote', form);
    state.quote = quote;
    state.canName = !!quote.can_name;
    // Attest under the canonical id the server returned, never a form of the
    // id this page happened to parse out of a URL.
    const canonicalTagID = quote.tag_id || state.tagID;
    step('quote', 'done');

    // 2. The finder attests to their own report. This is what puts their name
    //    on the record rather than the server's.
    step('attest', 'doing');
    // Hand the wallet the payload itself, NOT a hash of it. BRC-100's
    // createSignature applies SHA-256 to `data` before signing, so passing a
    // digest here would sign sha256(sha256(payload)) while the server verifies
    // against sha256(payload) -- a mismatch that surfaces only as "attestation
    // signature does not verify", with both halves looking correct in
    // isolation. Use hashToDirectlySign if a pre-computed hash is ever needed.
    const payloadBytes = hexToBytes(quote.observation);
    const attestSig = await window.Wallet.createSignature({
      protocolID: ATTEST_PROTOCOL,
      keyID: canonicalTagID,
      // 'anyone', not 'self'. The wallet derives a BRC-42 child either way and
      // signs with that, never with the identity key -- but only the anyone
      // derivation can be reproduced by a third party from the published
      // identity key, which is what makes the record's attribution checkable
      // by anybody reading the dataset. Under 'self' nobody but the signer
      // could ever verify their own attestation.
      counterparty: 'anyone',
      data: Array.from(payloadBytes),
    });
    step('attest', 'done');

    // 3. Ask the server to build the payment.
    step('build', 'doing');
    const draft = await api('/api/redeem/prepare', {
      ...form,
      observation: quote.observation,
      attest_sig: bytesToHex(new Uint8Array(attestSig)),
      attest_pub: state.identityKey,
    });
    step('build', 'done');

    // 4. Check it really pays us, before signing anything.
    step('verify', 'doing');
    const tx = Transaction.fromHexBEEF(draft.signable_tx);
    await verifyPayout(tx, draft);
    step('verify', 'done');

    // 5. Unlock the tag with the key printed on it, and broadcast. Once this
    //    call returns, the payment has moved on chain no matter what happens
    //    next -- so the receipt is cached, in memory and on disk, before the
    //    wallet handoff below is even attempted. See persistPendingReceipt.
    step('sign', 'doing');
    const tagSig = signTagInput(tx, draft.input_index);
    const receipt = await api('/api/redeem/complete', {
      reference: draft.reference,
      tag_sig: bytesToHex(tagSig),
    });
    state.receipt = receipt;
    state.pendingBonusSats = quote.bonus_satoshis;
    persistPendingReceipt(canonicalTagID, receipt, quote.bonus_satoshis);
    step('sign', 'done');

    // 6. Hand the payment to the finder's wallet so it shows in their balance.
    step('receive', 'doing');
    await internalizeReceipt(receipt);
    step('receive', 'done');
    clearPendingReceipt();

    showPaid(receipt, quote.bonus_satoshis);
  }

  // internalizeReceipt is the one place step 6 happens, so both the normal
  // path and the resume-after-interruption path above hand the payment to
  // the wallet exactly the same way.
  async function internalizeReceipt(receipt) {
    await window.Wallet.internalizeAction({
      tx: hexToBytes(receipt.atomic_beef),
      outputIndex: receipt.payout_index,
      derivationPrefix: receipt.derivation_prefix,
      derivationSuffix: receipt.derivation_suffix,
      senderIdentityKey: receipt.sender_identity_key,
      description: `SCDNR wildlife tag ${state.tagID}`,
    });
  }

  // verifyPayout is the reason this page signs in the browser at all.
  //
  // The server built this transaction and could, in principle, have built one
  // that pays somebody else. So before the tag key touches it, the page asks
  // the finder's OWN wallet to derive the key that output zero should be
  // locked to, rebuilds the expected P2PKH script from it, and compares. No
  // private key of the finder's ever enters the page; the wallet does the
  // derivation and hands back a public key.
  //
  // Without this step, in-browser signing would be theatre.
  async function verifyPayout(tx, draft) {
    const derivedHex = await window.Wallet.derivePublicKey({
      protocolID: BRC29_PROTOCOL,
      keyID: `${draft.derivation_prefix} ${draft.derivation_suffix}`,
      counterparty: draft.sender_identity_key,
      forSelf: true,
    });

    // fromString, not the constructor: `new PublicKey(hex)` reads the string as
    // an x-coordinate, not as a DER-encoded key, and yields a different (and
    // wrong) hash without complaining.
    const expected = new Uint8Array(PublicKey.fromString(derivedHex).toHash());
    const out = tx.outputs[draft.payout_index];
    if (!out) throw new Error('the payment has no output where the reward should be');

    const script = out.lockingScript.chunks;
    // OP_DUP OP_HASH160 <20 bytes> OP_EQUALVERIFY OP_CHECKSIG
    const looksRight =
      script.length === 5 &&
      script[0].op === 0x76 &&
      script[1].op === 0xa9 &&
      script[3].op === 0x88 &&
      script[4].op === 0xac &&
      script[2].data &&
      script[2].data.length === 20;
    if (!looksRight) {
      throw new Error('the payment output is not a standard payment; refusing to sign');
    }
    if (bytesToHex(new Uint8Array(script[2].data)) !== bytesToHex(expected)) {
      throw new Error(
        'the payment does not go to your wallet. Nothing has been signed and no money has moved. ' +
          'Do not retry: report this.'
      );
    }
    if (Number(out.satoshis) !== Number(draft.payout_satoshis)) {
      throw new Error(
        `the payment is ${sats(out.satoshis)}, not the ${sats(draft.payout_satoshis)} you were quoted. Refusing to sign.`
      );
    }
  }

  // signTagInput signs with the key printed on the tag.
  //
  // SIGHASH_ALL over everything, so the report and the payment cannot be
  // separated afterwards: altering either invalidates this signature.
  //
  // The two-step hashing is the library's contract, not a mistake. format()
  // returns the BIP-143 preimage; sha256 is applied here and PrivateKey.sign
  // applies the second internally, which together give Bitcoin's double
  // SHA-256. This mirrors what @bsv/sdk's own P2PKH template does, and is the
  // only combination that produces a signature the Go interpreter accepts.
  function signTagInput(tx, inputIndex) {
    const scope = TransactionSignature.SIGHASH_ALL | TransactionSignature.SIGHASH_FORKID;
    const input = tx.inputs[inputIndex];
    const source = input.sourceTransaction.outputs[input.sourceOutputIndex];

    const preimage = TransactionSignature.format({
      sourceTXID: input.sourceTXID ?? input.sourceTransaction.id('hex'),
      sourceOutputIndex: input.sourceOutputIndex,
      sourceSatoshis: source.satoshis,
      transactionVersion: tx.version,
      otherInputs: tx.inputs.filter((_, i) => i !== inputIndex),
      outputs: tx.outputs,
      inputIndex,
      subscript: source.lockingScript,
      inputSequence: input.sequence,
      lockTime: tx.lockTime,
      scope,
    });

    const raw = state.tagKey.sign(Hash.sha256(preimage));
    // toChecksigFormat appends the sighash byte; doing it by hand is a classic
    // off-by-one that only shows up as a script failure at broadcast.
    return new Uint8Array(new TransactionSignature(raw.r, raw.s, scope).toChecksigFormat());
  }

  // showPaid takes bonusSats explicitly rather than reading state.quote
  // itself, because a payment resumed after a reload (see redeem()'s
  // state.receipt branch) never ran the quote step this session -- the
  // number has to come from wherever the caller actually has it.
  function showPaid(receipt, bonusSats) {
    $('pay').classList.add('hidden');
    $('paid').classList.remove('hidden');
    $('paidAmount').textContent = sats(receipt.payout_satoshis);
    const named = state.canName && $('animalname').value.trim();
    if (named) {
      $('animalName').textContent = named;
      $('animalName').classList.remove('unnamed');
      $('animalSub').textContent = `tag ${state.tagID} · named by you, permanently`;
    }
    $('paidNote').textContent = receipt.retired
      ? 'This tag is now retired. Thank you for reporting it.'
      : bonusSats != null
        ? `Your ${sats(bonusSats)} bonus is held until this ${animal()} is caught again. ` +
          'If it is, you get paid automatically: no need to come back.'
        : `Your bonus is held until this ${animal()} is caught again. If it is, you get paid automatically: no need to come back.`;
    const link = $('paidTx');
    link.textContent = receipt.txid;
    link.href = `${(state.info && state.info.arcade_url) || ''}/tx/${receipt.txid}`;
  }

  // ---- sharing ------------------------------------------------------------
  //
  // This page's whole reason to generate an OG image per tag (see
  // handleRedeemPage) was so a link to it looks like something worth
  // opening when shared -- and until now there was no way to actually share
  // one from here short of copying the address bar by hand. The link shared
  // is this exact page, secret fragment and all: the fragment never reaches
  // the server (see readFragment), so it is what makes the link open this
  // animal's story for whoever it is sent to rather than a dead end asking
  // for a tag code nobody but the finder has.
  async function share() {
    const url = location.href;
    const name = state.info && state.info.provenance && state.info.provenance.name;
    const title = name ? `${name} · SCDNR wildlife tag` : 'SCDNR wildlife tag';
    if (navigator.share) {
      try {
        await navigator.share({ title, url });
      } catch (e) {
        // AbortError is the person closing the share sheet without picking
        // anything -- not a failure worth telling them about.
        if (e.name !== 'AbortError' && window.Toast) window.Toast.error('Could not open the share sheet.');
      }
      return;
    }
    try {
      await navigator.clipboard.writeText(url);
      if (window.Toast) window.Toast.success('Link copied.');
    } catch (e) {
      if (window.Toast) window.Toast.error('Could not copy the link.');
    }
  }

  // ---- the animal's story ----------------------------------------------

  // Esri's World Gray Canvas rather than stock OSM raster tiles: the same
  // coastline and road network, drawn as a near-monochrome canvas instead of
  // OSM's saturated road-orange and park-green, so it sits under this app's
  // palette instead of fighting it. (CARTO's equivalent free basemaps now
  // gate behind an API key; this one still doesn't, as of this writing.) The
  // base layer carries no labels -- "Reference" is a second, transparent
  // overlay of place names and roads, added on top so it can be swapped or
  // dropped independently of the canvas underneath. A real deployment should
  // still point this at its own tile server rather than lean on donated
  // infrastructure.
  const TILE_URL = {
    light: { base: 'World_Light_Gray_Base', ref: 'World_Light_Gray_Reference' },
    dark: { base: 'World_Dark_Gray_Base', ref: 'World_Dark_Gray_Reference' },
  };
  const tileURL = (layer) => `https://server.arcgisonline.com/ArcGIS/rest/services/Canvas/${layer}/MapServer/tile/{z}/{y}/{x}`;
  const TILE_ATTRIB = '&copy; Esri &copy; OpenStreetMap contributors';

  // data-theme can be "system" (see theme.js); the map still has to pick one
  // literal tile set, so this resolves it the same way the toggle icons do.
  function effectiveTheme() {
    const attr = document.documentElement.getAttribute('data-theme');
    if (attr === 'dark') return 'dark';
    if (attr === 'system' && window.matchMedia && window.matchMedia('(prefers-color-scheme: dark)').matches) return 'dark';
    return 'light';
  }

  const fmt = (n) => Number(n).toLocaleString();

  function renderProvenance(p) {
    if (!p || !p.tagged_at) return;
    $('prov').classList.remove('hidden');

    renderName(p);
    renderJourney(p);
    renderTimeline(p);
    renderFacts(p);
    renderMap(p);
  }

  // Same species -> icon/gradient mapping as admin.js's card grid, kept in
  // step by hand since there's no module system here to share it through.
  // Species not in the map (anything a future deployment adds) falls back
  // to a generic family silhouette rather than a broken image.
  const SPECIES_ICON = { CALSAP: 'blue-crab.svg', SCIOCE: 'red-drum.svg' };
  const SPECIES_GRADIENT = { CALSAP: 'estuary', SCIOCE: 'sunset' };
  const FALLBACK_ICON = 'crab-generic.svg';
  const FALLBACK_GRADIENT = 'slate';

  function renderHeroStyle(p) {
    const grad = SPECIES_GRADIENT[p.species] || FALLBACK_GRADIENT;
    const icon = SPECIES_ICON[p.species] || FALLBACK_ICON;
    $('crabHero').style.background = `var(--grad-${grad})`;
    // Absolute, not relative: this page is served at /t/{tagID}, and a
    // relative path set from JS (which the server's static-HTML rewrite
    // step never sees) resolves against that nested URL, not against /.
    $('animalIcon').src = `/vendor/animals/${icon}`;
  }

  function renderName(p) {
    renderHeroStyle(p);
    const el = $('animalName');
    const what = p.common_name || 'animal';
    if (p.name) {
      el.textContent = p.name;
      el.classList.remove('unnamed');
      $('animalSub').textContent =
        `${what} · tag ${p.tag_id} · named by ${shortKey(p.named_by)}`;
    } else {
      el.textContent = `This ${what.toLowerCase()} has no name`;
      el.classList.add('unnamed');
      $('animalSub').textContent = `tag ${p.tag_id} · nobody has named it yet`;
    }
  }

  // The two numbers a finder actually wants, plus growth when it is interesting.
  function renderJourney(p) {
    const km = p.distance_m / 1000;
    const tiles = [
      {
        hero: true,
        n: fmt(p.days_at_large),
        u: p.days_at_large === 1 ? 'day' : 'days',
        k: 'carrying this tag',
      },
      { n: km < 1 ? fmt(p.distance_m) : km.toFixed(1), u: km < 1 ? 'metres' : 'km', k: 'from where it started' },
      { n: String((p.recaptures || []).length + 1), u: 'sightings', k: 'including this one' },
    ];
    // Total path only differs once an animal has been found more than once, and
    // saying so twice would be noise.
    if (p.total_path_m > p.distance_m + 50) {
      tiles.push({ n: (p.total_path_m / 1000).toFixed(1), u: 'km', k: 'total known journey' });
    }
    $('journey').innerHTML = tiles
      .map((t) => `<div class="jstat${t.hero ? ' hero' : ''}"><div class="n">${t.n}</div>` +
                  `<div class="u">${t.u}</div><div class="k">${t.k}</div></div>`)
      .join('');
  }

  function renderTimeline(p) {
    const items = [];
    items.push({
      cls: 'tagged',
      when: dateOf(p.tagged_at),
      what: 'Tagged by a DNR biologist',
      // The facts are the profile's own fields, already labelled and rendered
      // by the server. This page does not know what a carapace width is.
      detail: (p.facts || []).map((f) => `${f.label}: ${f.value}`).join(' · '),
    });
    for (const r of p.recaptures || []) {
      items.push({
        cls: 'caught',
        when: dateOf(r.at),
        what: r.disposition === 'RELEASED' ? 'Caught and put back' : 'Caught and kept',
        detail:
          `${size(p, r.primary)} · ${(r.distance_m / 1000).toFixed(1)} km out · ` +
          `day ${r.days_at_large}` + (r.proven ? ' · proven on chain' : ' · awaiting proof'),
      });
    }
    items.push({ cls: 'now', when: 'today', what: 'You found it', detail: 'Report it below to get paid' });

    $('timeline').innerHTML = '<ul class="tl">' + items.map((i) =>
      `<li class="${i.cls}"><div class="when">${i.when}</div>` +
      `<div class="what">${escapeHTML(i.what)}</div>` +
      `<div class="detail">${escapeHTML(i.detail)}</div></li>`).join('') + '</ul>';
  }

  // size renders the profile's primary measurement, whatever it happens to be.
  function size(p, value) {
    if (!value) return 'not measured';
    const n = p.primary_scale > 1 ? (value / p.primary_scale).toFixed(2) : value;
    return `${n} ${p.primary_unit}`;
  }

  // Observations that turn the numbers into something worth telling someone.
  function renderFacts(p) {
    const facts = [];
    const km = p.distance_m / 1000;
    const days = p.days_at_large;
    const what = (p.common_name || 'animal').toLowerCase();

    if (days > 0 && km > 0.1) {
      const perDay = (p.distance_m / days).toFixed(0);
      facts.push(`It has averaged <b>${fmt(perDay)} m a day</b> since it was tagged, ` +
                 `though a straight line between two sightings says nothing about the route it took.`);
    }
    if ((p.recaptures || []).length > 0) {
      if (p.growth && p.growth_expected) {
        facts.push(`It has grown <b>${size(p, p.growth)}</b> since tagging, which is exactly what ` +
                   `a tagging programme is for: nobody can measure the same wild animal twice any other way.`);
      } else if (p.growth) {
        facts.push(`It has grown <b>${size(p, p.growth)}</b> since tagging. That is unusual and worth a ` +
                   `second look: a ${what} grows only by moulting, and it sheds the tag when it does.`);
      } else if (!p.growth_expected) {
        facts.push(`It has not moulted since it was tagged: a ${what} sheds its shell and everything ` +
                   `attached to it, so a tag still on the animal means the same shell it was wearing on day one.`);
      }
    }
    if (days >= 365) {
      facts.push(`Over a year at large, still carrying the same tag.`);
    }
    if ((p.recaptures || []).length >= 1) {
      facts.push(`Reported <b>${p.recaptures.length + 1} times</b>. Every extra sighting is worth more to the ` +
                 `study than the first, because it turns two points into a track.`);
    }
    if (p.scientific_name) {
      facts.push(`Recorded as <b>${escapeHTML(p.common_name)}</b> (<i>${escapeHTML(p.scientific_name)}</i>), ` +
                 `which is written into the signed record rather than inferred later.`);
    }
    facts.push(`Tagged at <b>${p.tagged_lat.toFixed(4)}, ${p.tagged_lon.toFixed(4)}</b> and recorded on chain ` +
               `the same day: that part cannot be edited by anyone, including us.`);

    $('funFacts').innerHTML = facts.map((f) => `<div class="fact">${f}</div>`).join('');
  }

  // ---- the map ----------------------------------------------------------

  function points(p) {
    const pts = [{ lat: p.tagged_lat, lon: p.tagged_lon, kind: 'tagged', label: 'Tagged here' }];
    for (const r of p.recaptures || []) {
      pts.push({ lat: r.lat, lon: r.lon, kind: 'caught', label: 'Found here' });
    }
    if (state.fix) {
      pts.push({ lat: state.fix.lat, lon: state.fix.lon, kind: 'caught', label: 'You are here' });
    }
    return pts;
  }

  // Tiles are the one live network dependency on this page, and the page is
  // opened in marshes. If Leaflet is missing or the tiles never arrive, fall
  // back to a hand-drawn track that needs nothing but the coordinates.
  function renderMap(p) {
    const pts = points(p);
    if (!window.L) {
      drawTrack(pts);
      return;
    }
    try {
      const map = L.map('map', { attributionControl: false, scrollWheelZoom: false });
      const layer = TILE_URL[effectiveTheme()];
      L.tileLayer(tileURL(layer.base), { maxZoom: 18, crossOrigin: true }).addTo(map);
      L.tileLayer(tileURL(layer.ref), { maxZoom: 18, crossOrigin: true }).addTo(map);
      $('mapAttrib').innerHTML = ' · ' + TILE_ATTRIB;

      const latlngs = pts.map((q) => [q.lat, q.lon]);
      for (const q of pts) {
        L.marker([q.lat, q.lon], {
          icon: L.divIcon({ className: '', html: `<div class="pin ${q.kind}"></div>`, iconSize: [18, 18], iconAnchor: [9, 9] }),
        }).addTo(map).bindPopup(q.label);
      }
      if (latlngs.length > 1) {
        L.polyline(latlngs, { color: '#93a8b3', weight: 2, dashArray: '6 5' }).addTo(map);
        map.fitBounds(L.latLngBounds(latlngs).pad(0.35));
      } else {
        map.setView(latlngs[0], 13);
      }
      state.map = map;
    } catch (e) {
      drawTrack(pts);
    }
  }

  // A hand-drawn track: no tiles, no network, just the shape of the journey.
  function drawTrack(pts) {
    $('map').classList.add('hidden');
    $('track').classList.remove('hidden');
    $('mapAttrib').textContent = ' · map unavailable, showing the track only';

    if (pts.length < 2) {
      $('track').innerHTML =
        `<title id="trackTitle">Where this animal was tagged</title>` +
        `<circle cx="300" cy="130" r="7" fill="var(--accent)"/>`;
      return;
    }
    const lats = pts.map((q) => q.lat), lons = pts.map((q) => q.lon);
    const pad = 0.2;
    const spanLat = Math.max(Math.max(...lats) - Math.min(...lats), 0.002);
    const spanLon = Math.max(Math.max(...lons) - Math.min(...lons), 0.002);
    const minLat = Math.min(...lats) - spanLat * pad, minLon = Math.min(...lons) - spanLon * pad;
    const w = spanLon * (1 + 2 * pad), h = spanLat * (1 + 2 * pad);
    const x = (lon) => ((lon - minLon) / w) * 600;
    const y = (lat) => 260 - ((lat - minLat) / h) * 260;

    const path = pts.map((q, i) => `${i ? 'L' : 'M'}${x(q.lon).toFixed(1)},${y(q.lat).toFixed(1)}`).join(' ');
    const dots = pts.map((q) =>
      `<circle cx="${x(q.lon).toFixed(1)}" cy="${y(q.lat).toFixed(1)}" r="7" ` +
      `fill="${q.kind === 'tagged' ? 'var(--accent)' : 'var(--warn)'}" stroke="var(--panel)" stroke-width="2"/>`
    ).join('');

    $('track').innerHTML =
      `<title id="trackTitle">Track from where this animal was tagged to where it was found</title>` +
      `<path d="${path}" fill="none" stroke="var(--ink-dim)" stroke-width="2" stroke-dasharray="6 5"/>` + dots;
  }

  function dateOf(iso) {
    try {
      return new Date(iso).toLocaleDateString(undefined, { dateStyle: 'medium' });
    } catch (_) {
      return iso;
    }
  }
  // Truncated in the middle, not at the end -- see admin.js's identical
  // midTruncate for why: the tail of a key is the part a person actually
  // checks against, and cutting it off throws away the part that matters.
  const shortKey = (k) => (k && k.length > 16 ? `${k.slice(0, 8)}…${k.slice(-6)}` : k || 'a biologist');
  function escapeHTML(s) {
    return String(s).replace(/[&<>"']/g, (c) =>
      ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c]));
  }

  // ---- dev-mode mocks, see devpanel.js -----------------------------------
  //
  // Every scenario below drives the exact same render functions the real
  // boot() flow uses, with fabricated data instead of a fetch -- so a mock
  // screen is pixel-for-pixel what the real one would look like, and a CSS
  // change that fixes one fixes the other.

  const CHARLESTON = { lat: 32.7765, lon: -79.9311 };

  function resetPanels() {
    ['loading', 'noSecret', 'tagState', 'form', 'pay', 'paid'].forEach((id) => $(id).classList.add('hidden'));
    $('formErr').textContent = '';
    $('payErr').textContent = '';
    $('retry').classList.add('hidden');
    document.querySelectorAll('#steps li').forEach((li) => li.classList.remove('doing', 'done'));
    state.receipt = null;
    state.pendingBonusSats = null;
    state.retryGoesToForm = true;
    state.pendingRetry = false;
    state.walletAvailable = true;
  }

  // baseProvenance is a crab tagged 214 days ago near Charleston, with no
  // recaptures yet -- the shape every "active" tag starts in.
  function baseProvenance(overrides) {
    return Object.assign(
      {
        tag_id: 'K2M9Q7C',
        species: 'CALSAP',
        common_name: 'Blue crab',
        scientific_name: 'Callinectes sapidus',
        name: '',
        named_by: '',
        tagged_at: new Date(Date.now() - 214 * 86400000).toISOString(),
        tagged_lat: CHARLESTON.lat,
        tagged_lon: CHARLESTON.lon,
        days_at_large: 214,
        distance_m: 0,
        total_path_m: 0,
        recaptures: [],
        growth: 0,
        growth_expected: true,
        primary_scale: 1,
        primary_unit: 'mm',
        // facts is filled in by mockBoot() from whatever species schema is
        // actually loaded -- never hardcoded here, see schemaFacts() below.
        facts: [],
      },
      overrides || {}
    );
  }

  // schemaFacts builds a plausible tagging-time fact list purely from the
  // loaded profile's own field labels, the same way the server would for a
  // real record. Hardcoding a species' field names here would be exactly the
  // bug TestSchemaDrivesTheForms exists to catch.
  function schemaFacts(profile) {
    if (!profile) return [];
    const facts = [];
    for (const m of (profile.measures || []).slice(0, 2)) {
      const mid = Math.round((m.min + m.max) / 2);
      const shown = m.scale > 1 ? (mid / m.scale).toFixed(2) : mid;
      facts.push({ label: m.label, value: `${shown} ${m.unit}` });
    }
    const v = (profile.vocabs || [])[0];
    const val = v && (v.values || [])[0];
    if (v && val) facts.push({ label: v.label, value: val.label });
    return facts;
  }

  // richProvenance is the same crab two sightings later: named, moved, grown.
  function richProvenance() {
    return baseProvenance({
      name: 'Old Bertha',
      named_by: '03a1b2c3d4e5f60718293a4b5c6d7e8f90123456',
      days_at_large: 214,
      distance_m: 6400,
      total_path_m: 9100,
      growth: 24,
      recaptures: [
        {
          at: new Date(Date.now() - 140 * 86400000).toISOString(),
          disposition: 'RELEASED',
          primary: 148,
          distance_m: 4200,
          days_at_large: 74,
          proven: true,
          lat: CHARLESTON.lat + 0.06,
          lon: CHARLESTON.lon + 0.04,
        },
        {
          at: new Date(Date.now() - 30 * 86400000).toISOString(),
          disposition: 'RELEASED',
          primary: 156,
          distance_m: 6400,
          days_at_large: 184,
          proven: true,
          lat: CHARLESTON.lat + 0.1,
          lon: CHARLESTON.lon + 0.09,
        },
      ],
    });
  }

  // mockBoot mirrors boot()'s tail: the part that runs once the tag, the
  // schema, and the provenance are all known.
  async function mockBoot(status, provenance, displayTag) {
    resetPanels();
    await window.Schema.load();
    state.profile = window.Schema.profile();
    if (provenance) provenance.facts = schemaFacts(state.profile);
    state.tagID = (provenance && provenance.tag_id) || 'K2M9Q7C';
    state.info = {
      tag: { display: displayTag || 'K2M-9Q7', status },
      provenance: provenance || {},
      base_satoshis: 5000,
      bonus_satoshis: 15000,
      arcade_url: 'https://arcade-v2-tstn-us-1.bsvblockchain.tech',
    };
    state.canName = !!provenance && !provenance.name && status === 'active';
    $('nameField').classList.toggle('hidden', !state.canName);
    buildForm();
    renderTag();
    if (provenance && provenance.tagged_at) renderProvenance(provenance);
    else $('prov').classList.add('hidden');
  }

  window.DevMocks = window.DevMocks || {};
  window.DevMocks.redeem = {
    'Missing tag code': () => {
      resetPanels();
      $('noSecretTag').textContent = 'K2M-9Q7';
      $('noSecret').classList.remove('hidden');
    },
    'Tag not found': () => {
      resetPanels();
      $('loading').classList.remove('hidden');
      $('loading').innerHTML = '<h2>Tag not found</h2><p class="err">tag K2M9Q7C: no such tag</p>';
    },
    'Active: first sighting, unnamed': () => mockBoot('active', baseProvenance()),
    'Active: with history, named': () => mockBoot('active', richProvenance()),
    'Cooldown': () => mockBoot('cooldown', richProvenance()),
    'Redeeming (claim in progress)': () => mockBoot('redeeming', richProvenance()),
    'Minted, never armed': () => mockBoot('minted', null),
    'Retired': () => mockBoot('retired', richProvenance()),
    'Payment: in progress': async () => {
      await mockBoot('active', baseProvenance());
      $('form').classList.add('hidden');
      $('pay').classList.remove('hidden');
      step('quote', 'done');
      step('attest', 'done');
      step('build', 'doing');
    },
    'Payment: failed to verify': async () => {
      await mockBoot('active', baseProvenance());
      $('form').classList.add('hidden');
      $('pay').classList.remove('hidden');
      step('quote', 'done');
      step('attest', 'done');
      step('build', 'done');
      step('verify', 'doing');
      fail('the payment does not go to your wallet. Nothing has been signed and no money has moved. Do not retry: report this.');
    },
    'Paid!': async () => {
      await mockBoot('active', richProvenance());
      // showPaid() only touches #pay/#paid, exactly like the real flow where
      // onSubmit() has already hidden #form by the time it runs.
      $('form').classList.add('hidden');
      showPaid(
        {
          payout_satoshis: 5000,
          retired: false,
          txid: '4f3c9e8a1b2d5f60718293a4b5c6d7e8f9012345678901234567890abcdef01',
        },
        15000
      );
    },
    'No wallet on this device': async () => {
      await mockBoot('active', baseProvenance());
      state.walletAvailable = false;
      renderTag();
    },
    'Payment: dropped signal (auto-retries)': async () => {
      await mockBoot('active', baseProvenance());
      $('form').classList.add('hidden');
      $('pay').classList.remove('hidden');
      step('quote', 'done');
      step('attest', 'done');
      step('build', 'doing');
      state.retryGoesToForm = false;
      state.pendingRetry = true;
      fail('No signal right now. Nothing has been lost -- this will retry automatically the moment you have one, or tap Try again.');
    },
  };

  document.addEventListener('DOMContentLoaded', () => {
    wireForm();
    boot();
  });
})();
