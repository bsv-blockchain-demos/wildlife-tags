import { Beef, MerklePath, Transaction } from '@bsv/sdk'
import { Services } from '@bsv/wallet-toolbox-mobile'
import { ArcadeChainTracker, install } from '../../src/wallet/arcade.ts'

const ARCADE = 'https://arcade-v2-ttn-us-1.bsvblockchain.tech'
const MINED_PAYMENT = 'b8243b9c5f4ec0b92415b1202c180922e0d6c49a23b95996a1343c79693ec972'

const tracker = new ArcadeChainTracker(ARCADE)
const height = await tracker.currentHeight()
console.log('1. currentHeight            ', height)

// A root the chain really has, and one it does not.
const tip = await (await fetch(`${ARCADE}/chaintracks/v2/tip`)).json()
console.log('2. isValidRootForHeight(ok) ', await tracker.isValidRootForHeight(tip.merkleRoot, tip.height))
console.log('   isValidRootForHeight(no) ', await tracker.isValidRootForHeight('00'.repeat(32), tip.height))

// The services, with everything third-party removed.
const services = new Services('teratest')
install(services, ARCADE)
for (const k of ['getMerklePathServices', 'getRawTxServices', 'getStatusForTxidsServices', 'postBeefServices']) {
  console.log(`3. ${k.padEnd(26)}`, services[k].services.map((s) => s.name).join(', '))
}

// A proof for a transaction we know is mined.
const mp = await services.getMerklePathServices.services[0].service(MINED_PAYMENT, services)
console.log('4. getMerklePath            ', mp.merklePath ? `BUMP at height ${mp.merklePath.blockHeight}` : `none (${JSON.stringify(mp.notes)})`)

// And the whole point: a payment BEEF that a wallet will accept.
const info = await (await fetch(`${ARCADE}/tx/${MINED_PAYMENT}`)).json()
let tx
try { tx = Transaction.fromHexEF(info.rawTx) } catch { tx = Transaction.fromHex(info.rawTx) }
tx.merklePath = MerklePath.fromHex(info.merklePath)

const beef = new Beef()
beef.mergeTransaction(tx)
const atomic = beef.toBinaryAtomic(MINED_PAYMENT)
const parsed = Beef.fromBinary(atomic)
const ok = await parsed.verify(await services.getChainTracker(), false)
console.log('5. verify(arcade tracker)   ', ok, ok ? '-> internalizeAction accepts this' : '-> still rejected')

// --- the header client the monitor uses -------------------------------------
import { ArcadeChaintracks } from '../../src/wallet/arcade.ts'

const ct = new ArcadeChaintracks(ARCADE)
const ctTip = await ct.findChainTipHeader()
console.log('6. findChainTipHeader       ', `height ${ctTip.height} hash ${ctTip.hash.slice(0, 16)}…`)

const byHeight = await ct.findHeaderForHeight(ctTip.height)
const fieldsMatch =
  byHeight.hash === ctTip.hash &&
  byHeight.merkleRoot === ctTip.merkleRoot &&
  byHeight.previousHash === ctTip.previousHash &&
  byHeight.version === ctTip.version &&
  byHeight.time === ctTip.time &&
  byHeight.bits === ctTip.bits &&
  byHeight.nonce === ctTip.nonce
console.log('7. findHeaderForHeight      ', fieldsMatch ? 'every field matches the JSON tip' : 'MISMATCH')
if (!fieldsMatch) {
  console.log('   from bytes:', JSON.stringify(byHeight))
  console.log('   from json :', JSON.stringify(ctTip))
}

const byHash = await ct.findHeaderForBlockHash(ctTip.hash)
console.log('8. findHeaderForBlockHash   ', byHash?.height === ctTip.height ? 'found the tip' : 'NOT FOUND')
console.log('9. getPresentHeight         ', await ct.getPresentHeight())
