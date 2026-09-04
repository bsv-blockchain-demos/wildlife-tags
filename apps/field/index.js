/**
 * Root entry point.
 *
 * react-native-quick-crypto must be installed via JSI BEFORE any @bsv/sdk
 * import runs. The SDK's Random.js probes for a crypto implementation at module
 * load time, in this order:
 *
 *   globalThis.crypto.getRandomValues
 *   self.crypto.getRandomValues
 *   window.crypto.getRandomValues
 *   process.require('crypto').randomBytes()
 *
 * React Native has none of those by default, so an import ordered even one line
 * wrong yields a wallet that cannot generate a key -- and the failure surfaces
 * far from its cause. Lifted from bsv-browser, where this took days to get
 * right; see ATTRIBUTION.md.
 */
import { install } from 'react-native-quick-crypto'

install() // sets global.Buffer and global.crypto

if (typeof globalThis === 'undefined') {
  global.globalThis = global
}

if (global.crypto && typeof global.crypto.getRandomValues === 'function') {
  // Propagate the one real implementation to every name the SDK might probe.
  if (typeof globalThis !== 'undefined') globalThis.crypto = global.crypto
  if (typeof global.self === 'undefined') global.self = global
  else if (!global.self.crypto) global.self.crypto = global.crypto
  if (typeof global.window === 'undefined') global.window = global
  else if (!global.window.crypto) global.window.crypto = global.crypto
} else {
  // Loud, because everything downstream of this quietly produces wrong keys.
  console.error('[crypto] quick-crypto did not install; the wallet cannot generate keys safely')
}

import 'expo-router/entry'
