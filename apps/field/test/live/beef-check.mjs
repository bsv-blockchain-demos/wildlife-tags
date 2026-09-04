// Does the BEEF the server just handed a finder actually verify?
//
// This is the whole bug: the payment is real and irreversible either way, but a
// wallet cannot credit a BEEF it cannot walk back to a proven transaction. Run
// against a live deployment right after a redemption.
import { Beef } from '@bsv/sdk'
import { Services } from '@bsv/wallet-toolbox-mobile'
import { install } from '../../src/wallet/arcade.ts'

const [, , server, txid] = process.argv
const info = await (await fetch(`${server}/api/info`)).json()
const services = new Services(info.wallet_chain)
install(services, info.arcade_url)

// Re-fetch the payment the way the app would see it in the receipt.
const arcadeTx = await (await fetch(`${info.arcade_url}/tx/${txid}`)).json()
console.log('payment status  ', arcadeTx.txStatus, 'height', arcadeTx.blockHeight ?? '-')

const beefHex = process.env.BEEF_HEX
if (!beefHex) {
  console.log('(no BEEF_HEX given; only reporting arcade status)')
  process.exit(0)
}
const beef = Beef.fromBinary(Buffer.from(beefHex, 'hex'))
console.log('txs in beef     ', beef.txs.map((t) => `${t.txid.slice(0, 12)} proof=${!!t.tx?.merklePath}`).join(', '))
const ok = await beef.verify(await services.getChainTracker(), false)
console.log('verify          ', ok, ok ? '-> the wallet credits this immediately' : '-> kept until a block arrives')
