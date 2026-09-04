const { getDefaultConfig } = require('expo/metro-config')
const path = require('path')

const config = getDefaultConfig(__dirname)

// The canonical encoder is NOT copied into this app.
//
// Every signature this application makes is over bytes an observation becomes,
// and the server rebuilds those bytes to verify. Two implementations of that
// encoding is the exact drift that broke three separate things in this project
// already, so there is one file and both sides use it: the web pages load it as
// a <script>, and Metro resolves it from here.
//
// watchFolders is what makes a file outside the project root resolvable at all;
// without it Metro refuses to serve the module even with an explicit alias.
const shared = path.resolve(__dirname, '../../internal/web/static')
config.watchFolders = [...(config.watchFolders || []), shared]

config.resolver.extraNodeModules = {
  'wildtag-canonical': path.join(shared, 'canonical.js'),
  crypto: require.resolve('react-native-quick-crypto'),
  stream: require.resolve('stream-browserify'),
  buffer: require.resolve('buffer'),
  ...config.resolver.extraNodeModules
}

const emptyShim = path.resolve(__dirname, 'metro-shims/empty.js')
const quickCryptoMain = require.resolve('react-native-quick-crypto')

const upstream = config.resolver.resolveRequest
config.resolver.resolveRequest = (context, moduleName, platform) => {
  // node:crypto → quick-crypto, so the SDK gets native SHA-256, PBKDF2 and
  // AES-GCM rather than a JS fallback that is slow enough to be noticeable
  // while a phone is signing on a boat.
  if (moduleName === 'node:crypto') {
    return { type: 'sourceFile', filePath: quickCryptoMain }
  }
  if (moduleName === 'node:buffer' || moduleName === 'node:process') {
    return { type: 'sourceFile', filePath: emptyShim }
  }
  if (moduleName === 'wildtag-canonical') {
    return { type: 'sourceFile', filePath: path.join(shared, 'canonical.js') }
  }
  if (typeof upstream === 'function') return upstream(context, moduleName, platform)
  return context.resolveRequest(context, moduleName, platform)
}

module.exports = config
