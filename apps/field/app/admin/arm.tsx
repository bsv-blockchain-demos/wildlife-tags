/**
 * Arming a tag.
 *
 * The order of the fields is the order the work happens in: pick the tag,
 * measure the animal, then take the fix last because it is the slowest step and
 * the animal is in one hand throughout.
 *
 * Two round trips to the server, and that is not an accident. The record is
 * signed by the tagger's own wallet, which will only sign bytes it has been
 * handed -- so the server assembles the canonical observation, the wallet signs
 * exactly those bytes, and only then is the tag armed. Letting the server sign
 * instead would make every activation attributable to whoever runs it and
 * nobody else, which is precisely the attribution the on-chain record exists to
 * provide.
 *
 * Offline, the observation is signed here and queued. Arming needs signal --
 * the server funds the transaction -- but a tagger out of range should still be
 * recording animals rather than waiting.
 */
import { useCallback, useEffect, useMemo, useState } from 'react'
import { useRouter } from 'expo-router'
import { View } from 'react-native'

import {
  Banner,
  Button,
  Card,
  Choice,
  Field,
  H1,
  H2,
  Input,
  Mono,
  Note,
  P,
  Screen
} from '../../src/ui/atoms'
import { FixField, useFix } from '../../src/ui/Fix'
import {
  Missing,
  ObservationForm,
  RuleBanner,
  emptyObservation,
  type ObservationFormValue
} from '../../src/ui/ObservationForm'
import { useWallet } from '../../src/wallet/WalletProvider'
import * as api from '../../src/wildtag/api'
import { canonicalBytes } from '../../src/wildtag/redeem'
import * as queue from '../../src/wildtag/queue'
import * as schema from '../../src/wildtag/schema'
import type { Profile } from '../../src/wildtag/types'

const ATTEST_PROTOCOL: [2, string] = [2, 'wildtag observation']

export default function Arm() {
  const router = useRouter()
  const { wallet, identityKey } = useWallet()
  const fix = useFix()

  const [profiles, setProfiles] = useState<Profile[]>([])
  const [species, setSpecies] = useState<string>('')
  const [tagID, setTagID] = useState('')
  const [name, setName] = useState('')
  const [form, setForm] = useState<ObservationFormValue>(emptyObservation)
  const [result, setResult] = useState<{ tone: 'good' | 'bad' | 'warn'; text: string } | null>(null)
  const [busy, setBusy] = useState(false)

  const profile = useMemo(() => profiles.find((p) => p.code === species) ?? null, [profiles, species])

  useEffect(() => {
    void (async () => {
      try {
        const doc = await schema.load(api.server())
        setProfiles(doc.profiles)
        setSpecies(doc.default)
      } catch (err) {
        setResult({ tone: 'bad', text: err instanceof Error ? err.message : String(err) })
      }
    })()
  }, [])

  // Changing species changes what the form asks for, so the answers to the old
  // one must not carry over: a "stage" of HARD on a red drum is a field that
  // profile does not define, and the server would refuse it.
  const changeSpecies = useCallback((code: string) => {
    setSpecies(code)
    setForm(emptyObservation())
  }, [])

  const canonicalTagID = tagID.trim().replace(/-/g, '').toUpperCase()

  const blocked = profile ? schema.notTaggable(profile, form.meas, form.attr) : null
  const ready =
    !!profile &&
    !!fix.fix &&
    !!wallet &&
    !!identityKey &&
    canonicalTagID.length > 0 &&
    !blocked &&
    schema.missing(profile, form.meas, form.attr, true).length === 0 &&
    schema.outOfRange(profile, form.meas).length === 0

  const arm = async () => {
    if (!profile || !fix.fix || !wallet || !identityKey) return
    setBusy(true)
    setResult(null)

    const base: api.ActivateForm = {
      tag_id: canonicalTagID,
      species: profile.code,
      lat: fix.fix.lat,
      lon: fix.fix.lon,
      accuracy_m: fix.fix.accuracyM,
      meas: form.meas,
      attr: form.attr,
      name: name.trim(),
      // The identity key goes in the first request, not the second: it is
      // written *inside* the bytes about to be signed, so asking for the record
      // without it would produce one record to sign and a different one to
      // submit, and the server would correctly refuse the mismatch.
      attest_pub: identityKey
    }

    try {
      const preview = await api.prepareActivation(base)
      // Sign under the canonical id the server handed back, never the string
      // that was typed. A tagger enters the displayed form with a dash; the
      // server strips it, and deriving under the typed value produces a key
      // that verifies nowhere.
      const { signature } = await wallet.createSignature({
        protocolID: ATTEST_PROTOCOL,
        keyID: preview.tag_id,
        counterparty: 'anyone',
        // The payload itself, not a hash of it: createSignature applies SHA-256
        // to `data` before signing, and the server verifies against
        // sha256(payload). Passing a digest signs it twice.
        data: hexToBytes(preview.observation)
      })
      const res = await api.activate({
        ...base,
        tag_id: preview.tag_id,
        observation: preview.observation,
        attest_sig: toHex(signature),
        attest_pub: identityKey
      })
      setResult({
        tone: 'good',
        text: `Armed with ${res.satoshis.toLocaleString()} sats. ${res.txid}`
      })
      setTagID('')
      setName('')
      setForm(emptyObservation())
    } catch (err) {
      if (err instanceof api.ApiError && err.offline) {
        await queueTagging()
        return
      }
      setResult({ tone: 'bad', text: err instanceof Error ? err.message : String(err) })
    } finally {
      setBusy(false)
    }
  }

  /**
   * queueTagging signs and stores a tagging captured out of range.
   *
   * The species is known here because the tagger chose it, which is the easy
   * half. What cannot be known offline is the server's own timestamp, so the
   * device stamps the moment of the catch and the server accepts a queued
   * record within a day, recording the delay.
   */
  const queueTagging = async () => {
    if (!profile || !fix.fix || !wallet || !identityKey) return
    try {
      const obs = {
        lat: fix.fix.lat,
        lon: fix.fix.lon,
        accuracyM: fix.fix.accuracyM,
        meas: form.meas,
        attr: form.attr,
        name: name.trim(),
        species: profile.code,
        observer: identityKey,
        at: fix.fix.at
      }
      const { text } = canonicalBytes(obs)
      const hex = toHex(Array.from(new TextEncoder().encode(text)))
      const { signature } = await wallet.createSignature({
        protocolID: ATTEST_PROTOCOL,
        keyID: canonicalTagID,
        counterparty: 'anyone',
        data: hexToBytes(hex)
      })
      await queue.enqueue({
        kind: 'tagging',
        tagID: canonicalTagID,
        observation: obs,
        observationHex: hex,
        attestSig: toHex(signature),
        attestPub: identityKey
      })
      setResult({
        tone: 'warn',
        text:
          'No signal, so this tagging is saved on the phone. The position and time are already ' +
          'fixed by your signature; it will be sent when you are back in range.'
      })
      setTagID('')
      setForm(emptyObservation())
    } catch (err) {
      setResult({ tone: 'bad', text: err instanceof Error ? err.message : String(err) })
    }
  }

  return (
    <Screen>
      <Card>
        <H1>Arm a tag</H1>
        {result ? <Banner tone={result.tone}>{result.text}</Banner> : null}

        <Field label="Tag id" help="Read it off the tag, or scan the QR and copy the code shown.">
          <Input
            value={tagID}
            onChangeText={setTagID}
            autoCapitalize="characters"
            autoCorrect={false}
            placeholder="ECX-ZMJP"
          />
        </Field>

        <Field label="Species">
          <Choice
            options={profiles.map((p) => ({ code: p.code, label: p.common }))}
            value={species}
            onChange={changeSpecies}
          />
          {profile ? <Note>{profile.programme}</Note> : null}
        </Field>
      </Card>

      {profile ? (
        <Card>
          <H2>The animal</H2>
          <RuleBanner profile={profile} value={form} tagging />
          <ObservationForm profile={profile} value={form} onChange={setForm} tagging />
          <Field
            label="Name it (optional)"
            help={
              'Leaving this blank is usually the better call: whoever finds it gets to name it, and ' +
              'that is the part people remember. Either way the name is permanent and public.'
            }
          >
            <Input value={name} onChangeText={setName} maxLength={24} placeholder="leave blank" />
          </Field>
          <FixField label="Where you are" fix={fix} />
          <Missing profile={profile} value={form} tagging />
          <Button title="Arm this tag" onPress={arm} disabled={!ready} busy={busy} />
        </Card>
      ) : null}

      <Card>
        <P>Signed in as</P>
        <Mono>{identityKey ?? 'no wallet'}</Mono>
        <View style={{ gap: 8 }}>
          <Button title="Reports waiting to send" onPress={() => router.push('/admin/queue')} tone="quiet" />
          <Button
            title="Sign out"
            onPress={async () => {
              await api.logout()
              router.replace('/')
            }}
            tone="quiet"
          />
        </View>
      </Card>
    </Screen>
  )
}

const toHex = (b: number[] | Uint8Array): string =>
  Array.from(b, (x) => x.toString(16).padStart(2, '0')).join('')

const hexToBytes = (h: string): number[] => {
  const out: number[] = []
  for (let i = 0; i < h.length; i += 2) out.push(parseInt(h.slice(i, i + 2), 16))
  return out
}
