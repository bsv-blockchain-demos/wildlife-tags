// Types for the shared canonical encoder.
//
// The implementation is internal/web/static/canonical.js, resolved by Metro --
// see metro.config.js for why it is shared rather than copied. This file is the
// TypeScript view of it and must be kept in step with that module's exports;
// there is nothing to generate it from, because the encoder has to stay
// loadable as a plain <script> in a browser with no build step.
declare module 'wildtag-canonical' {
  /** What a person saw, in the human units a form collects. */
  export interface ObservationInput {
    accuracyM: number
    attr: Record<string, string>
    lat: number
    lon: number
    /** Scaled integers, never decimals. See the profile's `scale`. */
    meas: Record<string, number>
    name?: string
    observer?: string
    species?: string
    /** RFC3339, UTC. */
    at: string
  }

  /** The record as it goes on chain: short keys, sorted, integers only. */
  export interface CanonicalObservation {
    acc: number
    attr: Record<string, string>
    lat: number
    lon: number
    meas: Record<string, number>
    name: string
    obs: string
    sp: string
    ts: string
  }

  export function sortDeep<T>(value: T): T
  export function encode(value: unknown): string
  export function encodeCoord(deg: number): number
  export function decodeCoord(e7: number): number
  export function scale(value: number, factor: number): number
  export function unscale(value: number, factor: number): number
  export function observation(o: ObservationInput): CanonicalObservation
  export function encodeObservation(o: ObservationInput): string
  /** UTF-8 bytes, which is what a wallet's createSignature is handed. */
  export function toBytes(text: string): number[]
}
