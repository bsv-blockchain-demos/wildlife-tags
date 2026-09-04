/**
 * Which deployment this device talks to.
 *
 * A tag's QR code carries its own origin, so a finder never has to configure
 * anything: scanning points the app at whichever DNR runs that tag. This exists
 * for the tagger side, which has to sign in before it has scanned anything, and
 * for a device pointed at a test deployment.
 */
import AsyncStorage from '@react-native-async-storage/async-storage'

import * as api from './api'
import type { WalletChain } from './types'

const CHAIN_KEY = 'wildtag.chain'

/**
 * chain is the network this phone's wallet is built for.
 *
 * It is the deployment's answer, not a preference: a wallet built for the wrong
 * chain cannot verify a payment made on the right one, because the merkle proof
 * is checked against headers from a chain the transaction was never on. So the
 * value is whatever the server last said, cached because the wallet is built at
 * launch and the server may not be reachable then.
 *
 * "test" is the default for a phone that has never reached a deployment. It is
 * the safe way to be wrong: a testnet wallet cannot spend or receive real
 * money, and the value is corrected the first time the app talks to a server.
 */
export async function chain(): Promise<WalletChain> {
  return ((await AsyncStorage.getItem(CHAIN_KEY)) as WalletChain | null) ?? 'test'
}

const ARCADE_KEY = 'wildtag.arcade'

/**
 * arcadeURL is the transaction oracle this phone's wallet verifies against.
 *
 * Cached for the same reason as the chain: the wallet is built at launch, and
 * on teratestnet the toolbox's default providers are dead hosts -- so a wallet
 * built without this has no chaintracker and no proof source, and refuses every
 * incoming payment. See src/wallet/arcade.ts.
 */
export async function arcadeURL(): Promise<string> {
  return (await AsyncStorage.getItem(ARCADE_KEY)) ?? ''
}

/**
 * learnDeployment records what the deployment says about itself, and reports
 * whether anything the wallet is built from changed.
 *
 * A change means the wallet in memory is verifying against the wrong chain, or
 * against nothing at all. That is a different database and a different balance,
 * so it is surfaced rather than done silently.
 */
export async function learnDeployment(): Promise<{
  chain: WalletChain
  arcadeURL: string
  changed: boolean
}> {
  const info = await api.info()
  const [previousChain, previousArcade] = await Promise.all([chain(), arcadeURL()])

  const changed = info.wallet_chain !== previousChain || info.arcade_url !== previousArcade
  if (changed) {
    await AsyncStorage.multiSet([
      [CHAIN_KEY, info.wallet_chain],
      [ARCADE_KEY, info.arcade_url]
    ])
  }
  return { chain: info.wallet_chain, arcadeURL: info.arcade_url, changed }
}

/**
 * adopt points the app at the deployment a scanned tag came from.
 *
 * Only when nothing is configured yet. A tag from another DNR must not
 * silently repoint a signed-in tagger's console at a server they did not
 * choose -- that is how a session token ends up sent somewhere it should not
 * go.
 */
export async function adopt(origin: string): Promise<void> {
  if (!api.server()) await api.setServer(origin)
}
