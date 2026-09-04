/**
 * The wallet screen: balance, identity, and the two ways to not lose it.
 *
 * A lost mnemonic is lost satoshis, permanently, and no amount of contacting
 * DNR gets them back -- which is the point of paying on chain, and also the
 * responsibility that comes with it. So the backup is offered before the
 * balance rather than buried in a settings menu, and the screen says plainly
 * what happens if it is skipped.
 */
import { useCallback, useEffect, useState } from 'react'
import * as Print from 'expo-print'
import { useRouter } from 'expo-router'
import { View } from 'react-native'

import { Banner, Button, Card, H1, H2, Mono, Note, P, Screen } from '../src/ui/atoms'
import { generateBackupShares, generatePrintHTML } from '../src/wallet/backupShares'
import * as keys from '../src/wallet/keys'
import { useWallet } from '../src/wallet/WalletProvider'
import { formatMnemonicForDisplay } from '../src/wallet/mnemonicWallet'

export default function WalletScreen() {
  const router = useRouter()
  const { identityKey, building, error, version, create, balance } = useWallet()
  const [sats, setSats] = useState<number | null>(null)
  const [words, setWords] = useState<string[] | null>(null)
  const [backedUp, setBackedUp] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)
  const [note, setNote] = useState<string | null>(null)

  const refresh = useCallback(async () => {
    if (!identityKey) return
    setSats(await balance())
    setBackedUp(await keys.backedUpAt())
  }, [identityKey, balance])

  useEffect(() => {
    void refresh()
  }, [refresh, version])

  const make = async () => {
    setBusy(true)
    setNote(null)
    try {
      const mnemonic = await create()
      setWords(formatMnemonicForDisplay(mnemonic))
    } catch (err) {
      setNote(err instanceof Error ? err.message : String(err))
    } finally {
      setBusy(false)
    }
  }

  const reveal = async () => {
    const mnemonic = await keys.mnemonic()
    if (mnemonic) setWords(formatMnemonicForDisplay(mnemonic))
  }

  /**
   * Recovery shares, printed.
   *
   * Shamir 2-of-3: any two of the three sheets rebuild the key, and any one on
   * its own reveals nothing. That is the shape that survives a boat, a house
   * fire and a filing cabinet without any single sheet being a liability.
   */
  const printShares = async () => {
    setBusy(true)
    setNote(null)
    try {
      const mnemonic = await keys.mnemonic()
      if (!mnemonic || !identityKey) throw new Error('There is no wallet on this phone yet.')
      const wallet = await keys.load()
      const shares = generateBackupShares(wallet!.primaryKey, 3, 2)
      const html = await generatePrintHTML(shares, identityKey)
      await Print.printAsync({ html })
      await keys.markBackedUp()
      setBackedUp(await keys.backedUpAt())
    } catch (err) {
      setNote(err instanceof Error ? err.message : String(err))
    } finally {
      setBusy(false)
    }
  }

  if (!identityKey) {
    return (
      <Screen>
        <Card>
          <H1>No wallet on this phone</H1>
          <P>
            Rewards are paid to a wallet this phone owns. Nobody else can spend from it — not DNR,
            not us — which also means nobody else can recover it for you.
          </P>
          <P>One is normally made the first time the app opens; this one has been erased.</P>
          {error ? <Banner tone="bad">{error}</Banner> : null}
          {note ? <Banner tone="bad">{note}</Banner> : null}
          <Button title="Make a wallet" onPress={make} busy={busy || building} />
        </Card>
        {words ? <Words words={words} /> : null}
      </Screen>
    )
  }

  return (
    <Screen>
      <Card>
        <H1>{sats === null ? '—' : sats.toLocaleString()} sats</H1>
        <Note>Identity key</Note>
        <Mono>{identityKey}</Mono>
        <Button title="Refresh" onPress={() => void refresh()} tone="quiet" />
      </Card>

      <Card>
        <H2>If you lose this phone</H2>
        {backedUp ? (
          <Banner tone="good">Recovery sheets printed {new Date(backedUp).toLocaleDateString()}.</Banner>
        ) : (
          <Banner tone="warn">
            This wallet has no backup. If the phone is lost, the satoshis in it are gone — there is no
            reset, and no one to ask.
          </Banner>
        )}
        <P>
          Printing produces three sheets. Any two of them rebuild the wallet; any one on its own
          reveals nothing. Keep them apart.
        </P>
        {note ? <Banner tone="bad">{note}</Banner> : null}
        <View style={{ gap: 8 }}>
          <Button title="Print recovery sheets" onPress={printShares} busy={busy} />
          <Button title="Show the twelve words" onPress={() => void reveal()} tone="quiet" />
        </View>
      </Card>

      {words ? <Words words={words} /> : null}

      <Card>
        <Button title="Back" onPress={() => router.back()} tone="quiet" />
      </Card>
    </Screen>
  )
}

function Words({ words }: { words: string[] }) {
  return (
    <Card>
      <H2>Your twelve words</H2>
      <Banner tone="warn">
        Anyone who reads these owns the wallet. Write them down on paper; do not photograph them and
        do not type them into anything else.
      </Banner>
      <View style={{ flexDirection: 'row', flexWrap: 'wrap', gap: 8 }}>
        {words.map((w, i) => (
          <Mono key={`${w}-${i}`}>{`${i + 1}. ${w}`}</Mono>
        ))}
      </View>
    </Card>
  )
}
