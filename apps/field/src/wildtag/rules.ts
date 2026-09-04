/**
 * The parts of the schema that are pure functions.
 *
 * Nothing here imports anything. That is deliberate: this logic has to agree
 * with internal/species on the server exactly -- if the two ever disagree, this
 * app tells somebody they may keep an animal the server then refuses to let
 * them keep, after they have already killed it -- and the only way to test that
 * cheaply is to run it under plain node, with no React Native runtime in the
 * way.
 *
 * The rules themselves are still data fetched from the server. What is here is
 * the evaluator, not the law.
 */
import type { Measure, Profile, Rule, Vocab } from './types'

/** DISPOSITION_KEY is the fallback when the schema document is not loaded yet. */
const DEFAULT_DISPOSITION_KEY = 'disp'

let dispositionKeyValue = DEFAULT_DISPOSITION_KEY

/** useDispositionKey adopts the key the server named. See schema.load. */
export function useDispositionKey(key: string): void {
  dispositionKeyValue = key || DEFAULT_DISPOSITION_KEY
}

export function dispositionKey(): string {
  return dispositionKeyValue
}

/** fires mirrors species.Rule.Fires on the server. */
export function fires(rule: Rule, meas: Record<string, number>, attr: Record<string, string>): boolean {
  if (rule.measure) {
    const v = meas[rule.measure]
    if (v === undefined) return false
    if (rule.less_than !== undefined && v < rule.less_than) return true
    if (rule.more_than !== undefined && v > rule.more_than) return true
    return false
  }
  if (rule.vocab) {
    const code = attr[rule.vocab]
    if (code === undefined || code === '') return false
    if ((rule.in ?? []).includes(code)) return true
    if ((rule.not_in ?? []).length > 0) return !rule.not_in!.includes(code)
    return false
  }
  return false
}

function firstFiring(
  rules: Rule[] | undefined,
  meas: Record<string, number>,
  attr: Record<string, string>
): Rule | null {
  for (const r of rules ?? []) if (fires(r, meas, attr)) return r
  return null
}

/** mustRelease reports the law's reason this animal has to go back, if any. */
export function mustRelease(
  p: Profile,
  meas: Record<string, number>,
  attr: Record<string, string>
): Rule | null {
  return firstFiring(p.must_release, meas, attr)
}

/** notTaggable reports why a tag spent on this animal would be wasted, if it would. */
export function notTaggable(
  p: Profile,
  meas: Record<string, number>,
  attr: Record<string, string>
): Rule | null {
  return firstFiring(p.not_taggable, meas, attr)
}

/** measures returns the numbers this observer is asked for. */
export function measures(p: Profile, tagging: boolean): Measure[] {
  return p.measures.filter((m) => tagging || !m.tagging_only)
}

/**
 * vocabs returns the choices this observer is asked for.
 *
 * The disposition is excluded at tagging: a tagger is releasing the animal by
 * definition, and asking would invite the wrong answer.
 */
export function vocabs(p: Profile, tagging: boolean): Vocab[] {
  const dispKey = dispositionKey()
  return p.vocabs.filter((v) => {
    if (v.key === dispKey) return !tagging
    return tagging || !v.tagging_only
  })
}

export function required(field: Measure | Vocab, tagging: boolean): boolean {
  return field.required && (tagging || !field.tagging_only)
}

/**
 * scaleValue converts what a person typed into the integer the record carries.
 *
 * Math.round, not truncation: 28.4 * 100 is 2839.999... in binary floating
 * point, and a measurement one unit short of what somebody typed is a bug
 * nobody would ever find in the data.
 */
export function scaleValue(text: string, m: Measure): number | undefined {
  const trimmed = text.trim()
  if (trimmed === '') return undefined
  const n = Number(trimmed)
  if (!Number.isFinite(n)) return undefined
  return Math.round(n * (m.scale || 1))
}

/** displayValue is the inverse, for showing a stored integer in a field. */
export function displayValue(value: number | undefined, m: Measure): string {
  if (value === undefined) return ''
  return m.scale > 1 ? String(value / m.scale) : String(value)
}

/** show renders a stored integer with its unit, for a receipt. */
export function show(p: Profile, key: string, value: number): string {
  const m = p.measures.find((x) => x.key === key)
  if (!m) return String(value)
  return `${m.scale > 1 ? (value / m.scale).toFixed(2) : value} ${m.unit}`
}

export function label(p: Profile, key: string, code: string): string {
  const v = p.vocabs.find((x) => x.key === key)
  return v?.values.find((x) => x.code === code)?.label ?? code
}

/**
 * missing lists the required fields with no answer yet, so a screen can say
 * what is still needed rather than disabling a button with no explanation.
 */
export function missing(
  p: Profile,
  meas: Record<string, number>,
  attr: Record<string, string>,
  tagging: boolean
): string[] {
  const out: string[] = []
  for (const m of measures(p, tagging)) {
    if (required(m, tagging) && meas[m.key] === undefined) out.push(m.label)
  }
  for (const v of vocabs(p, tagging)) {
    const isDisposition = v.key === dispositionKey()
    if ((required(v, tagging) || isDisposition) && !attr[v.key]) out.push(v.label)
  }
  return out
}

/** outOfRange lists measurements a profile would refuse, with the reason. */
export function outOfRange(p: Profile, meas: Record<string, number>): string[] {
  const out: string[] = []
  for (const m of p.measures) {
    const v = meas[m.key]
    if (v === undefined) continue
    if (v < m.min || v > m.max) {
      const lo = m.scale > 1 ? m.min / m.scale : m.min
      const hi = m.scale > 1 ? m.max / m.scale : m.max
      out.push(`${m.label} must be between ${lo} and ${hi} ${m.unit}`)
    }
  }
  return out
}
