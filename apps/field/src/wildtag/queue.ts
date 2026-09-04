/**
 * The offline capture queue.
 *
 * # What can and cannot happen offline
 *
 * Full offline redemption is impossible and this file does not pretend
 * otherwise: the server funds the transaction and adds DNR's half of the
 * two-of-two lock, so getting paid needs signal. What works offline is the part
 * that actually matters scientifically -- **capture**.
 *
 * An observation is built and signed here, on the device, at the moment of the
 * catch. The position fix and the timestamp are taken there and bound by the
 * signature made there, so a report submitted six hours later still says
 * exactly where and when the animal was found. That is the property a movement
 * study needs, and it is why the record format splits the signed observation
 * from the unsigned settlement: none of the amounts exist yet when this row is
 * written.
 *
 * The delay is recorded rather than smoothed away. The server stamps it into
 * the settlement as `q`, and it reaches the public dataset as
 * `queued_seconds`, so a researcher can tell a fix taken at the moment of the
 * catch from one submitted after the boat came in.
 */
import * as SQLite from 'expo-sqlite'

import type { Observation } from './types'

export type QueueKind = 'report' | 'tagging'

/**
 * A payment that is the finder's on chain but that their wallet has not been
 * able to credit yet.
 *
 * This is not a failure state. A wallet verifies an incoming payment by walking
 * its BEEF back to a transaction with a merkle proof, and a payment broadcast a
 * minute ago has none -- nor does its parent, if the tag was armed the same
 * afternoon. Until a block arrives there is genuinely nothing to verify.
 *
 * The money is already theirs; only the bookkeeping is pending. So the receipt
 * is kept and retried rather than discarded with an error about an argument.
 */
export interface PendingPayment {
  id: number
  tagID: string
  txid: string
  payoutIndex: number
  atomicBeefHex: string
  derivationPrefix: string
  derivationSuffix: string
  senderIdentityKey: string
  satoshis: number
  createdAt: string
  attempts: number
  lastError: string
}

export interface QueuedItem {
  id: number
  kind: QueueKind
  tagID: string
  /** base64url, so a queued report can still unlock its tag when it goes out. */
  secret: string
  observation: Observation
  /** The canonical bytes, exactly as signed. Never rebuilt. */
  observationHex: string
  attestSig: string
  attestPub: string
  createdAt: string
  attempts: number
  lastError: string
}

let db: SQLite.SQLiteDatabase | null = null

async function open(): Promise<SQLite.SQLiteDatabase> {
  if (db) return db
  db = await SQLite.openDatabaseAsync('wildtag-outbox.db')
  await db.execAsync(`
    PRAGMA journal_mode = WAL;
    CREATE TABLE IF NOT EXISTS outbox (
      id              INTEGER PRIMARY KEY AUTOINCREMENT,
      kind            TEXT NOT NULL,
      tag_id          TEXT NOT NULL,
      secret          TEXT NOT NULL DEFAULT '',
      observation     TEXT NOT NULL,
      observation_hex TEXT NOT NULL,
      attest_sig      TEXT NOT NULL,
      attest_pub      TEXT NOT NULL,
      created_at      TEXT NOT NULL,
      attempts        INTEGER NOT NULL DEFAULT 0,
      last_error      TEXT NOT NULL DEFAULT ''
    );
    -- One queued item per tag per kind. A finder who taps submit twice on a bad
    -- connection must not end up with two reports racing to spend one output.
    CREATE UNIQUE INDEX IF NOT EXISTS outbox_one_per_tag ON outbox (tag_id, kind);

    -- Payments waiting for a block. Keyed by txid rather than by tag: one tag
    -- can be reported more than once over its life, and each payment is its
    -- own coin.
    CREATE TABLE IF NOT EXISTS payments (
      id                  INTEGER PRIMARY KEY AUTOINCREMENT,
      tag_id              TEXT NOT NULL,
      txid                TEXT NOT NULL UNIQUE,
      payout_index        INTEGER NOT NULL,
      atomic_beef         TEXT NOT NULL,
      derivation_prefix   TEXT NOT NULL,
      derivation_suffix   TEXT NOT NULL,
      sender_identity_key TEXT NOT NULL,
      satoshis            INTEGER NOT NULL DEFAULT 0,
      created_at          TEXT NOT NULL,
      attempts            INTEGER NOT NULL DEFAULT 0,
      last_error          TEXT NOT NULL DEFAULT ''
    );
  `)
  return db
}

type Row = {
  id: number
  kind: string
  tag_id: string
  secret: string
  observation: string
  observation_hex: string
  attest_sig: string
  attest_pub: string
  created_at: string
  attempts: number
  last_error: string
}

const toItem = (r: Row): QueuedItem => ({
  id: r.id,
  kind: r.kind as QueueKind,
  tagID: r.tag_id,
  secret: r.secret,
  observation: JSON.parse(r.observation) as Observation,
  observationHex: r.observation_hex,
  attestSig: r.attest_sig,
  attestPub: r.attest_pub,
  createdAt: r.created_at,
  attempts: r.attempts,
  lastError: r.last_error
})

export interface EnqueueArgs {
  kind: QueueKind
  tagID: string
  secret?: string
  observation: Observation
  observationHex: string
  attestSig: string
  attestPub: string
}

/**
 * enqueue stores a signed observation for later submission.
 *
 * The signed bytes are stored verbatim alongside the values they came from.
 * Rebuilding them at submission time would risk producing different bytes from
 * the ones that were signed -- a different timestamp, a rounding that landed
 * elsewhere -- and the signature would then verify against nothing.
 */
export async function enqueue(args: EnqueueArgs): Promise<void> {
  const conn = await open()
  await conn.runAsync(
    `INSERT OR REPLACE INTO outbox
       (kind, tag_id, secret, observation, observation_hex, attest_sig, attest_pub, created_at, attempts, last_error)
     VALUES (?, ?, ?, ?, ?, ?, ?, ?, 0, '')`,
    args.kind,
    args.tagID,
    args.secret ?? '',
    JSON.stringify(args.observation),
    args.observationHex,
    args.attestSig,
    args.attestPub,
    new Date().toISOString()
  )
}

export async function pending(): Promise<QueuedItem[]> {
  const conn = await open()
  const rows = await conn.getAllAsync<Row>('SELECT * FROM outbox ORDER BY id')
  return rows.map(toItem)
}

export async function count(): Promise<number> {
  const conn = await open()
  const row = await conn.getFirstAsync<{ n: number }>('SELECT COUNT(*) AS n FROM outbox')
  return row?.n ?? 0
}

export async function forTag(tagID: string, kind: QueueKind): Promise<QueuedItem | null> {
  const conn = await open()
  const row = await conn.getFirstAsync<Row>(
    'SELECT * FROM outbox WHERE tag_id = ? AND kind = ?',
    tagID,
    kind
  )
  return row ? toItem(row) : null
}

/** done removes an item that was accepted by the server. */
export async function done(id: number): Promise<void> {
  const conn = await open()
  await conn.runAsync('DELETE FROM outbox WHERE id = ?', id)
}

/**
 * failed records an attempt that did not succeed.
 *
 * `keep` is the caller's judgement about whether trying again could ever work.
 * A refusal -- the wrong species, a tag already redeemed, a record too old --
 * will refuse identically forever, and retrying it every time the phone finds
 * signal is a queue that never drains and an error the user cannot clear. Those
 * are dropped, with the reason surfaced. Anything transient stays.
 */
export async function failed(id: number, reason: string, keep: boolean): Promise<void> {
  const conn = await open()
  if (!keep) {
    await conn.runAsync('DELETE FROM outbox WHERE id = ?', id)
    return
  }
  await conn.runAsync(
    'UPDATE outbox SET attempts = attempts + 1, last_error = ? WHERE id = ?',
    reason,
    id
  )
}

/** discard drops an item at the user's request. */
export async function discard(id: number): Promise<void> {
  await done(id)
}

// ---- payments waiting for a block ------------------------------------------

type PaymentRow = {
  id: number
  tag_id: string
  txid: string
  payout_index: number
  atomic_beef: string
  derivation_prefix: string
  derivation_suffix: string
  sender_identity_key: string
  satoshis: number
  created_at: string
  attempts: number
  last_error: string
}

const toPayment = (r: PaymentRow): PendingPayment => ({
  id: r.id,
  tagID: r.tag_id,
  txid: r.txid,
  payoutIndex: r.payout_index,
  atomicBeefHex: r.atomic_beef,
  derivationPrefix: r.derivation_prefix,
  derivationSuffix: r.derivation_suffix,
  senderIdentityKey: r.sender_identity_key,
  satoshis: r.satoshis,
  createdAt: r.created_at,
  attempts: r.attempts,
  lastError: r.last_error
})

export interface KeepPaymentArgs {
  tagID: string
  txid: string
  payoutIndex: number
  atomicBeefHex: string
  derivationPrefix: string
  derivationSuffix: string
  senderIdentityKey: string
  satoshis: number
  reason: string
}

/**
 * keepPayment stores a receipt the wallet could not credit yet.
 *
 * INSERT OR IGNORE, because a retry that fails again must not multiply the
 * row -- and because the txid is the coin's identity, so a second copy would
 * be a second attempt to internalize the same output.
 */
export async function keepPayment(args: KeepPaymentArgs): Promise<void> {
  const conn = await open()
  await conn.runAsync(
    `INSERT OR IGNORE INTO payments
       (tag_id, txid, payout_index, atomic_beef, derivation_prefix, derivation_suffix,
        sender_identity_key, satoshis, created_at, attempts, last_error)
     VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, 0, ?)`,
    args.tagID,
    args.txid,
    args.payoutIndex,
    args.atomicBeefHex,
    args.derivationPrefix,
    args.derivationSuffix,
    args.senderIdentityKey,
    args.satoshis,
    new Date().toISOString(),
    args.reason
  )
}

export async function pendingPayments(): Promise<PendingPayment[]> {
  const conn = await open()
  const rows = await conn.getAllAsync<PaymentRow>('SELECT * FROM payments ORDER BY id')
  return rows.map(toPayment)
}

export async function pendingPaymentCount(): Promise<number> {
  const conn = await open()
  const row = await conn.getFirstAsync<{ n: number }>('SELECT COUNT(*) AS n FROM payments')
  return row?.n ?? 0
}

/** paymentCredited removes a payment the wallet has accepted. */
export async function paymentCredited(id: number): Promise<void> {
  const conn = await open()
  await conn.runAsync('DELETE FROM payments WHERE id = ?', id)
}

/**
 * paymentFailed records another unsuccessful attempt.
 *
 * Never drops the row, unlike the observation outbox. An observation the server
 * refuses is worthless, but a payment is a coin: if this device cannot credit
 * it the satoshis are still on chain and still the finder's, and deleting the
 * receipt is the only way to actually lose them.
 */
export async function paymentFailed(id: number, reason: string): Promise<void> {
  const conn = await open()
  await conn.runAsync(
    'UPDATE payments SET attempts = attempts + 1, last_error = ? WHERE id = ?',
    reason,
    id
  )
}
