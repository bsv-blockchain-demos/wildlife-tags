/**
 * The shapes GET /api/schema publishes, and the ones the API returns.
 *
 * These mirror internal/species and internal/service on the server. They are
 * hand-written rather than generated because there is no code generator in this
 * project worth the ceremony for one endpoint, and because a wrong field name
 * here fails immediately and loudly at the first request.
 */

export type Workflow = 'mark-recapture' | 'harvest'

/** The networks a BRC-100 wallet can be built for. */
export type WalletChain = 'main' | 'test' | 'teratest'

/** A number recorded about an animal, stored as a scaled integer. */
export interface Measure {
  key: string
  label: string
  unit: string
  /** 1 for whole units, 100 for hundredths. The record carries value * scale. */
  scale: number
  min: number
  max: number
  required: boolean
  /** True for a field a tagger records and a finder is not asked for. */
  tagging_only?: boolean
  help?: string
}

export interface VocabValue {
  code: string
  label: string
}

/** A categorical choice: sex, life stage, gear, disposition. */
export interface Vocab {
  key: string
  label: string
  values: VocabValue[]
  required: boolean
  tagging_only?: boolean
  help?: string
}

/**
 * A declarative predicate over an observation.
 *
 * Exactly one of `measure` or `vocab` is set. This is how legal and protocol
 * constraints reach the phone as data: a size minimum, a slot limit, an
 * egg-bearing female, an antler restriction — all one shape.
 */
export interface Rule {
  measure?: string
  vocab?: string
  less_than?: number
  more_than?: number
  in?: string[]
  not_in?: string[]
  /** Shown to the user, and carried in the refusal. */
  reason: string
}

export interface Profile {
  code: string
  common: string
  scientific: string
  workflow: Workflow
  programme: string
  measures: Measure[]
  vocabs: Vocab[]
  /** The size axis a receipt leads with, and what growth is computed against. */
  primary_measure: string
  not_taggable: Rule[]
  must_release: Rule[]
  sweep_after_days: number
  qr_version_max: number
  growth_expected: boolean
}

export interface SchemaDocument {
  default: string
  profiles: Profile[]
  /** Named by the server so a client cannot guess this key wrong. */
  disposition_key: string
}

export const RELEASED = 'RELEASED'
export const HARVESTED = 'HARVESTED'

/** What a person saw, in the human units a form collects. */
export interface Observation {
  lat: number
  lon: number
  accuracyM: number
  meas: Record<string, number>
  attr: Record<string, string>
  name?: string
  species: string
  observer: string
  /** RFC3339, UTC — the moment of the catch, not of submission. */
  at: string
}

export interface Fact {
  key: string
  label: string
  value: string
}

export interface RecaptureSummary {
  at: string
  lat: number
  lon: number
  meas: Record<string, number>
  attr: Record<string, string>
  primary: number
  disposition: string
  days_at_large: number
  distance_m: number
  txid: string
  proven: boolean
  paid_satoshis: number
}

/** An animal's whole story, as the receipt shows it. */
export interface Provenance {
  tag_id: string
  species: string
  common_name: string
  scientific_name: string
  name: string
  named_by?: string
  named_at?: string
  tagged_at: string | null
  tagged_lat: number
  tagged_lon: number
  tagged_txid: string
  tagged_meas: Record<string, number>
  tagged_attr: Record<string, string>
  facts: Fact[]
  primary_key: string
  primary_label: string
  primary_unit: string
  primary_scale: number
  primary_at_tagging: number
  tagger_key: string
  batch_id: string
  recaptures: RecaptureSummary[]
  days_at_large: number
  distance_m: number
  total_path_m: number
  growth: number
  growth_expected: boolean
  generation: number
  status: string
}

export interface TagView {
  tag_id: string
  display: string
  name?: string
  status: 'minted' | 'active' | 'redeeming' | 'cooldown' | 'retired'
  generation: number
  satoshis: number
  activated_at?: string
  last_event_at?: string
  cooldown_until?: string
  txid?: string
}

export interface TagResponse {
  tag: TagView
  provenance: Provenance
  base_satoshis: number
  bonus_satoshis: number
}

export interface RecaptureQuote {
  tag_id: string
  species: string
  /** Canonical bytes, hex. Signed exactly as given. */
  observation: string
  payout_satoshis: number
  bonus_satoshis: number
  escrow_release_satoshis: number
  must_release: boolean
  must_release_reason?: string
  animal_name: string
  can_name: boolean
  provenance: Provenance | null
}

export interface RedeemDraft {
  reference: string
  tag_id: string
  /** BEEF, hex. */
  signable_tx: string
  input_index: number
  derivation_prefix: string
  derivation_suffix: string
  sender_identity_key: string
  payout_index: number
  payout_satoshis: number
  escrow_satoshis: number
  next_lock_satoshis: number
  expires_at: string
}

export interface RedeemReceipt {
  txid: string
  atomic_beef: string
  payout_index: number
  payout_satoshis: number
  derivation_prefix: string
  derivation_suffix: string
  sender_identity_key: string
  retired: boolean
}

export interface ActivationPreview {
  tag_id: string
  species: string
  /** Canonical bytes, hex. */
  observation: string
  at: string
  base_satoshis: number
  bonus_satoshis: number
  total_satoshis: number
}

export interface ServerInfo {
  network: string
  /**
   * The network a client wallet should build itself for.
   *
   * Not the same vocabulary as `network`: the server has four, a BRC-100 wallet
   * has three, and it calls both Teranode testnets "teratest". Published by the
   * server so no client has to know the correspondence.
   */
  wallet_chain: WalletChain
  arcade_url: string
  public_url: string
  identity_key: string
  base_satoshis: number
  bonus_satoshis: number
  admin_protocol: string
  password_login: boolean
}
