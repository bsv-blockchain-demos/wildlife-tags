/**
 * The home screen: two jobs, and they are not the same job.
 *
 * Most people who open this app found a tag on an animal and want to be paid.
 * A much smaller number are DNR staff or authorised volunteers about to put a
 * tag on one. Those are different tasks with different consequences, so they
 * are separated at the top rather than mixed into one menu -- and the tagger
 * side stays behind a sign-in.
 */
import { useCallback, useEffect, useState } from 'react'
import { useFocusEffect, useRouter } from 'expo-router'
import { View } from 'react-native'

import { Banner, Button, Card, H1, H2, Note, P, Screen } from '../src/ui/atoms'
import { useWallet } from '../src/wallet/WalletProvider'
import * as api from '../src/wildtag/api'
import * as queue from '../src/wildtag/queue'
import { claimPending } from '../src/wildtag/redeem'

export default function Home() {
  const router = useRouter()
  const { wallet, identityKey, building, error } = useWallet()
  const [waiting, setWaiting] = useState(0)
  const [owed, setOwed] = useState(0)
  const [signedIn, setSignedIn] = useState(false)

  const refresh = useCallback(async () => {
    setWaiting(await queue.count())
    setOwed(await queue.pendingPaymentCount())
    setSignedIn(api.signedIn())
  }, [])

  useFocusEffect(
    useCallback(() => {
      void refresh()
    }, [refresh])
  )

  // A payment that could not be credited when it was made is retried whenever
  // the app opens with a wallet. The usual reason is simply that its block had
  // not arrived yet, and by now it has -- so this is the path that quietly
  // turns "paid, pending" into a balance without anybody being asked to do
  // anything.
  useEffect(() => {
    void (async () => {
      if (wallet) await claimPending(wallet)
      await refresh()
    })()
  }, [wallet, refresh])

  return (
    <Screen>
      <Card>
        <H1>Found a tagged animal?</H1>
        <P>
          Scan the QR code on the tag. You will see where the animal was tagged and how far it has
          travelled, and the reward goes straight into this phone&apos;s wallet.
        </P>
        <Button title="Scan a tag" onPress={() => router.push('/scan')} />
      </Card>

      {owed > 0 ? (
        <Card>
          <Banner tone="good">
            {owed} payment{owed === 1 ? ' is' : 's are'} yours on chain and waiting for a block
            before your wallet can show {owed === 1 ? 'it' : 'them'}.
          </Banner>
          <Button title="Check again" onPress={() => void refresh()} tone="quiet" />
        </Card>
      ) : null}

      {waiting > 0 ? (
        <Card>
          <Banner tone="warn">
            {waiting} {waiting === 1 ? 'report is' : 'reports are'} waiting for a signal. They were
            signed where you caught the animal, so the position and time are already fixed.
          </Banner>
          <Button title="Send them now" onPress={() => router.push('/admin/queue')} tone="quiet" />
        </Card>
      ) : null}

      <Card>
        <H2>Your wallet</H2>
        {building ? (
          <Note>Opening…</Note>
        ) : error ? (
          <Banner tone="bad">{error}</Banner>
        ) : identityKey ? (
          <Note>Identity {identityKey.slice(0, 16)}…</Note>
        ) : (
          <Note>No wallet on this phone yet. One is made the first time you need it.</Note>
        )}
        <Button title="Open wallet" onPress={() => router.push('/wallet')} tone="quiet" />
      </Card>

      <Card>
        <H2>Tagging an animal</H2>
        <P>
          For DNR staff and authorised volunteers. Arming a tag locks a reward on chain, so it needs
          a sign-in.
        </P>
        <View style={{ gap: 8 }}>
          <Button
            title={signedIn ? 'Arm a tag' : 'Sign in to tag'}
            onPress={() => router.push(signedIn ? '/admin/arm' : '/admin/login')}
            tone="quiet"
          />
          <Button title="Settings" onPress={() => router.push('/settings')} tone="quiet" />
        </View>
      </Card>
    </Screen>
  )
}
