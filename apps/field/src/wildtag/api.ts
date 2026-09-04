/**
 * The server client.
 *
 * Two things are worth knowing about it.
 *
 * Authentication is a bearer header, not a cookie. React Native's fetch handles
 * cookies differently on Android and iOS and differently again between debug
 * and release builds, and debugging a login that works on one platform and
 * silently fails on the other is not how anybody should spend a week. The
 * server accepts either; see auth.sessionToken.
 *
 * Errors carry the server's own message. A finder standing in a marsh needs to
 * know whether to try again, wait, or give up, and that sentence is the only
 * thing telling them -- so it is preserved rather than replaced with a status
 * code.
 */
import AsyncStorage from '@react-native-async-storage/async-storage'

import type {
  ActivationPreview,
  Provenance,
  RecaptureQuote,
  RedeemDraft,
  RedeemReceipt,
  ServerInfo,
  TagResponse
} from './types'

const BASE_KEY = 'wildtag.server'
const TOKEN_KEY = 'wildtag.session'

/** ApiError keeps the status alongside the message, so callers can branch. */
export class ApiError extends Error {
  readonly status: number
  constructor(status: number, message: string) {
    super(message)
    this.name = 'ApiError'
    this.status = status
  }

  /** Retryable means "the same request might work in a moment". */
  get retryable(): boolean {
    return this.status === 0 || this.status === 409 || this.status === 429 || this.status >= 500
  }

  /** Offline means the request never reached the server at all. */
  get offline(): boolean {
    return this.status === 0
  }
}

let baseURL = ''
let token: string | null = null

export async function init(): Promise<void> {
  baseURL = (await AsyncStorage.getItem(BASE_KEY)) ?? ''
  token = await AsyncStorage.getItem(TOKEN_KEY)
}

export function server(): string {
  return baseURL
}

/** setServer points the app at a deployment. */
export async function setServer(url: string): Promise<void> {
  baseURL = url.replace(/\/+$/, '')
  await AsyncStorage.setItem(BASE_KEY, baseURL)
}

export function signedIn(): boolean {
  return token !== null && token !== ''
}

async function setToken(value: string | null): Promise<void> {
  token = value
  if (value) await AsyncStorage.setItem(TOKEN_KEY, value)
  else await AsyncStorage.removeItem(TOKEN_KEY)
}

async function request<T>(path: string, body?: unknown, method?: string): Promise<T> {
  if (!baseURL) throw new ApiError(0, 'No server configured yet.')

  const headers: Record<string, string> = {}
  if (body !== undefined) headers['Content-Type'] = 'application/json'
  if (token) headers.Authorization = `Bearer ${token}`

  let res: Response
  try {
    res = await fetch(`${baseURL}${path}`, {
      method: method ?? (body !== undefined ? 'POST' : 'GET'),
      headers,
      body: body !== undefined ? JSON.stringify(body) : undefined
    })
  } catch (err) {
    // Status zero means the request never left the phone. Callers distinguish
    // this from a refusal, because a queued report should be retried and a
    // refused one should not.
    throw new ApiError(0, err instanceof Error ? err.message : 'No connection')
  }

  const text = await res.text()
  let parsed: unknown = null
  try {
    parsed = text ? JSON.parse(text) : null
  } catch {
    parsed = null
  }
  if (!res.ok) {
    const message =
      (parsed as { error?: string } | null)?.error ?? `The server refused the request (${res.status})`
    if (res.status === 401) await setToken(null)
    throw new ApiError(res.status, message)
  }
  return parsed as T
}

// ---- public ----------------------------------------------------------------

export const info = () => request<ServerInfo>('/api/info')

export const tag = async (tagID: string): Promise<TagResponse> => {
  const res = await request<TagResponse>(`/api/tag/${encodeURIComponent(tagID)}`)
  res.provenance = normaliseProvenance(res.provenance)
  return res
}

/**
 * normaliseProvenance makes every collection a real array or object.
 *
 * Go marshals a nil slice as `null`, so a tag nobody has reported yet arrives
 * with `recaptures: null` while this app's type says `RecaptureSummary[]`. The
 * compiler cannot catch that -- the type describes the intent, not the bytes --
 * and the first `.length` crashes the screen. It did: every finder scanning a
 * freshly armed tag, which is the single most common path there is.
 *
 * The server no longer sends null, and there is a test pinning that. This stays
 * regardless, because the app is installed on phones that will meet deployments
 * older than themselves, and "the server was fixed" is not a property this side
 * can check.
 */
export function normaliseProvenance(p: Provenance | null): Provenance {
  const safe = (p ?? {}) as Provenance
  return {
    ...safe,
    recaptures: (safe.recaptures ?? []).map((r) => ({
      ...r,
      meas: r.meas ?? {},
      attr: r.attr ?? {}
    })),
    facts: safe.facts ?? [],
    tagged_meas: safe.tagged_meas ?? {},
    tagged_attr: safe.tagged_attr ?? {}
  }
}

export interface RecaptureForm {
  tag_id: string
  lat: number
  lon: number
  accuracy_m: number
  meas: Record<string, number>
  attr: Record<string, string>
  payee: string
  name?: string
  observation?: string
  attest_sig?: string
  attest_pub?: string
}

export const quoteRecapture = async (form: RecaptureForm): Promise<RecaptureQuote> => {
  const q = await request<RecaptureQuote>('/api/redeem/quote', form)
  // The quote carries the same object, from the same nil slices.
  if (q.provenance) q.provenance = normaliseProvenance(q.provenance)
  return q
}

export const prepareRedeem = (form: RecaptureForm) =>
  request<RedeemDraft>('/api/redeem/prepare', form)

export const completeRedeem = (reference: string, tagSig: string) =>
  request<RedeemReceipt>('/api/redeem/complete', { reference, tag_sig: tagSig })

// ---- tagger ----------------------------------------------------------------

export interface Challenge {
  nonce: string
  protocol: string
  security_level: number
}

export const challenge = () => request<Challenge>('/api/admin/challenge', {})

/**
 * loginWithIdentity signs in with a BRC-100 identity signature.
 *
 * `bearer: true` asks for the session token in the response body. The server
 * withholds it otherwise, because the browser flow keeps the token in an
 * HttpOnly cookie and handing the same value to page JavaScript would throw
 * that protection away for every browser session to spare one field here.
 */
export async function loginWithIdentity(
  identityKey: string,
  nonce: string,
  signature: string
): Promise<{ identity_key: string; expires_at: string }> {
  const res = await request<{ identity_key: string; expires_at: string; token?: string }>(
    '/api/admin/login',
    { identity_key: identityKey, nonce, signature, bearer: true }
  )
  if (res.token) await setToken(res.token)
  return res
}

export async function loginWithPassword(
  password: string
): Promise<{ identity_key: string; expires_at: string }> {
  const res = await request<{ identity_key: string; expires_at: string; token?: string }>(
    '/api/admin/login',
    { password, bearer: true }
  )
  if (res.token) await setToken(res.token)
  return res
}

export async function logout(): Promise<void> {
  try {
    await request('/api/admin/logout', {})
  } finally {
    await setToken(null)
  }
}

export const session = () =>
  request<{ identity_key: string; label: string; expires_at: string }>('/api/admin/session')

export interface ActivateForm {
  tag_id: string
  species: string
  lat: number
  lon: number
  accuracy_m: number
  meas: Record<string, number>
  attr: Record<string, string>
  name?: string
  observation?: string
  attest_sig?: string
  attest_pub?: string
}

export const prepareActivation = (form: ActivateForm) =>
  request<ActivationPreview>('/api/admin/activate/prepare', form)

export const activate = (form: ActivateForm) =>
  request<{ txid: string; vout: number; satoshis: number; sweep_after: string }>(
    '/api/admin/activate',
    form
  )
