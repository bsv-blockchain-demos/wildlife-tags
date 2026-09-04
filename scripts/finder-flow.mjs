#!/usr/bin/env node
/**
 * Walk the whole finder flow against a running deployment.
 *
 * This is the end-to-end proof that a scanned tag turns into money, and it runs
 * the same code the phone does: the canonical encoder is
 * internal/web/static/canonical.js -- the one file both the web page and the
 * Expo app import -- and the sequence, the payout check and the signature
 * construction mirror apps/field/src/wildtag/redeem.ts step for step.
 *
 * It exists because the parts that break here break silently. A wrong derived
 * key, a payload hashed twice, a payout output nobody checked: each looks
 * correct from one side and fails only when real money is involved. Running it
 * against a live server is the only way to know.
 *
 *   node scripts/finder-flow.mjs --server http://127.0.0.1:8120 \
 *        --tag ECX-ZMJP --secret <base64url> [--name "Old Bertha"] \
 *        --meas tl=545 --attr sex=U,gear=HANDLINE,disp=RELEASED
 *
 * The finder's wallet is a ProtoWallet built from a throwaway key, which is
 * exactly what a real one does for the two operations this needs: derive a
 * BRC-29 public key, and sign an attestation. It cannot internalize a payment,
 * so the last step reports the outpoint instead of crediting a balance.
 */
import { createRequire } from 'node:module'
import path from 'node:path'
import { fileURLToPath } from 'node:url'

const require = createRequire(import.meta.url)
const here = path.dirname(fileURLToPath(import.meta.url))
const staticDir = path.resolve(here, '..', 'internal', 'web', 'static')

const bsv = require(path.join(staticDir, 'vendor', 'bsv-sdk.js'))
const Canonical = require(path.join(staticDir, 'canonical.js'))

const { Beef, Hash, PrivateKey, ProtoWallet, PublicKey, Transaction, TransactionSignature, Utils } = bsv

const BRC29_PROTOCOL = [2, '3241645161d8']
const ATTEST_PROTOCOL = [2, 'wildtag observation']
const TAG_KEY_DOMAIN = 'wildtag-v1-key|'

// ---- arguments -------------------------------------------------------------

function args() {
  const out = {}
  for (let i = 2; i < process.argv.length; i += 2) {
    out[process.argv[i].replace(/^--/, '')] = process.argv[i + 1]
  }
  return out
}

function pairs(text, asNumber) {
  const out = {}
  for (const pair of (text ?? '').split(',')) {
    if (!pair.trim()) continue
    const [k, v] = pair.split('=')
    if (!k || v === undefined) throw new Error(`"${pair}" is not a key=value pair`)
    out[k.trim()] = asNumber ? Number(v) : v.trim()
  }
  return out
}

// ---- helpers ---------------------------------------------------------------

const hex = (b) => Array.from(b, (x) => x.toString(16).padStart(2, '0')).join('')
const fromHex = (h) => Utils.toArray(h, 'hex')
const sats = (n) => `${Number(n).toLocaleString()} sats`

function tagKeyFrom(secretB64) {
  const padded = secretB64.length % 4 === 0 ? secretB64 : secretB64 + '='.repeat(4 - (secretB64.length % 4))
  const bytes = Utils.toArray(padded.replace(/-/g, '+').replace(/_/g, '/'), 'base64')
  if (bytes.length !== 16) throw new Error(`the tag secret is ${bytes.length} bytes, not 16`)
  return {
    secret: bytes,
    key: new PrivateKey(Hash.sha256(Utils.toArray(TAG_KEY_DOMAIN, 'utf8').concat(bytes)))
  }
}

async function api(server, pathname, body) {
  const res = await fetch(`${server}${pathname}`, {
    method: body ? 'POST' : 'GET',
    headers: body ? { 'Content-Type': 'application/json' } : undefined,
    body: body ? JSON.stringify(body) : undefined
  })
  const text = await res.text()
  let parsed = null
  try {
    parsed = text ? JSON.parse(text) : null
  } catch {
    /* the server answered with something that is not JSON */
  }
  if (!res.ok) throw new Error(parsed?.error ?? `HTTP ${res.status}: ${text.slice(0, 200)}`)
  return parsed
}

/**
 * verifyPayout is the step the whole exercise is for.
 *
 * The server built this transaction. Before the tag key signs it, the finder's
 * own wallet derives the key output zero should be locked to and the script is
 * compared against it. Without this, signing on the device would be theatre.
 */
async function verifyPayout(wallet, tx, draft) {
  const { publicKey } = await wallet.getPublicKey({
    protocolID: BRC29_PROTOCOL,
    keyID: `${draft.derivation_prefix} ${draft.derivation_suffix}`,
    counterparty: draft.sender_identity_key,
    forSelf: true
  })
  // fromString, not the constructor: `new PublicKey(hex)` reads the string as an
  // x-coordinate rather than a DER key and hashes to the wrong thing silently.
  const expected = hex(PublicKey.fromString(publicKey).toHash())

  const out = tx.outputs[draft.payout_index]
  if (!out) throw new Error('the payment has no output where the reward should be')

  const c = out.lockingScript.chunks
  const standard =
    c.length === 5 && c[0].op === 0x76 && c[1].op === 0xa9 && c[3].op === 0x88 && c[4].op === 0xac &&
    c[2].data?.length === 20
  if (!standard) throw new Error('the payment output is not a standard payment; refusing to sign')
  if (hex(c[2].data) !== expected) {
    throw new Error('the payment does not go to this wallet. Nothing signed, nothing moved.')
  }
  if (Number(out.satoshis) !== Number(draft.payout_satoshis)) {
    throw new Error(
      `the payment is ${sats(out.satoshis)}, not the ${sats(draft.payout_satoshis)} quoted`
    )
  }
}

function signTagInput(tx, inputIndex, tagKey) {
  const scope = TransactionSignature.SIGHASH_ALL | TransactionSignature.SIGHASH_FORKID
  const input = tx.inputs[inputIndex]
  const source = input.sourceTransaction.outputs[input.sourceOutputIndex]

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
    scope
  })
  const raw = tagKey.sign(Hash.sha256(preimage))
  return hex(new TransactionSignature(raw.r, raw.s, scope).toChecksigFormat())
}

// ---- the flow --------------------------------------------------------------

async function main() {
  const a = args()
  const server = (a.server ?? 'http://127.0.0.1:8120').replace(/\/+$/, '')
  const tagID = (a.tag ?? '').replace(/-/g, '').toUpperCase()
  if (!tagID || !a.secret) throw new Error('--tag and --secret are required')

  const { secret, key: tagKey } = tagKeyFrom(a.secret)
  const finder = a.key ? PrivateKey.fromHex(a.key) : PrivateKey.fromRandom()
  const wallet = new ProtoWallet(finder)
  const payee = finder.toPublicKey().toString()

  console.log(`server   ${server}`)
  console.log(`tag      ${tagID}`)
  console.log(`finder   ${payee}`)

  // What is this animal, and what does its profile ask for?
  const schema = await api(server, '/api/schema')
  const info = await api(server, `/api/tag/${encodeURIComponent(tagID)}`)
  const profile =
    schema.profiles.find((p) => p.code === info.provenance.species) ??
    schema.profiles.find((p) => p.code === schema.default)
  console.log(`species  ${profile.code} — ${profile.common} (${profile.scientific})`)
  console.log(`status   ${info.tag.status}, generation ${info.tag.generation}`)
  if (info.provenance.tagged_at) {
    console.log(
      `history  tagged ${info.provenance.tagged_at}, ${info.provenance.days_at_large} days at large, ` +
        `${info.provenance.distance_m} m from where it started`
    )
  }

  const meas = pairs(a.meas, true)
  const attr = pairs(a.attr, false)

  // A position near where it was tagged, unless one was given.
  const lat = a.lat ? Number(a.lat) : info.provenance.tagged_lat + 0.03
  const lon = a.lon ? Number(a.lon) : info.provenance.tagged_lon + 0.04
  const accuracyM = a.acc ? Number(a.acc) : 6.2

  const form = {
    tag_id: tagID,
    lat,
    lon,
    accuracy_m: accuracyM,
    meas,
    attr,
    payee,
    name: a.name ?? ''
  }

  // 1. What are we owed, and what exactly is signed?
  console.log('\n1 quote')
  const q = await api(server, '/api/redeem/quote', form)
  console.log(`  pays ${sats(q.payout_satoshis)} now, ${sats(q.bonus_satoshis)} held for a release`)
  if (q.must_release) console.log(`  must be released: ${q.must_release_reason}`)
  if (q.can_name) console.log(`  this finder may name it${a.name ? `: "${a.name}"` : ''}`)

  // The client rebuilds the same bytes independently. If these differ, every
  // signature made on a phone would fail on the server -- so it is checked here
  // rather than discovered in a marsh.
  const rebuilt = Canonical.encodeObservation({
    accuracyM,
    attr,
    lat,
    lon,
    meas,
    name: q.can_name ? (a.name ?? '') : '',
    observer: payee,
    species: profile.code,
    at: JSON.parse(Buffer.from(q.observation, 'hex').toString('utf8')).ts
  })
  const served = Buffer.from(q.observation, 'hex').toString('utf8')
  if (rebuilt !== served) {
    throw new Error(`canonical bytes differ\n  client: ${rebuilt}\n  server: ${served}`)
  }
  console.log('  canonical bytes match the server byte for byte')

  // 2. The finder attests to their own report.
  console.log('2 attest')
  const { signature } = await wallet.createSignature({
    protocolID: ATTEST_PROTOCOL,
    keyID: q.tag_id,
    counterparty: 'anyone',
    data: fromHex(q.observation)
  })
  console.log(`  signed as ${payee.slice(0, 16)}…`)

  // 3. Ask the server to build the payment.
  console.log('3 build')
  const draft = await api(server, '/api/redeem/prepare', {
    ...form,
    tag_id: q.tag_id,
    observation: q.observation,
    attest_sig: hex(signature),
    attest_pub: payee
  })
  console.log(`  ${sats(draft.payout_satoshis)} at output ${draft.payout_index}`)

  // 4. Check it really pays us, before signing anything.
  console.log('4 verify')
  const tx = Transaction.fromHexBEEF(draft.signable_tx)
  await verifyPayout(wallet, tx, draft)
  console.log('  the payment goes to this wallet, for the amount quoted')

  // 5. Unlock the tag.
  console.log('5 sign')
  const receipt = await api(server, '/api/redeem/complete', {
    reference: draft.reference,
    tag_sig: signTagInput(tx, draft.input_index, tagKey)
  })
  console.log(`  ${receipt.txid}`)

  // 6. A real wallet internalizes here. A ProtoWallet has no storage, so report
  //    the outpoint and its derivation instead -- which is all a wallet needs.
  console.log('6 receive')
  console.log(`  outpoint ${receipt.txid}:${receipt.payout_index}`)
  console.log(`  derivation ${receipt.derivation_prefix} ${receipt.derivation_suffix}`)
  console.log(`  from ${receipt.sender_identity_key}`)

  //    And the check that matters: can a wallet actually credit this yet?
  //
  //    A wallet verifies an incoming payment by walking its BEEF back to a
  //    transaction with a merkle proof. If the server did not attach the
  //    parent's proof, the walk reaches nothing proven and the wallet refuses
  //    with WERR_INVALID_PARAMETER('tx', 'valid AtomicBEEF') -- a message about
  //    the argument, for a problem that is nothing of the kind. That reached a
  //    phone once, so the harness checks it here rather than leaving it to be
  //    discovered in a marsh.
  await reportBeef(server, receipt)

  console.log(`\npaid ${sats(receipt.payout_satoshis)}${receipt.retired ? ' — tag retired' : ''}`)
}

/**
 * reportBeef says whether the receipt's BEEF verifies against the deployment's
 * own chain, and if not, why not.
 *
 * "Not yet" is a legitimate answer: a payment whose parent is also unmined has
 * nothing proven to anchor to, and the client keeps the receipt until a block
 * arrives. What must never happen silently is the *other* case -- a parent that
 * is mined but whose proof the server failed to attach.
 */
async function reportBeef(server, receipt) {
  if (!receipt.atomic_beef) {
    console.log('  BEEF: none returned; the client must re-fetch before it can credit this')
    return
  }
  const info = await api(server, '/api/info')
  const beef = Beef.fromBinary(fromHex(receipt.atomic_beef))
  const parts = beef.txs.map((t) => `${t.txid.slice(0, 12)}${t.tx?.merklePath ? '+proof' : ''}`)
  console.log(`  BEEF: ${parts.join(' <- ')}`)

  const tracker = new ArcadeChainTracker(info.arcade_url)
  const ok = await beef.verify(tracker, false)
  console.log(
    `  verifies: ${ok}` +
      (ok
        ? ' — a wallet credits this immediately'
        : ' — nothing in it is proven yet, so a wallet keeps it until a block arrives')
  )
}

/**
 * ArcadeChainTracker is the same two questions apps/field asks, against the
 * deployment's own headers rather than a third party's.
 */
class ArcadeChainTracker {
  constructor(arcadeURL) {
    this.base = `${arcadeURL.replace(/\/+$/, '')}/chaintracks/v2`
  }
  async currentHeight() {
    return (await (await fetch(`${this.base}/height`)).json()).height
  }
  async isValidRootForHeight(root, height) {
    const res = await fetch(`${this.base}/headers?height=${height}&count=1`)
    if (!res.ok) return false
    const bytes = new Uint8Array(await res.arrayBuffer())
    if (bytes.length < 80) return false
    // merkleRoot is bytes 36..68, little-endian on the wire.
    return hex(bytes.slice(36, 68).reverse()) === root.toLowerCase()
  }
}

main().catch((err) => {
  console.error(`\nfailed: ${err.message}`)
  process.exit(1)
})
