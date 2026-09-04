/**
 * The chain tracker's header parsing.
 *
 * `isValidRootForHeight` is what actually validates a merkle proof: the proof
 * computes a root and this decides whether that root is the one in the block at
 * that height. It reads a raw 80-byte header, and the merkle root inside is
 * stored little-endian while every API and every log prints it big-endian.
 *
 * Getting that reversal wrong does not fail loudly. It makes every proof look
 * invalid, so every payment is refused, and the message the user sees is about
 * a malformed argument. Hence a fixture: a real teratestnet header, and the
 * root arcade reports for it.
 */
import { strict as assert } from 'node:assert'
import { createHash } from 'node:crypto'
import test from 'node:test'

import { ArcadeChainTracker, ArcadeChaintracks } from '../src/wallet/arcade.ts'

// Block 30861 on teratestnet, exactly as arcade serves it from
// /chaintracks/v2/headers?height=30861&count=1 — the real 80 bytes, not a
// hand-assembled stand-in. The test below hashes them and checks the result
// against the block hash arcade reports, so a fixture that drifted into
// fiction fails rather than quietly testing nothing.
const HEADER_HEX =
  '000000202bf2121cd97af28dda6c648ab0c6e282cb94febd08c5a9f40d6f3dff00000000' +
  'c46601e0fc4516a08b2632bec36100d242af310fe101c9d9e4f72cdf598a6532' +
  '5003976affff001d800d7cb7'

const HEIGHT = 30861
const BLOCK_HASH = '0000000030a7c1f454695cba4525039ef198c1cfccb372c968248eeeef918254'
const ROOT = '32658a59df2cf7e4d9c901e10f31af42d20061c3be32268ba01645fce00166c4'
const PREV = '00000000ff3d6f0df4a9c508bdfe94cb82e2c6b08a646cda8df27ad91c12f22b'

/** stubFetch answers the two endpoints the tracker uses, and counts calls. */
function stubFetch(headerHex) {
  const calls = { height: 0, headers: 0 }
  globalThis.fetch = async (url) => {
    if (String(url).includes('/height')) {
      calls.height++
      return { ok: true, json: async () => ({ height: HEIGHT }) }
    }
    calls.headers++
    const bytes = Uint8Array.from(Buffer.from(headerHex, 'hex'))
    return { ok: true, arrayBuffer: async () => bytes.buffer }
  }
  return calls
}

test('the fixture is a genuine block header', () => {
  // Without this the other cases would pass just as happily against invented
  // bytes, and would then be checking that this file agrees with itself.
  const bytes = Buffer.from(HEADER_HEX, 'hex')
  assert.equal(bytes.length, 80)
  const once = createHash('sha256').update(bytes).digest()
  const twice = createHash('sha256').update(once).digest()
  assert.equal(Buffer.from(twice).reverse().toString('hex'), BLOCK_HASH)
})

test('the merkle root is read big-endian, as everything else prints it', async () => {
  stubFetch(HEADER_HEX)
  const t = new ArcadeChainTracker('https://arcade.example')
  assert.equal(await t.isValidRootForHeight(ROOT, HEIGHT), true)
})

test('a root from the wrong block is rejected', async () => {
  stubFetch(HEADER_HEX)
  const t = new ArcadeChainTracker('https://arcade.example')
  assert.equal(await t.isValidRootForHeight('00'.repeat(32), HEIGHT), false)
  // The previous block's hash is the classic near-miss: it is in the same
  // header, four fields along, and reversed the same way.
  assert.equal(await t.isValidRootForHeight(PREV, HEIGHT), false)
})

test('a root is compared case-insensitively', async () => {
  stubFetch(HEADER_HEX)
  const t = new ArcadeChainTracker('https://arcade.example')
  assert.equal(await t.isValidRootForHeight(ROOT.toUpperCase(), HEIGHT), true)
})

test('a height is fetched once and then remembered', async () => {
  // Buried blocks do not change, and a wallet re-checks the same height for
  // every output in a transaction.
  const calls = stubFetch(HEADER_HEX)
  const t = new ArcadeChainTracker('https://arcade.example')
  await t.isValidRootForHeight(ROOT, HEIGHT)
  await t.isValidRootForHeight(ROOT, HEIGHT)
  await t.isValidRootForHeight('00'.repeat(32), HEIGHT)
  assert.equal(calls.headers, 1)
})

test('a truncated header is refused rather than misread', async () => {
  stubFetch(HEADER_HEX.slice(0, 100))
  const t = new ArcadeChainTracker('https://arcade.example')
  assert.equal(await t.isValidRootForHeight(ROOT, HEIGHT), false)
})

test('currentHeight comes from the deployment', async () => {
  stubFetch(HEADER_HEX)
  const t = new ArcadeChainTracker('https://arcade.example')
  assert.equal(await t.currentHeight(), HEIGHT)
})

test('a trailing slash on the arcade URL does not double up the path', async () => {
  let seen = ''
  globalThis.fetch = async (url) => {
    seen = String(url)
    return { ok: true, json: async () => ({ height: HEIGHT }) }
  }
  await new ArcadeChainTracker('https://arcade.example/').currentHeight()
  assert.equal(seen, 'https://arcade.example/chaintracks/v2/height')
})

// --- the header client the monitor reads ------------------------------------

test('a raw header decodes to every field arcade reports in JSON', async () => {
  // The monitor takes its header client straight from services.options, not
  // through getChainTracker, so this class is what stands between a broadcast
  // payment and its promotion to proven. A field decoded wrong here -- an
  // endianness, a byte offset -- makes the monitor disagree with the chain
  // about which block it is looking at.
  stubFetch(HEADER_HEX)
  const ct = new ArcadeChaintracks('https://arcade.example')
  const h = await ct.findHeaderForHeight(HEIGHT)

  assert.equal(h.height, HEIGHT)
  assert.equal(h.hash, BLOCK_HASH)
  assert.equal(h.merkleRoot, ROOT)
  assert.equal(h.previousHash, PREV)
  assert.equal(h.version, 0x20000000)
  assert.equal(h.bits, 0x1d00ffff)
  // time and nonce are little-endian unsigned 32-bit reads.
  assert.equal(h.time, 1788281680)
  assert.equal(h.nonce, 3078360448)
})

test('an unknown height is undefined rather than a wrong header', async () => {
  globalThis.fetch = async () => ({ ok: false })
  const ct = new ArcadeChaintracks('https://arcade.example')
  assert.equal(await ct.findHeaderForHeight(999999), undefined)
})

test('the tracker and the header client agree about the root', async () => {
  // Two classes read the same bytes for different callers. If they ever
  // disagree, proofs verify in one place and fail in the other.
  stubFetch(HEADER_HEX)
  const tracker = new ArcadeChainTracker('https://arcade.example')
  const ct = new ArcadeChaintracks('https://arcade.example')
  const header = await ct.findHeaderForHeight(HEIGHT)
  assert.equal(await tracker.isValidRootForHeight(header.merkleRoot, HEIGHT), true)
})
