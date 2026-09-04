/**
 * The finder flow: the animal's story, the report, and the money.
 *
 * The story comes first and gets the weight. SCDNR's own account of running a
 * tagging programme is that many people who report a tag are more interested in
 * where the animal came from and how far it moved than in the reward -- one
 * angler's explanation was simply that understanding his prey made him better
 * at catching it. Nobody currently gives them that, and the on-chain record
 * makes it free to give.
 *
 * The offline case is not an error path. A finder in a marsh signs their
 * observation on the spot, it goes in the outbox, and the position and time are
 * fixed by that signature. Payment needs signal -- the server funds the
 * transaction and adds DNR's half of the two-of-two lock -- but the science
 * does not.
 */
import { useCallback, useEffect, useMemo, useState } from 'react'
import { useLocalSearchParams, useRouter } from 'expo-router'
import { View } from 'react-native'

import {
  Banner,
  Button,
  Card,
  H1,
  H2,
  Input,
  Field as FormField,
  Mono,
  Note,
  P,
  Screen,
  Stat
} from '../src/ui/atoms'
import { FixField, useFix } from '../src/ui/Fix'
import {
  Missing,
  ObservationForm,
  RuleBanner,
  emptyObservation,
  type ObservationFormValue
} from '../src/ui/ObservationForm'
import { theme } from '../src/ui/theme'
import { useWallet } from '../src/wallet/WalletProvider'
import * as api from '../src/wildtag/api'
import * as queue from '../src/wildtag/queue'
import { attest, redeem, type Step } from '../src/wildtag/redeem'
import * as schema from '../src/wildtag/schema'
import type { Observation, Profile, Provenance, RedeemReceipt, TagResponse } from '../src/wildtag/types'

const STEPS: { key: Step; label: string }[] = [
  { key: 'quote', label: "Work out what you're owed" },
  { key: 'attest', label: 'Sign your report' },
  { key: 'build', label: 'Build the payment' },
  { key: 'verify', label: 'Check it really pays you' },
  { key: 'sign', label: 'Unlock the tag' },
  { key: 'receive', label: 'Put it in your wallet' }
]

export default function Report() {
  const router = useRouter()
  const params = useLocalSearchParams<{
    tagID: string
    display: string
    secret: string
    origin: string
  }>()
  const tagID = params.tagID ?? ''
  const secret = useMemo(
    () => (params.secret ?? '').split(',').filter(Boolean).map(Number),
    [params.secret]
  )

  const { wallet, identityKey } = useWallet()
  const fix = useFix()

  const [info, setInfo] = useState<TagResponse | null>(null)
  const [profile, setProfile] = useState<Profile | null>(null)
  const [form, setForm] = useState<ObservationFormValue>(emptyObservation)
  const [name, setName] = useState('')
  const [loadError, setLoadError] = useState<string | null>(null)
  const [offline, setOffline] = useState(false)

  const [busy, setBusy] = useState(false)
  const [steps, setSteps] = useState<Partial<Record<Step, 'doing' | 'done'>>>({})
  const [receipt, setReceipt] = useState<RedeemReceipt | null>(null)
  const [queued, setQueued] = useState(false)
  const [error, setError] = useState<string | null>(null)

  // ---- load ---------------------------------------------------------------

  useEffect(() => {
    void (async () => {
      if (!tagID) return
      const base = params.origin || api.server()
      try {
        const doc = await schema.load(base)
        const tag = await api.tag(tagID)
        setInfo(tag)
        setProfile(schema.profile(tag.provenance.species) ?? schema.profile(doc.default))
      } catch (err) {
        // With no signal there is no story to tell and no species to ask about
        // -- but a cached schema still lets a report be captured and queued,
        // which is the part that matters scientifically.
        const cached = schema.cached()
        if (cached) {
          setOffline(true)
          setProfile(schema.profile())
        } else {
          setLoadError(err instanceof Error ? err.message : String(err))
        }
      }
    })()
  }, [tagID, params.origin])

  const canName = !!info && !info.provenance.name && info.tag.status === 'active'

  const observation = useCallback((): Observation | null => {
    if (!fix.fix || !profile || !identityKey) return null
    return {
      lat: fix.fix.lat,
      lon: fix.fix.lon,
      accuracyM: fix.fix.accuracyM,
      meas: form.meas,
      attr: form.attr,
      name: canName ? name.trim() : '',
      species: profile.code,
      observer: identityKey,
      at: fix.fix.at
    }
  }, [fix.fix, profile, identityKey, form, canName, name])

  const ready =
    !!profile &&
    !!fix.fix &&
    !!identityKey &&
    schema.missing(profile, form.meas, form.attr, false).length === 0 &&
    schema.outOfRange(profile, form.meas).length === 0

  // ---- submit -------------------------------------------------------------

  const submit = async () => {
    const obs = observation()
    if (!obs || !wallet || !profile) return

    setBusy(true)
    setError(null)
    setSteps({})
    try {
      const got = await redeem({
        wallet,
        tagID,
        secret,
        observation: obs,
        payee: identityKey!,
        onStep: (step, state) => setSteps((s) => ({ ...s, [step]: state }))
      })
      setReceipt(got)
    } catch (err) {
      if (err instanceof api.ApiError && err.offline) {
        // No signal. Sign here and queue, so the fix and the timestamp are the
        // ones from the moment of the catch rather than from whenever the boat
        // gets back.
        try {
          await queueReport(obs)
          setQueued(true)
        } catch (qerr) {
          setError(qerr instanceof Error ? qerr.message : String(qerr))
        }
      } else {
        setError(err instanceof Error ? err.message : String(err))
      }
    } finally {
      setBusy(false)
    }
  }

  /**
   * queueReport signs the observation on the device and stores it.
   *
   * The bytes are built and signed here, not at submission time. Rebuilding
   * them later would risk different bytes -- a different timestamp, a rounding
   * that landed elsewhere -- and the signature would then verify against
   * nothing.
   */
  const queueReport = async (obs: Observation) => {
    const { canonicalBytes } = await import('../src/wildtag/redeem')
    const { text } = canonicalBytes(obs)
    const hex = Array.from(new TextEncoder().encode(text))
      .map((b) => b.toString(16).padStart(2, '0'))
      .join('')
    const sig = await attest(wallet!, tagID, hex)
    await queue.enqueue({
      kind: 'report',
      tagID,
      secret: params.secret ?? '',
      observation: obs,
      observationHex: hex,
      attestSig: sig,
      attestPub: identityKey!
    })
  }

  // ---- render -------------------------------------------------------------

  if (loadError) {
    return (
      <Screen>
        <Card>
          <H2>Cannot read this tag</H2>
          <Banner tone="bad">{loadError}</Banner>
          <Button title="Back" onPress={() => router.replace('/')} tone="quiet" />
        </Card>
      </Screen>
    )
  }

  if (receipt) return <Paid receipt={receipt} info={info} name={canName ? name.trim() : ''} />

  if (queued) {
    return (
      <Screen>
        <Card>
          <H1>Saved</H1>
          <P>
            There is no signal here, so your report is waiting on this phone. It was signed where you
            caught the animal, so the position and the time are already fixed and cannot change.
          </P>
          <Note>
            It will be sent the next time this phone is online, and the delay is recorded in the
            record so a researcher can see the report was captured out of range.
          </Note>
          <Button title="Done" onPress={() => router.replace('/')} />
        </Card>
      </Screen>
    )
  }

  return (
    <Screen>
      <Card>
        <H1>Tag {params.display ?? tagID}</H1>
        {offline ? (
          <Banner tone="warn">
            No signal. You can still record what you caught — the report is signed here and sent
            later.
          </Banner>
        ) : info ? (
          <TagStatus info={info} />
        ) : (
          <Note>Reading the tag…</Note>
        )}
      </Card>

      {info?.provenance?.tagged_at ? <Story provenance={info.provenance} /> : null}

      {profile ? (
        <Card>
          <H2>What did you catch?</H2>
          <Note>
            {profile.common} ({profile.scientific})
          </Note>
          <RuleBanner profile={profile} value={form} tagging={false} />
          <ObservationForm profile={profile} value={form} onChange={setForm} tagging={false} />
          <FixField label="Where you caught it" fix={fix} />

          {canName ? (
            <FormField
              label="Name this animal (optional)"
              help={
                'Nobody has named this one yet, so it is yours to name. The name goes on the public ' +
                'record permanently and cannot be changed by anyone, including us.'
              }
            >
              <Input value={name} onChangeText={setName} maxLength={24} placeholder="e.g. Old Bertha" />
            </FormField>
          ) : null}

          <Missing profile={profile} value={form} tagging={false} />
          {error ? <Banner tone="bad">{error}</Banner> : null}
          <Button title="Report it and get paid" onPress={submit} disabled={!ready || !wallet} busy={busy} />
          {!wallet ? <Note>Opening your wallet…</Note> : null}
        </Card>
      ) : null}

      {busy || Object.keys(steps).length > 0 ? <Steps steps={steps} /> : null}
    </Screen>
  )
}

function TagStatus({ info }: { info: TagResponse }) {
  const what = info.provenance.common_name?.toLowerCase() || 'animal'
  switch (info.tag.status) {
    case 'active':
      return (
        <P>
          This tag is live. Reporting it pays {info.base_satoshis.toLocaleString()} sats, and putting
          the {what} back with the tag on is worth {info.bonus_satoshis.toLocaleString()} more.
        </P>
      )
    case 'cooldown':
      return (
        <Banner tone="warn">
          This tag was reported recently and is waiting for DNR to put it back in service. Its
          history is below.
        </Banner>
      )
    case 'redeeming':
      return (
        <Banner tone="warn">
          Somebody is redeeming this tag right now. If that was you and it stalled, wait a minute and
          try again.
        </Banner>
      )
    case 'minted':
      return <Banner>This tag has not been put on an animal yet, so there is nothing to claim.</Banner>
    default:
      return <Banner>This tag has been retired. Its history is below.</Banner>
  }
}

/** Story is the animal's history: the part people actually come back for. */
function Story({ provenance: p }: { provenance: Provenance }) {
  const km = p.distance_m / 1000
  const size = (v: number) =>
    p.primary_scale > 1 ? `${(v / p.primary_scale).toFixed(2)} ${p.primary_unit}` : `${v} ${p.primary_unit}`

  return (
    <Card>
      <H1>{p.name || `This ${p.common_name.toLowerCase()} has no name`}</H1>
      <Note>
        {p.common_name} ({p.scientific_name}) · tag {p.tag_id}
        {p.name ? ` · named by ${short(p.named_by)}` : ' · nobody has named it yet'}
      </Note>

      <View style={{ flexDirection: 'row', flexWrap: 'wrap', gap: theme.gap, marginTop: 8 }}>
        <Stat
          n={p.days_at_large.toLocaleString()}
          unit={p.days_at_large === 1 ? 'day' : 'days'}
          caption="carrying this tag"
        />
        <Stat
          n={km < 1 ? p.distance_m.toLocaleString() : km.toFixed(1)}
          unit={km < 1 ? 'metres' : 'km'}
          caption="from where it started"
        />
        <Stat n={String(p.recaptures.length + 1)} unit="sightings" caption="including yours" />
        {p.total_path_m > p.distance_m + 50 ? (
          <Stat n={(p.total_path_m / 1000).toFixed(1)} unit="km" caption="total known journey" />
        ) : null}
      </View>

      <H2>Its history</H2>
      <View style={{ gap: 10 }}>
        <Timeline
          when={p.tagged_at}
          what="Tagged by a DNR biologist"
          detail={p.facts.map((f) => `${f.label}: ${f.value}`).join(' · ')}
        />
        {p.recaptures.map((r) => (
          <Timeline
            key={r.txid + r.at}
            when={r.at}
            what={r.disposition === 'RELEASED' ? 'Caught and put back' : 'Caught and kept'}
            detail={
              `${size(r.primary)} · ${(r.distance_m / 1000).toFixed(1)} km out · day ${r.days_at_large}` +
              (r.proven ? ' · proven on chain' : ' · awaiting proof')
            }
          />
        ))}
        <Timeline when={null} what="You found it" detail="Report it below to get paid" />
      </View>

      {p.growth !== 0 ? (
        <Note>
          {p.growth_expected
            ? `It has grown ${size(Math.abs(p.growth))} since tagging — which is exactly what a tagging ` +
              'programme is for: nobody can measure the same wild animal twice any other way.'
            : `It has grown ${size(Math.abs(p.growth))} since tagging. That is unusual and worth a second ` +
              'look: this animal grows only by moulting, and it sheds the tag when it does.'}
        </Note>
      ) : null}
      <Note>
        Tagged at {p.tagged_lat.toFixed(4)}, {p.tagged_lon.toFixed(4)} and recorded on chain the same
        day — that part cannot be edited by anyone, including us.
      </Note>
    </Card>
  )
}

function Timeline({ when, what, detail }: { when: string | null; what: string; detail: string }) {
  return (
    <View style={{ borderLeftWidth: 2, borderLeftColor: theme.panelEdge, paddingLeft: 12, gap: 2 }}>
      <Note>{when ? new Date(when).toLocaleDateString() : 'today'}</Note>
      <P>{what}</P>
      {detail ? <Note>{detail}</Note> : null}
    </View>
  )
}

function Steps({ steps }: { steps: Partial<Record<Step, 'doing' | 'done'>> }) {
  return (
    <Card>
      <H2>Getting you paid</H2>
      {STEPS.map((s) => (
        <P key={s.key} style={{ color: steps[s.key] === 'done' ? theme.good : theme.ink }}>
          {steps[s.key] === 'done' ? '✓ ' : steps[s.key] === 'doing' ? '… ' : '   '}
          {s.label}
        </P>
      ))}
    </Card>
  )
}

function Paid({
  receipt,
  info,
  name
}: {
  receipt: RedeemReceipt
  info: TagResponse | null
  name: string
}) {
  const router = useRouter()
  const what = info?.provenance.common_name?.toLowerCase() ?? 'animal'
  // Whether this payment is still sitting in the outbox rather than the
  // balance. Read once on mount; it does not change while this screen is up.
  const [pending, setPending] = useState(false)
  useEffect(() => {
    void queue
      .pendingPayments()
      .then((ps) => setPending(ps.some((p) => p.txid === receipt.txid)))
  }, [receipt.txid])
  return (
    <Screen>
      <Card>
        <H1>{receipt.payout_satoshis.toLocaleString()} sats</H1>
        <P>Paid into this phone&apos;s wallet.</P>
        {name ? <P>You named it {name}. That is permanent and public.</P> : null}
        {pending ? (
          <Banner tone="warn">
            The payment is signed and broadcast, so the satoshis are yours — but your wallet cannot
            show them until the block carrying the transaction arrives. It will credit itself the
            next time you open the app.
          </Banner>
        ) : null}
        <Note>
          {receipt.retired
            ? 'This tag is now retired. Thank you for reporting it.'
            : `Your bonus is held until this ${what} is caught again. If it is, you get paid ` +
              'automatically — no need to come back.'}
        </Note>
        <Note>Transaction</Note>
        <Mono>{receipt.txid}</Mono>
        <Button title="Done" onPress={() => router.replace('/')} />
      </Card>
    </Screen>
  )
}

const short = (k?: string) => (k && k.length > 12 ? `${k.slice(0, 10)}…` : (k ?? 'a biologist'))
