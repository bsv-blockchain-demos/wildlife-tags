// A minimal BRC-100 wallet client.
//
// Hand-rolled rather than reaching into the vendored @bsv/sdk, following the
// same reasoning as rule-110-arcade: what we need from a wallet is five calls,
// and five calls do not justify wiring a client library through a page that is
// already loading a bundle for a different job. (The bundle is there for
// redeem.js, which has to build and sign a transaction; see vendor/README.md.)
//
// Two substrates, tried in order:
//   window.CWI          an injected provider, which is what BSV Browser gives us
//   http://localhost:3321  the loopback HTTP substrate desktop wallets expose
(function (global) {
  'use strict';

  const HTTP_SUBSTRATE = 'http://localhost:3321';

  let substrate = null;

  async function detect() {
    if (substrate) return substrate;

    if (global.CWI && typeof global.CWI.getPublicKey === 'function') {
      substrate = { kind: 'injected', call: injectedCall };
      return substrate;
    }
    try {
      // A short timeout on purpose: a crabber on a phone has no desktop wallet
      // listening, and making them wait for a TCP timeout before we tell them
      // so is the wrong first impression.
      const controller = new AbortController();
      const timer = setTimeout(() => controller.abort(), 1500);
      const res = await fetch(`${HTTP_SUBSTRATE}/getVersion`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json', Originator: location.host },
        body: '{}',
        signal: controller.signal,
      });
      clearTimeout(timer);
      if (res.ok) {
        substrate = { kind: 'http', call: httpCall };
        return substrate;
      }
    } catch (_) {
      // No loopback wallet. Fall through.
    }
    return null;
  }

  async function injectedCall(method, args) {
    if (typeof global.CWI[method] !== 'function') {
      throw new Error(`this wallet does not support ${method}`);
    }
    return global.CWI[method](args);
  }

  async function httpCall(method, args) {
    const res = await fetch(`${HTTP_SUBSTRATE}/${method}`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json', Originator: location.host },
      body: JSON.stringify(args || {}),
    });
    const text = await res.text();
    let body;
    try {
      body = text ? JSON.parse(text) : {};
    } catch (_) {
      throw new Error(`wallet returned unreadable output from ${method}`);
    }
    if (!res.ok) throw new Error(body.description || body.error || `wallet refused ${method}`);
    return body;
  }

  async function call(method, args) {
    const s = await detect();
    if (!s) {
      throw new Error(
        'No BSV wallet found. Open this page in BSV Browser, or run a BRC-100 wallet on this device.'
      );
    }
    return s.call(method, args);
  }

  const wallet = {
    available: async () => (await detect()) !== null,

    kind: async () => {
      const s = await detect();
      return s ? s.kind : null;
    },

    // identityKey is who the crabber is, as far as the chain is concerned. It
    // is where their reward gets derived to.
    identityKey: async () => {
      const res = await call('getPublicKey', { identityKey: true });
      return res.publicKey;
    },

    // derivePublicKey asks the wallet for a type-42 child key.
    //
    // This is the call the redemption page's safety check is built on: the
    // crabber's own wallet derives the key the payment output should be locked
    // to, so the page can confirm the server built an honest transaction
    // without any private key ever entering the page.
    derivePublicKey: async ({ protocolID, keyID, counterparty, forSelf }) => {
      const res = await call('getPublicKey', {
        protocolID,
        keyID,
        counterparty,
        forSelf: forSelf !== false,
      });
      return res.publicKey;
    },

    // createSignature attests to a record. The signature names the crabber, so
    // the on-chain record says who reported the catch rather than merely that
    // somebody did.
    createSignature: async ({ protocolID, keyID, counterparty, data }) => {
      const res = await call('createSignature', {
        protocolID,
        keyID,
        counterparty,
        data: Array.from(data),
      });
      return res.signature;
    },

    // internalizeAction is how the money becomes visible in the crabber's
    // balance. Until this runs, the payment exists on chain and their wallet
    // has no idea it is theirs.
    internalizeAction: async ({ tx, outputIndex, derivationPrefix, derivationSuffix, senderIdentityKey, description }) =>
      call('internalizeAction', {
        tx: Array.from(tx),
        description,
        labels: ['wildtag'],
        outputs: [
          {
            outputIndex,
            protocol: 'wallet payment',
            paymentRemittance: { derivationPrefix, derivationSuffix, senderIdentityKey },
          },
        ],
      }),
  };

  global.Wallet = wallet;

  if (typeof module !== 'undefined' && module.exports) {
    module.exports = wallet;
  }
})(typeof window !== 'undefined' ? window : globalThis);
