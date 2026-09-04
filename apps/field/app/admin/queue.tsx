/**
 * The outbox.
 *
 * Everything here was signed where the animal was, so the position and the
 * timestamp are already fixed and cannot drift. What is left is the half that
 * genuinely needs a server: funding the transaction and adding DNR's signature.
 *
 * A failure that will fail identically forever -- the wrong species, a tag
 * already redeemed, a record past its window -- is dropped rather than retried,
 * with the reason shown. A queue that keeps re-attempting a permanent refusal
 * is a queue that never drains and an error nobody can clear.
 */
import { useCallback, useEffect, useState } from 'react'
import { useRouter } from 'expo-router'
import { View } from 'react-native'

import { Banner, Button, Card, H1, H2, Mono, Note, P, Screen } from '../../src/ui/atoms'
import { useWallet } from '../../src/wallet/WalletProvider'
import * as api from '../../src/wildtag/api'
import * as queue from '../../src/wildtag/queue'
import { decodeSecret } from '../../src/wildtag/tagkey'
import { claimPending, receive, signTagInput } from '../../src/wildtag/redeem'
import { Transaction } from '@bsv/sdk'

export default function Queue() {
  const router = useRouter()
  const { wallet, identityKey } = useWallet()
  const [items, setItems] = useState<queue.QueuedItem[]>([])
  const [payments, setPayments] = useState<queue.PendingPayment[]>([])
  const [busy, setBusy] = useState(false)
  const [log, setLog] = useState<string[]>([])

  const refresh = useCallback(async () => {
    const [queued, payments] = await Promise.all([queue.pending(), queue.pendingPayments()])
    setItems(queued)
    setPayments(payments)
  }, [])

  useEffect(() => {
    void refresh()
  }, [refresh])

  const sendAll = async () => {
    if (!wallet || !identityKey) return
    setBusy(true)
    setLog([])

    // Payments first: these are coins already on chain that this wallet has not
    // been able to credit, usually because their block had not arrived yet.
    // Nothing has to go out over the wire to fix that except a proof lookup.
    const credited = await claimPending(wallet)
    if (credited > 0) setLog((l) => [...l, `credited ${credited} payment${credited === 1 ? '' : 's'}`])

    for (const item of await queue.pending()) {
      try {
        await send(item, wallet, identityKey)
        await queue.done(item.id)
        setLog((l) => [...l, `${item.tagID}: sent`])
      } catch (err) {
        const message = err instanceof Error ? err.message : String(err)
        // Only a transient failure is worth keeping. Anything the server will
        // refuse identically next time is dropped, so the reason reaches the
        // person once instead of forever.
        const keep = err instanceof api.ApiError ? err.retryable : true
        await queue.failed(item.id, message, keep)
        setLog((l) => [...l, `${item.tagID}: ${keep ? 'will retry' : 'refused'} — ${message}`])
      }
    }
    await refresh()
    setBusy(false)
  }

  return (
    <Screen>
      <Card>
        <H1>Waiting to send</H1>
        {items.length === 0 && payments.length === 0 ? (
          <P>Nothing is waiting. Everything captured on this phone has been sent.</P>
        ) : items.length === 0 ? (
          <>
            <P>
              {payments.length} payment{payments.length === 1 ? ' is' : 's are'} waiting for a
              block. The satoshis are already yours — the transaction is signed and broadcast — but
              your wallet cannot show them until the block that carries it arrives.
            </P>
            <Button title="Try again" onPress={sendAll} busy={busy} disabled={!wallet} />
          </>
        ) : (
          <>
            <P>
              {items.length} {items.length === 1 ? 'record was' : 'records were'} captured out of
              range. Each was signed where the animal was, so the position and time in it cannot
              change.
            </P>
            <Button title="Send them now" onPress={sendAll} busy={busy} disabled={!wallet} />
          </>
        )}
      </Card>

      {log.length > 0 ? (
        <Card>
          <H2>What happened</H2>
          {log.map((line, i) => (
            <Note key={i}>{line}</Note>
          ))}
        </Card>
      ) : null}

      {payments.map((p) => (
        <Card key={`pay-${p.id}`}>
          <H2>Payment · {p.tagID}</H2>
          <P>{p.satoshis.toLocaleString()} sats, already yours on chain</P>
          <Mono>{p.txid}</Mono>
          <Note>
            Waiting for the block that carries it. {p.attempts > 0 ? `${p.attempts} attempts.` : ''}
          </Note>
        </Card>
      ))}

      {items.map((item) => (
        <Card key={item.id}>
          <H2>
            {item.kind === 'report' ? 'Report' : 'Tagging'} · {item.tagID}
          </H2>
          <Note>
            Caught {new Date(item.observation.at).toLocaleString()} at{' '}
            {item.observation.lat.toFixed(5)}, {item.observation.lon.toFixed(5)}
          </Note>
          <Note>Species {item.observation.species}</Note>
          {item.attempts > 0 ? (
            <Banner tone="warn">
              {item.attempts} {item.attempts === 1 ? 'attempt' : 'attempts'}: {item.lastError}
            </Banner>
          ) : null}
          <Button
            title="Discard this one"
            onPress={async () => {
              await queue.discard(item.id)
              await refresh()
            }}
            tone="danger"
          />
        </Card>
      ))}

      <Card>
        <Button title="Back" onPress={() => router.back()} tone="quiet" />
      </Card>
    </Screen>
  )
}

/**
 * send submits one queued record.
 *
 * The signed bytes are replayed verbatim -- never rebuilt. Rebuilding could
 * produce different bytes from the ones that were signed, and the signature
 * would then verify against nothing.
 */
async function send(
  item: queue.QueuedItem,
  wallet: NonNullable<ReturnType<typeof useWallet>['wallet']>,
  identityKey: string
): Promise<void> {
  const o = item.observation

  if (item.kind === 'tagging') {
    await api.activate({
      tag_id: item.tagID,
      species: o.species,
      lat: o.lat,
      lon: o.lon,
      accuracy_m: o.accuracyM,
      meas: o.meas,
      attr: o.attr,
      name: o.name ?? '',
      observation: item.observationHex,
      attest_sig: item.attestSig,
      attest_pub: item.attestPub
    })
    return
  }

  const form: api.RecaptureForm = {
    tag_id: item.tagID,
    lat: o.lat,
    lon: o.lon,
    accuracy_m: o.accuracyM,
    meas: o.meas,
    attr: o.attr,
    payee: item.attestPub,
    name: o.name ?? '',
    observation: item.observationHex,
    attest_sig: item.attestSig,
    attest_pub: item.attestPub
  }
  const draft = await api.prepareRedeem(form)
  const tx = Transaction.fromHexBEEF(draft.signable_tx)

  // The payout check runs here too. A queued report is submitted hours later
  // and the transaction is built at that moment, so it has had no more scrutiny
  // than a live one -- less, because nobody is watching the screen.
  const { verifyPayout } = await import('../../src/wildtag/redeem')
  await verifyPayout(wallet, tx, draft)

  const secret = decodeSecret(item.secret)
  const receipt = await api.completeRedeem(draft.reference, signTagInput(tx, draft.input_index, secret))
  // Same rule as the live path: if the wallet cannot credit it yet, the receipt
  // is kept rather than the report being counted as failed. The money moved.
  await receive(wallet, item.tagID, receipt)
  void identityKey
}
