/**
 * Assembling a BRC-100 wallet on the phone.
 *
 * The sequence is fixed by the toolbox and each layer needs the one below it:
 *
 *   KeyDeriver → WalletStorageManager → WalletSigner → Services → Wallet
 *                                                                   ↓
 *                                              StorageExpoSQLite ← Monitor
 *
 * The shape follows bsv-browser's, minus everything a field app does not need:
 * no BTMS, no permission prompts for protocols this app owns, no remote
 * storage, no exchange rates. What is kept is the storage provider and the
 * monitor, because those are what make a payment actually turn up in a balance.
 *
 * Storage is local-only and always will be. A finder's reward is theirs; a
 * design where DNR could reach it is a design where DNR has to be trusted not
 * to, and the whole point of paying on chain is that nobody has to be.
 */
import {
  KeyDeriver,
  PrivateKey,
  type WalletInterface
} from '@bsv/sdk'
import {
  Monitor,
  Services,
  StorageProvider,
  Wallet,
  WalletSigner,
  WalletStorageManager
} from '@bsv/wallet-toolbox-mobile'
import RNEventSource from 'react-native-sse'

import { StorageExpoSQLite } from '../storage'
import * as arcade from './arcade'
import type { WalletChain } from '../wildtag/types'

/**
 * Chain is the wallet's name for a network, which the deployment publishes.
 *
 * "teratest" is both Teranode testnets. Building for the wrong one is silent
 * until a payment arrives and its merkle proof is checked against headers from
 * a chain the transaction was never on -- so this value comes from
 * GET /api/info and is never assumed.
 */
export type Chain = WalletChain

export interface BuiltWallet {
  wallet: WalletInterface
  storage: StorageExpoSQLite
  monitor: Monitor
  identityKey: string
  /** Stop the background monitor. Call on logout or teardown. */
  close: () => Promise<void>
}

export interface BuildOptions {
  primaryKey: number[]
  chain: Chain
  /**
   * Arcade base URL, from GET /api/info.
   *
   * Required in practice on teratestnet: the toolbox's defaults for that
   * network are dead hosts, so without this the wallet cannot verify anything.
   */
  arcadeURL?: string
  onTransactionStatusChanged?: (txid: string, status: string) => void
}

/**
 * build assembles the wallet.
 *
 * Idempotent per identity: the database name is derived from the identity key
 * and the chain, so rebuilding after a restart reopens the same storage rather
 * than stranding coins in an orphaned file. That mistake costs real money and
 * is invisible until somebody notices their balance reset.
 */
export async function build(opts: BuildOptions): Promise<BuiltWallet> {
  const keyDeriver = new KeyDeriver(new PrivateKey(opts.primaryKey))
  const identityKey = keyDeriver.identityKey

  const storageManager = new WalletStorageManager(identityKey)
  const signer = new WalletSigner(opts.chain, keyDeriver, storageManager)

  // Everything network-facing goes through the deployment's own arcade.
  //
  // Not a preference. `new Services('teratest')` points at
  // teratestnet-chaintracks.babbage.systems and WhatsOnChain's "teratest"
  // network; the first does not answer and the second does not exist, so a
  // wallet on the defaults has no chaintracker and no proof source at all. It
  // then refuses every incoming payment with WERR_INVALID_PARAMETER('tx',
  // 'valid AtomicBEEF') -- a message about the argument, for a problem that has
  // nothing to do with it.
  //
  // Beyond being the only thing that works, it is the only thing that is
  // right: verifying a payment against a different view of the chain from the
  // one the server broadcast it to proves nothing about the payment. See
  // arcade.ts.
  const services = new Services(opts.chain)
  const chaintracks = opts.arcadeURL ? arcade.install(services, opts.arcadeURL) : undefined

  const storage = new StorageExpoSQLite({
    ...StorageProvider.createStorageBaseOptions(opts.chain),
    feeModel: { model: 'sat/kb', value: 100 },
    identityKey,
    databaseName: `wildtag-${identityKey.slice(-8)}-${opts.chain}.db`
  })
  storage.setServices(services)
  await storage.migrate('wildtag-field', identityKey)
  // addWalletStorageProvider calls makeAvailable internally.
  await storageManager.addWalletStorageProvider(storage as never)

  const wallet = new Wallet(signer, services)

  // The monitor is what turns "broadcast" into "proven": it fetches merkle
  // proofs and promotes transactions, which is how a reward stops being
  // provisional. Without it a finder's balance would show a payment that never
  // finished settling.
  const monitorOptions = Monitor.createDefaultWalletMonitorOptions(
    opts.chain,
    storageManager,
    services
  )
  // createDefaultWalletMonitorOptions copies services.options.chaintracks at
  // call time, so install() reaching that field is necessary but not obviously
  // sufficient. Set it here too, explicitly, rather than depending on the order
  // of two assignments in someone else's constructor.
  if (chaintracks) monitorOptions.chaintracks = chaintracks as never
  monitorOptions.callbackToken = identityKey.substring(0, 32)
  monitorOptions.EventSourceClass = RNEventSource as never
  if (opts.onTransactionStatusChanged) {
    monitorOptions.onTransactionStatusChanged = async (txid: string, status: string) => {
      opts.onTransactionStatusChanged?.(txid, status)
    }
  }
  // Resume the event stream where it left off, so a phone that has been in a
  // pocket for a day does not replay every event since it was last online.
  const SSE_KEY = 'sse_last_event_id'
  monitorOptions.loadLastSSEEventId = () => storage.getKeyValue(SSE_KEY)
  monitorOptions.saveLastSSEEventId = (id: string) => storage.setKeyValue(SSE_KEY, id)

  const monitor = new Monitor(monitorOptions)
  monitor.addDefaultTasks()
  void monitor.startTasks()

  return {
    wallet: wallet as unknown as WalletInterface,
    storage,
    monitor,
    identityKey,
    close: async () => {
      monitor.stopTasks()
      await storage.destroy()
    }
  }
}
