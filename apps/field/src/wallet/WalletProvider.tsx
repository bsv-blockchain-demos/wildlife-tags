/**
 * The wallet, for the whole app.
 *
 * One wallet per device, built once and kept: it owns a SQLite database and a
 * background monitor, and building a second would mean two processes writing
 * the same file and a balance that disagrees with itself.
 *
 * The wallet is built eagerly on launch when this device already has a
 * mnemonic, because the monitor is what turns a broadcast reward into a proven
 * one. A finder who is paid and then closes the app should come back to a
 * settled balance, not a pending one.
 */
import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useRef,
  useState,
  type ReactNode
} from 'react'
import type { WalletInterface } from '@bsv/sdk'

import { build, type BuiltWallet, type Chain } from './buildWallet'
import * as keys from './keys'

export interface WalletState {
  /** null until a wallet exists on this device and has finished building. */
  wallet: WalletInterface | null
  identityKey: string | null
  building: boolean
  error: string | null
  /** Bumped whenever a transaction changes status, to re-read the balance. */
  version: number

  create: () => Promise<string>
  restore: (mnemonic: string) => Promise<void>
  forget: () => Promise<void>
  balance: () => Promise<number>
}

const Ctx = createContext<WalletState>({
  wallet: null,
  identityKey: null,
  building: false,
  error: null,
  version: 0,
  create: async () => '',
  restore: async () => {},
  forget: async () => {},
  balance: async () => 0
})

export const useWallet = () => useContext(Ctx)

export function WalletProvider({
  chain,
  arcadeURL,
  children
}: {
  chain: Chain
  /** The deployment's transaction oracle; see src/wallet/arcade.ts. */
  arcadeURL: string
  children: ReactNode
}) {
  const built = useRef<BuiltWallet | null>(null)
  const [wallet, setWallet] = useState<WalletInterface | null>(null)
  const [identityKey, setIdentityKey] = useState<string | null>(null)
  const [building, setBuilding] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [version, setVersion] = useState(0)

  const open = useCallback(
    async (primaryKey: number[]) => {
      setBuilding(true)
      setError(null)
      try {
        const b = await build({
          primaryKey,
          chain,
          arcadeURL,
          onTransactionStatusChanged: () => setVersion((v) => v + 1)
        })
        built.current = b
        setWallet(b.wallet)
        setIdentityKey(b.identityKey)
      } catch (err) {
        setError(err instanceof Error ? err.message : String(err))
      } finally {
        setBuilding(false)
      }
    },
    [chain, arcadeURL]
  )

  // Build the wallet at launch, making one if this device has none.
  //
  // Creating it eagerly rather than behind a "set up your wallet" step is
  // deliberate. The person this app is for found a tag on an animal and wants
  // to be paid; making them understand and provision a wallet first is a step
  // most of them will not finish, and the reward stops being an incentive
  // exactly the way a posted cheque does. A wallet is a key -- it costs nothing
  // to make and nothing to hold.
  //
  // What it does cost is that the key now matters: lose the phone with no
  // backup and the satoshis are gone. The wallet screen says so loudly and
  // offers the recovery sheets, which is the honest place for that
  // conversation -- after somebody has money, not before they have any.
  useEffect(() => {
    let cancelled = false
    void (async () => {
      const existing = (await keys.load()) ?? (await keys.create())
      if (cancelled) return
      await open(existing.primaryKey)
    })()
    return () => {
      cancelled = true
      // Stop the monitor rather than leaving an SSE connection and a timer
      // running behind a torn-down tree.
      void built.current?.close()
      built.current = null
    }
  }, [open])

  const value = useMemo<WalletState>(
    () => ({
      wallet,
      identityKey,
      building,
      error,
      version,
      create: async () => {
        const made = await keys.create()
        await open(made.primaryKey)
        return made.mnemonic
      },
      restore: async (mnemonic: string) => {
        await built.current?.close()
        built.current = null
        setWallet(null)
        const restored = await keys.restore(mnemonic)
        await open(restored.primaryKey)
      },
      forget: async () => {
        await built.current?.close()
        built.current = null
        setWallet(null)
        setIdentityKey(null)
        await keys.forget()
      },
      balance: async () => {
        if (!wallet) return 0
        const res = await wallet.listOutputs({ basket: 'default', limit: 1000 })
        return res.outputs.reduce((sum, o) => sum + (o.spendable ? o.satoshis : 0), 0)
      }
    }),
    [wallet, identityKey, building, error, version, open]
  )

  return <Ctx.Provider value={value}>{children}</Ctx.Provider>
}
