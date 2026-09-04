/**
 * The finder flow: scan a tag, report what you caught, get paid.
 *
 * Six steps, and the order of two of them is load-bearing.
 *
 * The interesting one is verifyPayout. This device holds the tag's private key
 * and is asked to sign a transaction the *server* built. That is only safe if
 * the device checks what it is signing -- otherwise the server could hand over
 * a transaction paying somebody else entirely and the device would sign it.
 * The check is possible without the finder's private key ever leaving their
 * wallet: BRC-29 payment outputs are derived with type-42, so the wallet can be
 * asked to derive the key output zero *should* be locked to, and the result is
 * compared against the transaction byte for byte.
 *
 * verifyPayout runs BEFORE the tag key signs anything. Reversing those two is
 * the whole difference between checking a payment and rubber-stamping it.
 */
import {
  Hash,
  PublicKey,
  Transaction,
  TransactionSignature,
  Utils,
  type WalletInterface
} from '@bsv/sdk'

import * as api from './api'
import * as queue from './queue'
import { encodeObservation, toBytes } from './canonical'
import { privateKey } from './tagkey'
import type { Observation, RecaptureQuote, RedeemReceipt } from './types'

/**
 * BRC-29's protocol tuple. Security level 2, and the protocol id is BRC-29's
 * own identifier; the key id is the derivation prefix and suffix joined by a
 * single space. Any of those wrong yields a different derived key, and the
 * payout check then fails for a reason that has nothing to do with the payment.
 */
const BRC29_PROTOCOL: [2, string] = [2, '3241645161d8']

/** The protocol a finder's wallet attests their report under. */
const ATTEST_PROTOCOL: [2, string] = [2, 'wildtag observation']

export type Step =
  | 'quote'
  | 'attest'
  | 'build'
  | 'verify'
  | 'sign'
  | 'receive'

export interface Progress {
  (step: Step, state: 'doing' | 'done'): void
}

const hex = (b: number[] | Uint8Array): string =>
  Array.from(b, (x) => x.toString(16).padStart(2, '0')).join('')

const fromHex = (h: string): number[] => Utils.toArray(h, 'hex')

const sats = (n: number): string => `${n.toLocaleString()} sats`

/**
 * PayoutRefused means the transaction does not pay this finder what they were
 * quoted. Nothing has been signed when it is thrown, and nothing should be:
 * retrying would sign the same bad transaction.
 */
export class PayoutRefused extends Error {
  constructor(message: string) {
    super(message)
    this.name = 'PayoutRefused'
  }
}

/** quote asks what the report is worth and what bytes the wallet must sign. */
export async function quote(
  tagID: string,
  observation: Observation,
  payee: string
): Promise<RecaptureQuote> {
  return api.quoteRecapture({
    tag_id: tagID,
    lat: observation.lat,
    lon: observation.lon,
    accuracy_m: observation.accuracyM,
    meas: observation.meas,
    attr: observation.attr,
    payee,
    name: observation.name ?? ''
  })
}

/**
 * attest signs an observation with the finder's own identity.
 *
 * The wallet is handed the payload bytes, NOT a hash of them. BRC-100's
 * createSignature applies SHA-256 to `data` before signing, so passing a digest
 * signs sha256(sha256(payload)) while the server verifies against
 * sha256(payload) -- a mismatch that surfaces only as "attestation signature
 * does not verify", with both halves looking correct in isolation. This reached
 * a live deployment once already.
 *
 * The counterparty is 'anyone', not 'self'. The wallet derives a BRC-42 child
 * either way and signs with that, never with the identity key -- but only the
 * anyone derivation can be reproduced by a third party from the published
 * identity key, which is what makes the record's attribution checkable by
 * anybody reading the dataset. Under 'self' nobody but the signer could ever
 * verify their own attestation.
 *
 * The keyID is the canonical tag id the server returned, never the string this
 * app parsed out of a URL: a displayed id carries a dash, the canonical one
 * does not, and deriving under the wrong one produces a key that verifies
 * nowhere.
 */
export async function attest(
  wallet: WalletInterface,
  canonicalTagID: string,
  observationHex: string
): Promise<string> {
  const { signature } = await wallet.createSignature({
    protocolID: ATTEST_PROTOCOL,
    keyID: canonicalTagID,
    counterparty: 'anyone',
    data: fromHex(observationHex)
  })
  return hex(signature)
}

/**
 * verifyPayout is the reason this app signs on the device at all.
 *
 * Without it, device-side signing would be theatre: the server would build a
 * transaction, the device would sign whatever it was handed, and the tag key's
 * presence would prove possession of the tag but nothing about where the money
 * went.
 */
export async function verifyPayout(
  wallet: WalletInterface,
  tx: Transaction,
  draft: {
    payout_index: number
    payout_satoshis: number
    derivation_prefix: string
    derivation_suffix: string
    sender_identity_key: string
  }
): Promise<void> {
  const { publicKey: derivedHex } = await wallet.getPublicKey({
    protocolID: BRC29_PROTOCOL,
    keyID: `${draft.derivation_prefix} ${draft.derivation_suffix}`,
    counterparty: draft.sender_identity_key,
    forSelf: true
  })

  // fromString, not the constructor: `new PublicKey(hex)` reads the string as
  // an x-coordinate rather than a DER-encoded key, and yields a different --
  // and wrong -- hash without complaining. That one reached production too.
  const expected = hex(PublicKey.fromString(derivedHex).toHash() as number[])

  const out = tx.outputs[draft.payout_index]
  if (!out) throw new PayoutRefused('The payment has no output where the reward should be.')

  // OP_DUP OP_HASH160 <20 bytes> OP_EQUALVERIFY OP_CHECKSIG
  const chunks = out.lockingScript.chunks
  const standard =
    chunks.length === 5 &&
    chunks[0]?.op === 0x76 &&
    chunks[1]?.op === 0xa9 &&
    chunks[3]?.op === 0x88 &&
    chunks[4]?.op === 0xac &&
    chunks[2]?.data?.length === 20
  if (!standard) {
    throw new PayoutRefused('The payment output is not a standard payment. Refusing to sign.')
  }
  if (hex(chunks[2]!.data as number[]) !== expected) {
    throw new PayoutRefused(
      'The payment does not go to your wallet. Nothing has been signed and no money has moved. ' +
        'Do not retry — report this.'
    )
  }
  if (Number(out.satoshis) !== Number(draft.payout_satoshis)) {
    throw new PayoutRefused(
      `The payment is ${sats(Number(out.satoshis))}, not the ${sats(draft.payout_satoshis)} you were ` +
        'quoted. Refusing to sign.'
    )
  }
}

/**
 * signTagInput unlocks the reward with the key printed on the tag.
 *
 * SIGHASH_ALL over everything, so the report and the payment cannot be
 * separated afterwards: altering either invalidates this signature.
 *
 * The two-step hashing is the library's contract rather than a mistake.
 * format() returns the BIP-143 preimage; sha256 is applied here and
 * PrivateKey.sign applies the second internally, which together give Bitcoin's
 * double SHA-256. This mirrors what @bsv/sdk's own P2PKH template does and is
 * the only combination the Go interpreter accepts -- which
 * web.TestTheAppProducesASignatureTheInterpreterAccepts checks in CI rather
 * than leaving to a field report.
 */
export function signTagInput(tx: Transaction, inputIndex: number, secret: number[]): string {
  const scope = TransactionSignature.SIGHASH_ALL | TransactionSignature.SIGHASH_FORKID
  const input = tx.inputs[inputIndex]
  if (!input?.sourceTransaction) throw new Error('The transaction is missing the tag output it spends.')
  const source = input.sourceTransaction.outputs[input.sourceOutputIndex]
  if (!source) throw new Error('The tag output this spends is not in the transaction.')

  const preimage = TransactionSignature.format({
    sourceTXID: input.sourceTXID ?? input.sourceTransaction.id('hex'),
    sourceOutputIndex: input.sourceOutputIndex,
    sourceSatoshis: source.satoshis!,
    transactionVersion: tx.version,
    otherInputs: tx.inputs.filter((_, i) => i !== inputIndex),
    outputs: tx.outputs,
    inputIndex,
    subscript: source.lockingScript,
    inputSequence: input.sequence!,
    lockTime: tx.lockTime,
    scope
  })

  const raw = privateKey(secret).sign(Hash.sha256(preimage))
  // toChecksigFormat appends the sighash byte; doing that by hand is a classic
  // off-by-one that only shows up as a script failure at broadcast.
  return hex(new TransactionSignature(raw.r, raw.s, scope).toChecksigFormat())
}

export interface RedeemArgs {
  wallet: WalletInterface
  tagID: string
  secret: number[]
  observation: Observation
  /** The finder's identity key: where the money goes. */
  payee: string
  onStep?: Progress
}

/** redeem runs the whole flow and returns the receipt. */
export async function redeem(args: RedeemArgs): Promise<RedeemReceipt> {
  const { wallet, tagID, secret, observation, payee } = args
  const step: Progress = args.onStep ?? (() => {})

  // 1. What are we owed, and what exactly does the wallet sign?
  step('quote', 'doing')
  const q = await quote(tagID, observation, payee)
  // The canonical id the server returned, never the one parsed from a URL.
  const canonicalTagID = q.tag_id || tagID
  step('quote', 'done')

  // 2. The finder attests to their own report. This is what puts their name on
  //    the record rather than the server operator's.
  step('attest', 'doing')
  const attestSig = await attest(wallet, canonicalTagID, q.observation)
  step('attest', 'done')

  const form: api.RecaptureForm = {
    tag_id: canonicalTagID,
    lat: observation.lat,
    lon: observation.lon,
    accuracy_m: observation.accuracyM,
    meas: observation.meas,
    attr: observation.attr,
    payee,
    name: observation.name ?? '',
    observation: q.observation,
    attest_sig: attestSig,
    attest_pub: payee
  }

  // 3. Ask the server to build the payment.
  step('build', 'doing')
  const draft = await api.prepareRedeem(form)
  step('build', 'done')

  // 4. Check it really pays us, before signing anything.
  step('verify', 'doing')
  const tx = Transaction.fromHexBEEF(draft.signable_tx)
  await verifyPayout(wallet, tx, draft)
  step('verify', 'done')

  // 5. Unlock the tag with the key printed on it.
  step('sign', 'doing')
  const tagSig = signTagInput(tx, draft.input_index, secret)
  const receipt = await api.completeRedeem(draft.reference, tagSig)
  step('sign', 'done')

  // 6. Hand the payment to the finder's own wallet so it shows in their balance.
  step('receive', 'doing')
  await receive(wallet, canonicalTagID, receipt)
  step('receive', 'done')

  return receipt
}

/**
 * receive credits a payment to the wallet, or keeps it for when it can be.
 *
 * A wallet verifies an incoming payment by walking its BEEF back to a
 * transaction with a merkle proof. A payment broadcast a moment ago has none,
 * and neither does its parent if the tag was armed the same afternoon -- so
 * until a block arrives there is genuinely nothing to verify, and the wallet
 * refuses with WERR_INVALID_PARAMETER('tx', 'valid AtomicBEEF').
 *
 * That is not the payment failing. The satoshis are already the finder's: the
 * transaction is signed, broadcast and irreversible. Only the bookkeeping is
 * pending. So a refusal here stores the receipt and returns normally -- the
 * finder is told they were paid, because they were -- and the queue credits it
 * when the block lands.
 *
 * Losing the receipt is the one outcome that would actually cost them money,
 * which is why nothing in this path throws it away.
 */
export async function receive(
  wallet: WalletInterface,
  tagID: string,
  receipt: RedeemReceipt
): Promise<void> {
  try {
    if (!receipt.atomic_beef) throw new Error('the receipt carries no BEEF yet')
    await wallet.internalizeAction({
      tx: fromHex(receipt.atomic_beef),
      outputs: [
        {
          outputIndex: receipt.payout_index,
          protocol: 'wallet payment',
          paymentRemittance: {
            derivationPrefix: receipt.derivation_prefix,
            derivationSuffix: receipt.derivation_suffix,
            senderIdentityKey: receipt.sender_identity_key
          }
        }
      ],
      description: `Wildlife tag ${tagID}`
    })
  } catch (err) {
    await queue.keepPayment({
      tagID,
      txid: receipt.txid,
      payoutIndex: receipt.payout_index,
      atomicBeefHex: receipt.atomic_beef,
      derivationPrefix: receipt.derivation_prefix,
      derivationSuffix: receipt.derivation_suffix,
      senderIdentityKey: receipt.sender_identity_key,
      satoshis: receipt.payout_satoshis,
      reason: err instanceof Error ? err.message : String(err)
    })
  }
}

/**
 * claimPending retries every payment the wallet could not credit before.
 *
 * Returns how many were credited, so a screen can say something specific
 * rather than "done".
 */
export async function claimPending(wallet: WalletInterface): Promise<number> {
  let credited = 0
  for (const p of await queue.pendingPayments()) {
    try {
      await wallet.internalizeAction({
        tx: fromHex(p.atomicBeefHex),
        outputs: [
          {
            outputIndex: p.payoutIndex,
            protocol: 'wallet payment',
            paymentRemittance: {
              derivationPrefix: p.derivationPrefix,
              derivationSuffix: p.derivationSuffix,
              senderIdentityKey: p.senderIdentityKey
            }
          }
        ],
        description: `Wildlife tag ${p.tagID}`
      })
      await queue.paymentCredited(p.id)
      credited++
    } catch (err) {
      // Still no block, most likely. Kept, never dropped: this row is the only
      // record this device has of a coin that is already its owner's.
      await queue.paymentFailed(p.id, err instanceof Error ? err.message : String(err))
    }
  }
  return credited
}

/**
 * canonicalBytes renders an observation exactly as it will be signed.
 *
 * Used by the offline queue, which has to build and sign a report with no
 * server in reach -- the whole reason the record format has an unsigned
 * settlement half.
 */
export function canonicalBytes(o: Observation): { text: string; bytes: number[] } {
  const text = encodeObservation({
    accuracyM: o.accuracyM,
    attr: o.attr,
    lat: o.lat,
    lon: o.lon,
    meas: o.meas,
    name: o.name ?? '',
    observer: o.observer,
    species: o.species,
    at: o.at
  })
  return { text, bytes: toBytes(text) }
}
