// Runs the client half of two things Go then checks, so neither can drift.
//
// 1. The canonical encoder: the exact bytes an observation becomes, and the
//    attestation a wallet makes over them.
// 2. The tag-key signature over a redemption transaction, which the Go side
//    feeds to the real script interpreter.
//
// This exists because three separate bugs in this path reached a live
// deployment, each invisible from one side alone: a wrong derived key, a wrong
// key id, and a double hash. A signature is either accepted or it is not, and
// only running both languages against each other proves which.
const fs = require('fs');
const path = require('path');

const bsv = require(path.join(__dirname, '..', 'static', 'vendor', 'bsv-sdk.js'));
const Canonical = require(path.join(__dirname, '..', 'static', 'canonical.js'));
const { Transaction, PrivateKey, Hash, Utils, TransactionSignature, ProtoWallet } = bsv;

const fixture = JSON.parse(fs.readFileSync(process.argv[2], 'utf8'));

// Derive the tag key from the bearer secret, exactly as the page does from the
// URL fragment. Keep this in step with internal/tagkey.
function tagKeyFrom(secretBytes) {
  const prefix = Utils.toArray('wildtag-v1-key|', 'utf8');
  return new PrivateKey(Hash.sha256(prefix.concat(Array.from(secretBytes))));
}

function hexToBytes(h) {
  const out = new Uint8Array(h.length / 2);
  for (let i = 0; i < out.length; i++) out[i] = parseInt(h.substr(i * 2, 2), 16);
  return out;
}
const bytesToHex = (b) => Array.from(b, (x) => x.toString(16).padStart(2, '0')).join('');

// --- 1. the canonical observation and its attestation ------------------------

// The observation is built from the human values a form collects, not from
// pre-encoded fields, so the encoder's own scaling and rounding are under test
// rather than assumed.
const observationText = Canonical.encodeObservation(fixture.observation);
const observationBytes = Canonical.toBytes(observationText);

// Attest exactly as a BRC-100 wallet does: a BRC-42 child of the identity key,
// derived under (protocol, keyID, counterparty=anyone), signing the payload
// itself -- createSignature hashes what it is given once, so handing it a
// digest would sign sha256(sha256(payload)).
const attestKey = PrivateKey.fromHex(fixture.attestPrivHex);
const wallet = new ProtoWallet(attestKey);

async function attest() {
  const { signature } = await wallet.createSignature({
    data: observationBytes,
    protocolID: [2, 'wildtag observation'],
    keyID: fixture.tagID,
    counterparty: 'anyone',
  });
  return bytesToHex(new Uint8Array(signature));
}

// --- 2. the tag-key signature over the redemption ---------------------------

const tagKey = tagKeyFrom(hexToBytes(fixture.secretHex));
const tx = Transaction.fromHexBEEF(fixture.beefHex);
const inputIndex = fixture.inputIndex;

const scope = TransactionSignature.SIGHASH_ALL | TransactionSignature.SIGHASH_FORKID;
const input = tx.inputs[inputIndex];
const source = input.sourceTransaction.outputs[input.sourceOutputIndex];

const preimage = TransactionSignature.format({
  sourceTXID: input.sourceTXID ?? input.sourceTransaction.id('hex'),
  sourceOutputIndex: input.sourceOutputIndex,
  sourceSatoshis: source.satoshis,
  transactionVersion: tx.version,
  otherInputs: tx.inputs.filter((_, i) => i !== inputIndex),
  outputs: tx.outputs,
  inputIndex,
  subscript: source.lockingScript,
  inputSequence: input.sequence,
  lockTime: tx.lockTime,
  scope,
});

const raw = tagKey.sign(Hash.sha256(preimage));
const sig = new TransactionSignature(raw.r, raw.s, scope).toChecksigFormat();

attest().then((attestSigHex) => {
  process.stdout.write(JSON.stringify({
    observation: observationText,
    attestSigHex,
    attestPubKey: attestKey.toPublicKey().toString(),
    tagPubKey: tagKey.toPublicKey().toString(),
    sigHex: bytesToHex(new Uint8Array(sig)),
    sighashHex: bytesToHex(new Uint8Array(Hash.hash256(preimage))),
  }));
}).catch((err) => {
  process.stderr.write(String(err && err.stack ? err.stack : err));
  process.exit(1);
});
