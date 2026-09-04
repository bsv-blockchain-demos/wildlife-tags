/**
 * The canonical encoder, typed.
 *
 * This is a re-export, not an implementation. There is exactly one encoder in
 * this repository -- internal/web/static/canonical.js -- and Metro resolves it
 * here; see metro.config.js and ATTRIBUTION.md for why sharing beats copying.
 *
 * The short version: every signature this system makes is over the bytes an
 * observation becomes, and the server rebuilds those bytes to verify. A second
 * implementation is a second chance to disagree about a byte, and the failure
 * reads as "attestation signature does not verify" -- which names neither side.
 * web.TestTheAppAndTheServerAgreeOnCanonicalBytes compares this exact file's
 * output against Go's, byte for byte, in CI.
 */
export {
  sortDeep,
  encode,
  encodeCoord,
  decodeCoord,
  scale,
  unscale,
  observation,
  encodeObservation,
  toBytes
} from 'wildtag-canonical'

export type { ObservationInput, CanonicalObservation } from 'wildtag-canonical'
