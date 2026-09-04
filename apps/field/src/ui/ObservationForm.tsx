/**
 * The form for whatever the species profile says to record.
 *
 * This component knows nothing about any animal. It is handed a profile from
 * GET /api/schema and renders the measurements and choices that profile
 * declares, evaluates that profile's own rules, and hands back the two maps a
 * record carries. Adding a species is a JSON file on the server, not a release
 * here.
 *
 * The rules are evaluated on-device so a person finds out before they submit
 * rather than after -- but the server enforces them regardless. This is
 * courtesy, not security, and the two implementations agree because the rules
 * themselves are data fetched from the server rather than a second copy of the
 * law written in TypeScript.
 */
import { useMemo } from 'react'
import { View } from 'react-native'

import * as schema from '../wildtag/schema'
import type { Profile, Rule } from '../wildtag/types'
import { Banner, Choice, Field, Input, Note } from './atoms'

export interface ObservationFormValue {
  meas: Record<string, number>
  attr: Record<string, string>
  /** What the user typed, kept verbatim so a half-typed "12." is not eaten. */
  text: Record<string, string>
}

export const emptyObservation = (): ObservationFormValue => ({ meas: {}, attr: {}, text: {} })

export interface ObservationFormProps {
  profile: Profile
  value: ObservationFormValue
  onChange: (next: ObservationFormValue) => void
  /** True for a tagger: tagger-only fields appear, the disposition does not. */
  tagging: boolean
}

export function ObservationForm({ profile, value, onChange, tagging }: ObservationFormProps) {
  const measures = useMemo(() => schema.measures(profile, tagging), [profile, tagging])
  const vocabs = useMemo(() => schema.vocabs(profile, tagging), [profile, tagging])

  const setMeasure = (key: string, text: string) => {
    const m = profile.measures.find((x) => x.key === key)!
    const scaled = schema.scaleValue(text, m)
    const meas = { ...value.meas }
    if (scaled === undefined) delete meas[key]
    else meas[key] = scaled
    onChange({ ...value, meas, text: { ...value.text, [key]: text } })
  }

  const setAttr = (key: string, code: string) => {
    onChange({ ...value, attr: { ...value.attr, [key]: code } })
  }

  return (
    <View style={{ gap: 12 }}>
      {measures.map((m) => (
        <Field
          key={m.key}
          label={`${m.label}${m.unit ? ` (${m.unit})` : ''}${schema.required(m, tagging) ? '' : ' — optional'}`}
          help={m.help}
        >
          <Input
            value={value.text[m.key] ?? ''}
            onChangeText={(t) => setMeasure(m.key, t)}
            keyboardType={m.scale > 1 ? 'decimal-pad' : 'number-pad'}
            inputMode={m.scale > 1 ? 'decimal' : 'numeric'}
            placeholder={
              m.scale > 1 ? `${m.min / m.scale} – ${m.max / m.scale}` : `${m.min} – ${m.max}`
            }
            accessibilityLabel={m.label}
          />
        </Field>
      ))}

      {vocabs.map((v) => (
        <Field
          key={v.key}
          label={`${v.label}${schema.required(v, tagging) ? '' : ' — optional'}`}
          help={v.help}
        >
          <Choice
            options={v.values.map((x) => ({ code: x.code, label: x.label }))}
            value={value.attr[v.key]}
            onChange={(code) => setAttr(v.key, code)}
          />
        </Field>
      ))}
    </View>
  )
}

/**
 * RuleBanner shows the profile's own verdict on what has been entered.
 *
 * Two different things, deliberately worded differently. A tagger is told not
 * to spend the tag; a finder is told the animal has to go back. The finder's
 * report is still accepted either way -- the animal has already been caught,
 * and refusing the report would destroy the data point the programme exists to
 * collect.
 */
export function RuleBanner({
  profile,
  value,
  tagging
}: {
  profile: Profile
  value: ObservationFormValue
  tagging: boolean
}) {
  const rule: Rule | null = tagging
    ? schema.notTaggable(profile, value.meas, value.attr)
    : schema.mustRelease(profile, value.meas, value.attr)

  const ranges = schema.outOfRange(profile, value.meas)
  if (ranges.length > 0) {
    return <Banner tone="bad">{ranges.join('. ')}.</Banner>
  }
  if (!rule) return null

  return (
    <Banner tone="warn">
      {tagging
        ? `Do not tag this one: ${rule.reason}.`
        : `${capitalise(rule.reason)}. Choose "released" to continue — you are still paid for the report.`}
    </Banner>
  )
}

/** Missing lists what still has to be filled in, rather than a dead button. */
export function Missing({
  profile,
  value,
  tagging
}: {
  profile: Profile
  value: ObservationFormValue
  tagging: boolean
}) {
  const missing = schema.missing(profile, value.meas, value.attr, tagging)
  if (missing.length === 0) return null
  return <Note>Still needed: {missing.join(', ')}.</Note>
}

const capitalise = (s: string) => (s ? s.charAt(0).toUpperCase() + s.slice(1) : s)
