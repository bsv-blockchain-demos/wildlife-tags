/**
 * Signing in to tag.
 *
 * A BRC-100 identity signature rather than a password, and the reason is field
 * ergonomics as much as security: a tagger is standing in a boat with wet hands
 * and an animal in one of them. It also gives the dataset something a shared
 * password cannot -- every activation carries an attestation naming the
 * identity key that made it, so a record is attributable to a person rather
 * than to whoever was logged in.
 *
 * The password fallback exists because an identity-only system cannot be
 * administered from a device whose wallet is not set up yet, and a recovery
 * path that does not exist when you need it is not a design.
 *
 * Note the role this unlocks is *tagger*, which the fish programme shows is
 * often an authorised volunteer rather than DNR staff. It is deliberately not
 * the same thing as minting batches or sweeping rewards.
 */
import { useEffect, useState } from 'react'
import { Utils } from '@bsv/sdk'
import { useRouter } from 'expo-router'
import { View } from 'react-native'

import { Banner, Button, Card, Field, H2, Input, Note, P, Screen } from '../../src/ui/atoms'
import { useWallet } from '../../src/wallet/WalletProvider'
import * as api from '../../src/wildtag/api'
import type { ServerInfo } from '../../src/wildtag/types'

export default function Login() {
  const router = useRouter()
  const { wallet, identityKey } = useWallet()
  const [info, setInfo] = useState<ServerInfo | null>(null)
  const [password, setPassword] = useState('')
  const [error, setError] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)

  useEffect(() => {
    void (async () => {
      try {
        setInfo(await api.info())
      } catch (err) {
        setError(err instanceof Error ? err.message : String(err))
      }
    })()
  }, [])

  const withWallet = async () => {
    if (!wallet || !identityKey || !info) return
    setBusy(true)
    setError(null)
    try {
      const c = await api.challenge()
      // Sign the nonce under the admin protocol with the *server* as
      // counterparty. The server derives the same type-42 child from its own
      // private key and this identity key, so the signature verifies with no
      // shared secret -- and is useless to any other server, because a
      // different counterparty derives a different key.
      const { signature } = await wallet.createSignature({
        protocolID: [c.security_level as 2, c.protocol],
        keyID: c.nonce,
        counterparty: info.identity_key,
        data: Utils.toArray(c.nonce, 'utf8')
      })
      await api.loginWithIdentity(
        identityKey,
        c.nonce,
        Array.from(signature, (b) => b.toString(16).padStart(2, '0')).join('')
      )
      router.replace('/admin/arm')
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    } finally {
      setBusy(false)
    }
  }

  const withPassword = async () => {
    setBusy(true)
    setError(null)
    try {
      await api.loginWithPassword(password)
      router.replace('/admin/arm')
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    } finally {
      setBusy(false)
    }
  }

  return (
    <Screen>
      <Card>
        <H2>Sign in to tag</H2>
        <P>
          Arming a tag locks a reward on chain and writes a record under your name. Both need a
          sign-in.
        </P>
        {error ? <Banner tone="bad">{error}</Banner> : null}
        {!api.server() ? (
          <Banner tone="warn">No deployment is configured yet. Set one in Settings first.</Banner>
        ) : null}

        <View style={{ gap: 8 }}>
          <Button
            title="Sign in with this phone's wallet"
            onPress={withWallet}
            disabled={!wallet || !info}
            busy={busy}
          />
          <Note>
            Signs a one-time challenge with your identity key. Nothing secret leaves this phone.
          </Note>
        </View>
      </Card>

      {info?.password_login ? (
        <Card>
          <H2>Or a password</H2>
          <Field label="Operator password">
            <Input
              value={password}
              onChangeText={setPassword}
              secureTextEntry
              autoCapitalize="none"
              autoCorrect={false}
            />
          </Field>
          <Button title="Sign in" onPress={withPassword} busy={busy} tone="quiet" />
          <Note>
            A shared password signs records as &quot;operator&quot; rather than as you, and the
            dataset says so.
          </Note>
        </Card>
      ) : null}
    </Screen>
  )
}
