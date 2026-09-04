/**
 * Every network call this wallet makes, pointed at the deployment's own arcade.
 *
 * # Why this file exists
 *
 * `new Services('teratest')` configures the toolbox's defaults, and on this
 * network those defaults are not merely different -- they are dead. Checked on
 * 1 September 2026:
 *
 *   https://teratestnet-chaintracks.babbage.systems   does not answer at all
 *   https://api.whatsonchain.com/v1/bsv/teratest      404: no such network
 *
 * So a wallet built on the defaults has no chaintracker and no proof source. It
 * cannot verify an incoming payment, and the failure surfaces as
 * `WERR_INVALID_PARAMETER('tx', 'valid AtomicBEEF')` at internalizeAction --
 * which reads like a malformed argument and is nothing of the kind.
 *
 * There is a correct answer sitting right there: the deployment publishes the
 * arcade it writes to, and that arcade is the authority for this chain. Using
 * anything else means the phone verifying a payment against a different view of
 * the chain from the one the server broadcast it to. So everything goes through
 * arcade -- headers, proofs, raw transactions, status -- and nothing goes
 * through a third party.
 *
 * # The API
 *
 *   <arcade>/chaintracks/v2/height          {"height": n}
 *   <arcade>/chaintracks/v2/tip             a JSON block header
 *   <arcade>/chaintracks/v2/headers?height=&count=   raw 80-byte headers
 *   <arcade>/tx/<txid>                      {txStatus, blockHeight, merklePath, rawTx}
 */
import { Hash, MerklePath, Transaction, Utils, type ChainTracker } from '@bsv/sdk'
import type { Services } from '@bsv/wallet-toolbox-mobile'
import type {
  GetMerklePathResult,
  GetRawTxResult,
  GetStatusForTxidsResult,
  PostBeefResult
} from '@bsv/wallet-toolbox-mobile/out/src/sdk/WalletServices.interfaces'

/** HEADER_BYTES is the fixed serialised length of a Bitcoin block header. */
const HEADER_BYTES = 80

const NAME = 'Arcade'

async function json<T>(url: string, timeoutMs = 15000): Promise<T> {
  const abort = new AbortController()
  const timer = setTimeout(() => abort.abort(), timeoutMs)
  try {
    const res = await fetch(url, { signal: abort.signal })
    if (!res.ok) throw new Error(`${url}: HTTP ${res.status}`)
    return (await res.json()) as T
  } finally {
    clearTimeout(timer)
  }
}

/**
 * ArcadeChainTracker answers the only two questions the SDK asks of a chain
 * tracker, from the deployment's own headers.
 *
 * `isValidRootForHeight` is what actually validates a merkle proof: the proof
 * computes a root, and this says whether that root is the one in the block at
 * that height. Getting it from the same arcade the server broadcasts through is
 * the point -- a proof checked against somebody else's headers proves the
 * transaction is on somebody else's chain.
 */
export class ArcadeChainTracker implements ChainTracker {
  private readonly base: string
  /** Heights are immutable once buried, so a hit never needs revalidating. */
  private readonly roots = new Map<number, string>()

  constructor(arcadeURL: string) {
    this.base = `${arcadeURL.replace(/\/+$/, '')}/chaintracks/v2`
  }

  async currentHeight(): Promise<number> {
    const { height } = await json<{ height: number }>(`${this.base}/height`)
    return height
  }

  async isValidRootForHeight(root: string, height: number): Promise<boolean> {
    const known = this.roots.get(height)
    if (known !== undefined) return known === root.toLowerCase()

    const found = await this.merkleRootAt(height)
    if (found === null) return false
    this.roots.set(height, found)
    return found === root.toLowerCase()
  }

  /** merkleRootAt reads one 80-byte header and pulls its merkle root out. */
  private async merkleRootAt(height: number): Promise<string | null> {
    const res = await fetch(`${this.base}/headers?height=${height}&count=1`)
    if (!res.ok) return null

    const bytes = new Uint8Array(await res.arrayBuffer())
    if (bytes.length < HEADER_BYTES) return null

    // version(4) previousHash(32) merkleRoot(32) time(4) bits(4) nonce(4).
    // The root is stored little-endian and displayed big-endian, so it is
    // reversed here -- the classic silent mismatch in this format.
    const root = bytes.slice(36, 68).reverse()
    return Utils.toHex(Array.from(root))
  }
}

/**
 * ArcadeChaintracks is the header service the monitor talks to.
 *
 * A second class rather than more methods on the tracker, because they answer
 * to different callers. The SDK asks a ChainTracker two questions when it
 * verifies a proof; the monitor asks a Chaintracks client for headers, and
 * takes it from `services.options.chaintracks` **directly** -- bypassing
 * `getChainTracker()` entirely. Overriding only the tracker leaves the
 * monitor's TaskNewHeader polling a host that does not answer, which means no
 * new-block events, which means a payment that is never promoted from broadcast
 * to proven. The balance simply stays wrong, quietly.
 *
 * Only the methods the toolbox actually calls are implemented. The rest of the
 * Chaintracks API is a listening/subscription surface this app has no use for,
 * and stubbing it convincingly would be pretending to offer something.
 */
export class ArcadeChaintracks {
  private readonly base: string
  private readonly tracker: ArcadeChainTracker

  constructor(arcadeURL: string) {
    this.base = `${arcadeURL.replace(/\/+$/, '')}/chaintracks/v2`
    this.tracker = new ArcadeChainTracker(arcadeURL)
  }

  async makeAvailable(): Promise<void> {
    // Nothing to warm up: every call is a request.
  }

  currentHeight = (): Promise<number> => this.tracker.currentHeight()

  getPresentHeight(): Promise<number> {
    return this.tracker.currentHeight()
  }

  isValidRootForHeight(root: string, height: number): Promise<boolean> {
    return this.tracker.isValidRootForHeight(root, height)
  }

  async findChainTipHeader(): Promise<ArcadeHeader> {
    return json<ArcadeHeader>(`${this.base}/tip`)
  }

  async findHeaderForHeight(height: number): Promise<ArcadeHeader | undefined> {
    const res = await fetch(`${this.base}/headers?height=${height}&count=1`)
    if (!res.ok) return undefined

    const bytes = new Uint8Array(await res.arrayBuffer())
    if (bytes.length < HEADER_BYTES) return undefined
    return decodeHeader(bytes.slice(0, HEADER_BYTES), height)
  }

  /**
   * findHeaderForBlockHash walks back from the tip.
   *
   * Arcade indexes headers by height, not by hash. The callers that use this
   * are checking a proof that was just fetched, so the block is recent and the
   * walk is short -- but it is bounded rather than open-ended, because an
   * unknown hash would otherwise walk the entire chain one request at a time.
   */
  async findHeaderForBlockHash(hash: string): Promise<ArcadeHeader | undefined> {
    const want = hash.toLowerCase()
    const tip = await this.findChainTipHeader()
    const floor = Math.max(0, tip.height - 100)
    for (let h = tip.height; h >= floor; h--) {
      const header = await this.findHeaderForHeight(h)
      if (header?.hash === want) return header
    }
    return undefined
  }
}

/** decodeHeader turns 80 raw bytes into the toolbox's BlockHeader shape. */
function decodeHeader(bytes: Uint8Array, height: number): ArcadeHeader {
  const view = new DataView(bytes.buffer, bytes.byteOffset, bytes.byteLength)
  const be = (from: number, to: number) => Utils.toHex(Array.from(bytes.slice(from, to)).reverse())

  // The double-SHA256 of the 80 bytes, reversed, is the block hash.
  const hash = Utils.toHex(Hash.hash256(Array.from(bytes)).reverse())
  return {
    version: view.getUint32(0, true),
    previousHash: be(4, 36),
    merkleRoot: be(36, 68),
    time: view.getUint32(68, true),
    bits: view.getUint32(72, true),
    nonce: view.getUint32(76, true),
    height,
    hash
  }
}

/** ArcadeHeader is arcade's JSON tip, and the toolbox's BlockHeader shape. */
interface ArcadeHeader {
  version: number
  previousHash: string
  merkleRoot: string
  time: number
  bits: number
  nonce: number
  height: number
  hash: string
}

interface ArcadeTx {
  txid: string
  txStatus?: string
  blockHeight?: number
  blockHash?: string
  merklePath?: string
  rawTx?: string
  extraInfo?: string
}

const txURL = (base: string, txid: string) => `${base.replace(/\/+$/, '')}/tx/${txid}`

/**
 * install replaces every network provider on a Services with an arcade-backed
 * one, and removes the defaults.
 *
 * Removal is as important as addition. The toolbox tries providers in order
 * until one succeeds, so leaving WhatsOnChain in the list means a proof lookup
 * that silently falls through to a host with no record of this chain -- which
 * reads as "no proof yet" forever rather than as a misconfiguration.
 */
export function install(services: Services, arcadeURL: string): ArcadeChaintracks {
  const base = arcadeURL.replace(/\/+$/, '')
  const tracker = new ArcadeChainTracker(base)
  const chaintracks = new ArcadeChaintracks(base)

  // The SDK's chain tracker. Assigned rather than passed in options because the
  // toolbox builds its own from the chaintracks URL, and that URL is a Babbage
  // service with a different API from arcade's.
  services.getChainTracker = async () => tracker

  // And the header client the *monitor* reads, which it takes straight out of
  // options rather than through getChainTracker. Miss this and the tracker is
  // right while the monitor still polls a dead host, so nothing is ever
  // promoted from broadcast to proven and the balance stays quietly wrong.
  services.options.chaintracks = chaintracks as never

  const collections = [
    services.getMerklePathServices,
    services.getRawTxServices,
    services.getStatusForTxidsServices,
    services.postBeefServices
  ]
  for (const c of collections) {
    for (const name of ['WhatsOnChain', 'Bitails', 'TaalArcBeef', 'GorillaPoolArcBeef', 'ARC']) {
      try {
        c.remove(name)
      } catch {
        // Not present in this collection; nothing to remove.
      }
    }
  }

  services.getMerklePathServices.add({
    name: NAME,
    service: async (txid: string): Promise<GetMerklePathResult> => {
      const r: GetMerklePathResult = { name: NAME, notes: [] }
      try {
        const tx = await json<ArcadeTx>(txURL(base, txid))
        if (!tx.merklePath) {
          // Broadcast but not yet in a block. Not an error: the monitor asks
          // again, and this is the normal state for the first few minutes.
          r.notes?.push({ what: 'getMerklePathNoData', when: new Date().toISOString() })
          return r
        }
        r.merklePath = MerklePath.fromHex(tx.merklePath)
        const height = r.merklePath.blockHeight
        const header = await json<ArcadeHeader>(`${base}/chaintracks/v2/tip`).catch(() => null)
        if (header && header.height === height) r.header = header
        r.notes?.push({ what: 'getMerklePathSuccess', when: new Date().toISOString() })
      } catch (err) {
        r.error = err as never
        r.notes?.push({
          what: 'getMerklePathError',
          description: err instanceof Error ? err.message : String(err),
          when: new Date().toISOString()
        })
      }
      return r
    }
  })

  services.getRawTxServices.add({
    name: NAME,
    service: async (txid: string): Promise<GetRawTxResult> => {
      const r: GetRawTxResult = { name: NAME, txid }
      try {
        const tx = await json<ArcadeTx>(txURL(base, txid))
        if (!tx.rawTx) return r
        // Arcade returns extended format for anything it validated. EF carries
        // each input's source script inline, which a plain parse chokes on, so
        // try it first and fall back to raw.
        let parsed: Transaction
        try {
          parsed = Transaction.fromHexEF(tx.rawTx)
        } catch {
          parsed = Transaction.fromHex(tx.rawTx)
        }
        r.rawTx = parsed.toBinary()
      } catch (err) {
        r.error = err as never
      }
      return r
    }
  })

  services.getStatusForTxidsServices.add({
    name: NAME,
    service: async (txids: string[]): Promise<GetStatusForTxidsResult> => {
      const results = []
      for (const txid of txids) {
        try {
          const tx = await json<ArcadeTx>(txURL(base, txid))
          const mined = tx.txStatus === 'MINED' || tx.txStatus === 'IMMUTABLE'
          results.push({
            txid,
            status: (mined ? 'mined' : tx.txStatus ? 'known' : 'unknown') as never,
            depth: mined ? 1 : 0
          })
        } catch {
          results.push({ txid, status: 'unknown' as never, depth: 0 })
        }
      }
      return { name: NAME, status: 'success', results: results as never }
    }
  })

  // Broadcast, for completeness. This wallet does not use it -- the server
  // builds and signs every transaction that moves money -- but a collection
  // emptied of providers and never refilled would fail confusingly if some
  // future screen did try to send something.
  services.postBeefServices.add({
    name: NAME,
    service: async (beef, txids): Promise<PostBeefResult> => {
      const result: PostBeefResult = { name: NAME, status: 'success', txidResults: [] }
      try {
        const res = await fetch(`${base}/tx`, {
          method: 'POST',
          headers: { 'Content-Type': 'text/plain' },
          body: Utils.toHex(beef.toBinary())
        })
        const ok = res.ok
        result.status = ok ? 'success' : 'error'
        result.txidResults = txids.map((txid) => ({
          txid,
          status: ok ? 'success' : 'error'
        })) as never
      } catch (err) {
        result.status = 'error'
        result.error = err as never
        result.txidResults = txids.map((txid) => ({ txid, status: 'error' })) as never
      }
      return result
    }
  })

  return chaintracks
}
