// The species schema, and the forms built from it.
//
// Every measurement, vocabulary and legal rule in this application is data
// served by GET /api/schema, not code. Before that endpoint existed the blue
// crab's sex codes, moult stages, five-inch minimum and plausible size range
// were written out in four separate places -- two HTML files and two scripts --
// and adding a species meant finding all four and a Go file besides.
//
// So this file knows how to turn a profile into a form, read a form back into
// the two maps a record carries, and evaluate the profile's rules the same way
// the server does. It knows nothing about any particular animal.
//
// The schema is cached in localStorage, because a phone in a marsh has to be
// able to render a form without reaching the server. The cached copy is
// revalidated with its ETag whenever there is signal.
(function (root, factory) {
  if (typeof module === 'object' && module.exports) module.exports = factory();
  else root.Schema = factory();
})(typeof self !== 'undefined' ? self : this, function () {
  'use strict';

  const CACHE_KEY = 'wildtag.schema.v2';

  let doc = null;

  // load fetches the schema, falling back to the cached copy.
  //
  // A failure to reach the server is not an error here: the whole point of
  // caching is that a form still renders with no signal. A failure with no
  // cache is, because there is then nothing to ask anybody.
  async function load() {
    if (doc) return doc;

    const cached = readCache();
    try {
      const res = await fetch('/api/schema', {
        headers: cached && cached.etag ? { 'If-None-Match': cached.etag } : undefined,
      });
      if (res.status === 304 && cached) {
        doc = cached.doc;
        return doc;
      }
      if (!res.ok) throw new Error(`schema request failed (${res.status})`);
      doc = await res.json();
      writeCache(res.headers.get('ETag'), doc);
      return doc;
    } catch (err) {
      if (cached) {
        doc = cached.doc;
        return doc;
      }
      throw err;
    }
  }

  function readCache() {
    try {
      const raw = localStorage.getItem(CACHE_KEY);
      return raw ? JSON.parse(raw) : null;
    } catch (_) {
      // A private window, or storage that is full or blocked. Not a reason to
      // fail: it only costs a round trip.
      return null;
    }
  }

  function writeCache(etag, value) {
    try {
      localStorage.setItem(CACHE_KEY, JSON.stringify({ etag, doc: value }));
    } catch (_) {
      /* as above */
    }
  }

  function profiles() {
    return (doc && doc.profiles) || [];
  }

  function profile(code) {
    const want = code || (doc && doc.default);
    return profiles().find((p) => p.code === want) || profiles()[0] || null;
  }

  function dispositionKey() {
    return (doc && doc.disposition_key) || 'disp';
  }

  // ---- rules ------------------------------------------------------------

  // fires mirrors species.Rule.Fires exactly. If these two ever disagree, the
  // page tells somebody they may keep an animal the server then refuses to let
  // them keep -- after they have already killed it.
  function fires(rule, meas, attr) {
    if (rule.measure) {
      const v = meas[rule.measure];
      if (v === undefined || v === null || v === '') return false;
      if (rule.less_than !== undefined && v < rule.less_than) return true;
      if (rule.more_than !== undefined && v > rule.more_than) return true;
      return false;
    }
    if (rule.vocab) {
      const code = attr[rule.vocab];
      if (code === undefined || code === null || code === '') return false;
      if ((rule.in || []).includes(code)) return true;
      if ((rule.not_in || []).length > 0) return !rule.not_in.includes(code);
      return false;
    }
    return false;
  }

  function firstFiring(rules, meas, attr) {
    for (const r of rules || []) {
      if (fires(r, meas, attr)) return r;
    }
    return null;
  }

  const mustRelease = (p, meas, attr) => firstFiring(p.must_release, meas, attr);
  const notTaggable = (p, meas, attr) => firstFiring(p.not_taggable, meas, attr);

  // ---- forms ------------------------------------------------------------

  const esc = (s) =>
    String(s).replace(/[&<>"']/g, (c) =>
      ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c]));

  // unit renders a measure's range in the units a person types, which is not
  // what is stored: a scale of 100 means the field says 15.0 to 40.0 while the
  // record carries 1500 to 4000.
  function bounds(m) {
    const lo = m.scale > 1 ? m.min / m.scale : m.min;
    const hi = m.scale > 1 ? m.max / m.scale : m.max;
    return { lo, hi, stepAttr: m.scale > 1 ? `step="${1 / m.scale}"` : 'step="1"' };
  }

  // renderFields builds the inputs a profile declares.
  //
  // tagging says whether the observer is a tagger. It decides two things: the
  // tagging-only fields are shown and required, and the disposition is not
  // asked for at all -- a tagger is releasing the animal by definition.
  function renderFields(p, container, opts) {
    const tagging = !!(opts && opts.tagging);
    const dispKey = dispositionKey();
    const parts = [];

    for (const m of p.measures || []) {
      if (m.tagging_only && !tagging) continue;
      const b = bounds(m);
      const req = m.required && (tagging || !m.tagging_only) ? ' required' : '';
      const unit = m.unit ? ` (${esc(m.unit)})` : '';
      parts.push(
        `<div class="field">` +
          `<label for="f_${esc(m.key)}">${esc(m.label)}${unit}</label>` +
          `<input id="f_${esc(m.key)}" data-meas="${esc(m.key)}" data-scale="${m.scale}" ` +
          `type="number" inputmode="decimal" min="${b.lo}" max="${b.hi}" ${b.stepAttr}${req}>` +
          (m.help ? `<p class="note">${esc(m.help)}</p>` : '') +
        `</div>`
      );
    }

    for (const v of p.vocabs || []) {
      if (v.key === dispKey && tagging) continue;
      if (v.tagging_only && !tagging) continue;
      const req = v.required && (tagging || !v.tagging_only) ? ' required' : '';
      const options = (v.values || [])
        .map((val) => `<option value="${esc(val.code)}">${esc(val.label)}</option>`)
        .join('');
      parts.push(
        `<div class="field">` +
          `<label for="f_${esc(v.key)}">${esc(v.label)}</label>` +
          `<select id="f_${esc(v.key)}" data-attr="${esc(v.key)}"${req}>${options}</select>` +
          (v.help ? `<p class="note">${esc(v.help)}</p>` : '') +
          (v.key === dispKey ? `<p class="note" data-disp-note></p>` : '') +
        `</div>`
      );
    }

    container.innerHTML = parts.join('');
  }

  // read returns the two maps a record carries: scaled integers and codes.
  //
  // A blank number is omitted rather than sent as zero. "Not taken" and "taken,
  // and it was zero" are different facts, and a dataset that cannot tell them
  // apart has invented data in it.
  function read(container) {
    const meas = {};
    const attr = {};

    for (const el of container.querySelectorAll('[data-meas]')) {
      const raw = el.value.trim();
      if (raw === '') continue;
      const scale = Number(el.dataset.scale) || 1;
      const n = Number(raw);
      if (!Number.isFinite(n)) continue;
      // Math.round, not truncation: 28.4 * 100 is 2839.9999... in binary
      // floating point, and a carapace width one millimetre short of what
      // somebody typed is a bug nobody would ever find.
      meas[el.dataset.meas] = Math.round(n * scale);
    }
    for (const el of container.querySelectorAll('[data-attr]')) {
      if (el.value !== '') attr[el.dataset.attr] = el.value;
    }
    return { meas, attr };
  }

  // complete reports whether every required field has an answer, so a submit
  // button can be disabled rather than a request refused.
  function complete(p, meas, attr, opts) {
    const tagging = !!(opts && opts.tagging);
    const dispKey = dispositionKey();

    for (const m of p.measures || []) {
      if (!m.required || (m.tagging_only && !tagging)) continue;
      if (meas[m.key] === undefined) return false;
    }
    for (const v of p.vocabs || []) {
      if (v.key === dispKey) {
        if (!tagging && !attr[dispKey]) return false;
        continue;
      }
      if (!v.required || (v.tagging_only && !tagging)) continue;
      if (!attr[v.key]) return false;
    }
    return true;
  }

  // label renders an attribute code for a human.
  function label(p, key, code) {
    const v = (p.vocabs || []).find((x) => x.key === key);
    if (!v) return code;
    const found = (v.values || []).find((x) => x.code === code);
    return found ? found.label : code;
  }

  // measure looks up a measurement definition.
  function measure(p, key) {
    return (p.measures || []).find((m) => m.key === key) || null;
  }

  // show renders a stored integer in its unit.
  function show(p, key, value) {
    const m = measure(p, key);
    if (!m) return String(value);
    const n = m.scale > 1 ? (value / m.scale).toFixed(2) : value;
    return `${n} ${m.unit}`;
  }

  return {
    load,
    profiles,
    profile,
    dispositionKey,
    fires,
    mustRelease,
    notTaggable,
    renderFields,
    read,
    complete,
    label,
    measure,
    show,
  };
});
