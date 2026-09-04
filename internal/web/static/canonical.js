// The canonical encoder, shared by every client that signs a record.
//
// # Why this file exists at all
//
// A record's observation is signed by the person who made it, and verified by a
// server that rebuilt the same bytes from the same values. If the two sides
// disagree about a single byte, the signature fails -- and the failure surfaces
// as "attestation signature does not verify", which says nothing about which
// side is wrong. Three separate bugs of exactly this shape reached a live
// deployment: a key derived from the wrong material, a key id in the wrong
// form, and a message hashed twice. None was visible from one side alone.
//
// So the encoding rules live in one file, and a cross-language test compares
// its output against Go's byte for byte before any of it is trusted.
//
// # The rules
//
// 1. Object keys are sorted, recursively. Go's encoding/json sorts map keys for
//    us; JSON.stringify uses *insertion order*, so this has to sort explicitly.
//    That asymmetry is the single most dangerous thing about this format.
// 2. No floats, ever. Coordinates are integer degrees times 1e7, lengths are
//    whole millimetres, temperature and salinity are hundredths. 32.7765 has
//    more than one defensible shortest representation, and a signature over the
//    wrong one fails in a way nobody can debug from a boat.
// 3. A nil or absent map encodes as {}, and an entry with an empty value is
//    dropped. "Recorded as blank" and "not recorded" must not be two different
//    signatures over the same report.
//
// Loads as a browser global (window.Canonical) and as a CommonJS module, so the
// page and the cross-language test run the same code rather than two copies of
// it that drift.
(function (root, factory) {
  if (typeof module === 'object' && module.exports) module.exports = factory();
  else root.Canonical = factory();
})(typeof self !== 'undefined' ? self : this, function () {
  'use strict';

  // sortDeep rebuilds a value with every object's keys in sorted order.
  //
  // Rebuilding rather than sorting in place matters: JSON.stringify walks
  // properties in insertion order, so the only way to control the output is to
  // control the order they were inserted in.
  function sortDeep(value) {
    if (Array.isArray(value)) return value.map(sortDeep);
    if (value === null || typeof value !== 'object') return value;

    const out = {};
    for (const key of Object.keys(value).sort()) out[key] = sortDeep(value[key]);
    return out;
  }

  // encodeCoord converts degrees to the integer form stored on chain, rounding
  // half away from zero so a fix does not drift systematically toward the
  // equator.
  function encodeCoord(deg) {
    return deg < 0 ? Math.trunc(deg * 1e7 - 0.5) : Math.trunc(deg * 1e7 + 0.5);
  }

  function decodeCoord(e7) {
    return e7 / 1e7;
  }

  // scale converts a human number to the profile's stored integer.
  //
  // Math.round rather than truncation, because a user typing 28.4 into a field
  // scaled by 100 should get 2840 and not 2839 -- floating point multiplication
  // does not land exactly on the integer.
  function scale(value, factor) {
    return Math.round(Number(value) * (factor || 1));
  }

  function unscale(value, factor) {
    return factor > 1 ? Number(value) / factor : Number(value);
  }

  // cleanAttr and cleanMeas apply rule 3.
  function cleanAttr(attr) {
    const out = {};
    for (const key of Object.keys(attr || {})) {
      const v = attr[key];
      if (key !== '' && v !== '' && v !== null && v !== undefined) out[key] = String(v);
    }
    return out;
  }

  function cleanMeas(meas) {
    const out = {};
    for (const key of Object.keys(meas || {})) {
      const v = meas[key];
      if (key === '' || v === '' || v === null || v === undefined) continue;
      const n = Number(v);
      if (!Number.isFinite(n)) continue;
      if (!Number.isInteger(n)) {
        // Rule 2. Reaching here means a caller skipped the profile's scale, and
        // the resulting signature would verify nowhere.
        throw new Error(
          'canonical: ' + key + ' is ' + n + ', which is not a whole number; ' +
          'measurements are scaled integers, never decimals');
      }
      out[key] = n;
    }
    return out;
  }

  // observation builds the signed half of a record.
  //
  // The field names are the short ones that go on chain, not the readable ones
  // a form uses, because these bytes are paid for and permanent.
  function observation(o) {
    return sortDeep({
      acc: Math.round(o.accuracyM * 100),
      attr: cleanAttr(o.attr),
      lat: encodeCoord(o.lat),
      lon: encodeCoord(o.lon),
      meas: cleanMeas(o.meas),
      name: o.name || '',
      obs: o.observer || '',
      sp: o.species || '',
      ts: o.at,
    });
  }

  // encode renders any canonical value as the exact bytes that get signed.
  function encode(value) {
    return JSON.stringify(sortDeep(value));
  }

  // encodeObservation is the one callers actually want: an observation, as the
  // string whose UTF-8 bytes the wallet signs.
  function encodeObservation(o) {
    return JSON.stringify(observation(o));
  }

  // toBytes is the UTF-8 encoding a wallet is handed. A BRC-100 createSignature
  // hashes what it is given exactly once, so callers pass these bytes and never
  // a digest of them -- passing a digest signs sha256(sha256(payload)) while the
  // server verifies sha256(payload), which is a bug that reached production.
  function toBytes(text) {
    return Array.from(new TextEncoder().encode(text));
  }

  return {
    sortDeep,
    encode,
    encodeCoord,
    decodeCoord,
    scale,
    unscale,
    observation,
    encodeObservation,
    toBytes,
  };
});
