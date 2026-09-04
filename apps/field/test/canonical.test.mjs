/**
 * The encoder the app signs with is the server's own file.
 *
 * Not a copy of it -- the same file, resolved through Metro; see
 * metro.config.js. This test is what stops that quietly becoming a copy: it
 * imports the module by the path the app's alias points at, and fails if
 * anything else appears under the app claiming to be a canonical encoder.
 *
 * Two implementations of this encoding is the exact drift that broke three
 * separate things in this project already, and the failure reads as
 * "attestation signature does not verify" -- which names neither side.
 */
import { strict as assert } from 'node:assert'
import { createRequire } from 'node:module'
import { readdirSync, readFileSync, statSync } from 'node:fs'
import path from 'node:path'
import test from 'node:test'
import { fileURLToPath } from 'node:url'

const require = createRequire(import.meta.url)
const here = path.dirname(fileURLToPath(import.meta.url))
const shared = path.resolve(here, '..', '..', '..', 'internal', 'web', 'static', 'canonical.js')
const Canonical = require(shared)

test('metro resolves the encoder to the server\'s own file', () => {
  const metro = readFileSync(path.join(here, '..', 'metro.config.js'), 'utf8')
  assert.match(metro, /wildtag-canonical/, 'the alias is gone')
  assert.match(metro, /internal\/web\/static/, 'the alias no longer points at the shared file')
  assert.match(metro, /watchFolders/, 'without watchFolders Metro will not serve a file outside the project')
})

test('no second encoder has appeared under the app', () => {
  const offenders = []
  const walk = (dir) => {
    for (const entry of readdirSync(dir)) {
      if (entry === 'node_modules' || entry.startsWith('.')) continue
      const full = path.join(dir, entry)
      if (statSync(full).isDirectory()) {
        walk(full)
        continue
      }
      if (!/\.(ts|tsx|js)$/.test(entry)) continue
      const body = readFileSync(full, 'utf8')
      // A file that implements sorted-key encoding rather than re-exporting it.
      if (/JSON\.stringify/.test(body) && /sort\(\)/.test(body)) offenders.push(full)
    }
  }
  walk(path.join(here, '..', 'src'))
  walk(path.join(here, '..', 'app'))
  assert.deepEqual(offenders, [], 'these files look like a second canonical encoder')
})

test('object keys are sorted recursively', () => {
  const out = Canonical.encodeObservation({
    accuracyM: 4.8,
    attr: { stage: 'HARD', sex: 'M', gear: 'TRAP' },
    lat: 32.7765,
    lon: -79.9311,
    meas: { wt: 2840, sal: 3120, cw: 142 },
    name: '',
    observer: '02abcd',
    species: 'CALSAP',
    at: '2026-08-26T14:03:11Z'
  })
  assert.equal(
    out,
    '{"acc":480,"attr":{"gear":"TRAP","sex":"M","stage":"HARD"},"lat":327765000,' +
      '"lon":-799311000,"meas":{"cw":142,"sal":3120,"wt":2840},"name":"","obs":"02abcd",' +
      '"sp":"CALSAP","ts":"2026-08-26T14:03:11Z"}'
  )
})

test('insertion order does not change the bytes', () => {
  const base = {
    accuracyM: 6.2,
    lat: 32.81,
    lon: -79.85,
    name: 'Old Bertha',
    observer: '02cd',
    species: 'CALSAP',
    at: '2026-12-01T09:00:00Z'
  }
  const forward = Canonical.encodeObservation({
    ...base,
    attr: { disp: 'RELEASED', gear: 'TROTLINE', sex: 'M' },
    meas: { cw: 149, sal: 3000, wt: 2000 }
  })
  const backward = Canonical.encodeObservation({
    ...base,
    attr: { sex: 'M', gear: 'TROTLINE', disp: 'RELEASED' },
    meas: { wt: 2000, sal: 3000, cw: 149 }
  })
  assert.equal(forward, backward)
})

test('a nil map and an empty answer are the same report', () => {
  const at = '2026-01-01T00:00:00Z'
  const bare = Canonical.encodeObservation({ accuracyM: 0, lat: 1, lon: 1, meas: {}, attr: {}, at })
  const blank = Canonical.encodeObservation({
    accuracyM: 0,
    lat: 1,
    lon: 1,
    meas: {},
    attr: { sex: '' },
    at
  })
  assert.equal(bare, blank)
  assert.match(bare, /"attr":\{\}/)
})

test('a decimal measurement is refused rather than rounded silently', () => {
  assert.throws(
    () =>
      Canonical.encodeObservation({
        accuracyM: 0,
        lat: 1,
        lon: 1,
        // 28.4 degrees, which belongs in the record as 2840 under a scale of 100.
        meas: { wt: 28.4 },
        attr: {},
        at: '2026-01-01T00:00:00Z'
      }),
    /whole number/
  )
})

test('coordinates round half away from zero', () => {
  // Truncation would drift every fix systematically toward the equator.
  assert.equal(Canonical.encodeCoord(32.77650005), 327765001)
  assert.equal(Canonical.encodeCoord(-32.77650005), -327765001)
  assert.equal(Canonical.decodeCoord(Canonical.encodeCoord(-79.9311)), -79.9311)
})
