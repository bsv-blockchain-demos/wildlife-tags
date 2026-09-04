/**
 * Where the mnemonic lives, and how the key comes back from it.
 *
 * expo-secure-store, which is the Keychain on iOS and Keystore-backed
 * EncryptedSharedPreferences on Android. Deliberately not AsyncStorage: that is
 * a plaintext file in the app's data directory, and bsv-desktop's
 * `keyMaterial.ts` writing the root key and mnemonic to localStorage is exactly
 * the mistake this app was told not to repeat.
 *
 * A lost mnemonic is lost satoshis, permanently, so the Shamir 2-of-3 recovery
 * shares from backupShares.ts exist from day one rather than "later".
 */
import * as SecureStore from 'expo-secure-store'

import { generateMnemonicWallet, recoverMnemonicWallet, type MnemonicWalletResult } from './mnemonicWallet'

const MNEMONIC_KEY = 'wildtag.mnemonic'
const BACKED_UP_KEY = 'wildtag.mnemonic.backedUp'

const options: SecureStore.SecureStoreOptions = {
  // Available after first unlock rather than while unlocked: the wallet has a
  // background monitor that promotes a transaction when its proof arrives, and
  // a key readable only with the screen on would leave payments stuck until
  // somebody happened to open the app.
  keychainAccessible: SecureStore.AFTER_FIRST_UNLOCK
}

export async function hasWallet(): Promise<boolean> {
  return (await SecureStore.getItemAsync(MNEMONIC_KEY, options)) !== null
}

/** create makes a new wallet and stores its mnemonic. Refuses to overwrite. */
export async function create(): Promise<MnemonicWalletResult> {
  if (await hasWallet()) {
    throw new Error('This device already has a wallet. Recover or reset it deliberately.')
  }
  const wallet = generateMnemonicWallet()
  await SecureStore.setItemAsync(MNEMONIC_KEY, wallet.mnemonic, options)
  return wallet
}

/** restore replaces the stored mnemonic with a supplied one. */
export async function restore(mnemonic: string): Promise<MnemonicWalletResult> {
  const wallet = recoverMnemonicWallet(mnemonic)
  await SecureStore.setItemAsync(MNEMONIC_KEY, wallet.mnemonic, options)
  await SecureStore.deleteItemAsync(BACKED_UP_KEY, options)
  return wallet
}

/** load returns the stored wallet, or null if this device has none yet. */
export async function load(): Promise<MnemonicWalletResult | null> {
  const mnemonic = await SecureStore.getItemAsync(MNEMONIC_KEY, options)
  if (!mnemonic) return null
  return recoverMnemonicWallet(mnemonic)
}

/** mnemonic returns the words, for the one screen that shows them. */
export async function mnemonic(): Promise<string | null> {
  return SecureStore.getItemAsync(MNEMONIC_KEY, options)
}

export async function markBackedUp(): Promise<void> {
  await SecureStore.setItemAsync(BACKED_UP_KEY, new Date().toISOString(), options)
}

export async function backedUpAt(): Promise<string | null> {
  return SecureStore.getItemAsync(BACKED_UP_KEY, options)
}

/**
 * forget erases the wallet from this device.
 *
 * Irreversible without the mnemonic or two recovery shares, which is why every
 * caller has to say so explicitly rather than reaching a confirm dialog.
 */
export async function forget(): Promise<void> {
  await SecureStore.deleteItemAsync(MNEMONIC_KEY, options)
  await SecureStore.deleteItemAsync(BACKED_UP_KEY, options)
}
