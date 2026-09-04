/**
 * The species schema on the phone: fetch, cache, and evaluate.
 *
 * The cache is the point. This app is used in marshes and deer woods, and a
 * form that cannot render until the server answers is a form that does not work
 * where it is used. So the schema is stored on disk, revalidated with its ETag
 * whenever there is signal, and served from disk when there is not.
 *
 * The rule evaluation here mirrors species.Rule.Fires on the server exactly. If
 * the two ever disagree, this app tells somebody they may keep an animal the
 * server then refuses to let them keep -- after they have already killed it.
 * That is why the rules are data fetched from the server rather than a second
 * copy of the law written in TypeScript.
 */
import AsyncStorage from '@react-native-async-storage/async-storage'

import { dispositionKey, useDispositionKey } from './rules'
import type { Profile, SchemaDocument } from './types'

const CACHE_KEY = 'wildtag.schema.v2'
const ETAG_KEY = 'wildtag.schema.etag.v2'

let doc: SchemaDocument | null = null

export class SchemaUnavailable extends Error {
  constructor(cause: string) {
    super(
      `Cannot read the field guide: ${cause}. This app needs the list of what to ` +
        'record for each species, and could not reach the server or find a saved copy.'
    )
    this.name = 'SchemaUnavailable'
  }
}

/**
 * load fetches the schema, falling back to the cached copy.
 *
 * A failure to reach the server is not an error: that is the whole reason for
 * the cache. A failure with no cache is, because there is then nothing to ask
 * anybody for.
 */
export async function load(baseURL: string, force = false): Promise<SchemaDocument> {
  if (doc && !force) return doc

  const [cachedRaw, etag] = await Promise.all([
    AsyncStorage.getItem(CACHE_KEY),
    AsyncStorage.getItem(ETAG_KEY)
  ])
  const cached: SchemaDocument | null = cachedRaw ? (JSON.parse(cachedRaw) as SchemaDocument) : null

  try {
    const res = await fetch(`${baseURL}/api/schema`, {
      headers: etag ? { 'If-None-Match': etag } : undefined
    })
    if (res.status === 304 && cached) {
      doc = cached
      useDispositionKey(cached.disposition_key)
      return doc
    }
    if (!res.ok) throw new Error(`HTTP ${res.status}`)

    const fresh = (await res.json()) as SchemaDocument
    doc = fresh
    useDispositionKey(fresh.disposition_key)
    await AsyncStorage.multiSet([
      [CACHE_KEY, JSON.stringify(fresh)],
      [ETAG_KEY, res.headers.get('ETag') ?? '']
    ])
    return fresh
  } catch (err) {
    if (cached) {
      doc = cached
      useDispositionKey(cached.disposition_key)
      return cached
    }
    throw new SchemaUnavailable(err instanceof Error ? err.message : String(err))
  }
}

/** cached returns whatever is already loaded, without touching the network. */
export function cached(): SchemaDocument | null {
  return doc
}

export function profiles(): Profile[] {
  return doc?.profiles ?? []
}

/** profile resolves a species code, falling back to the deployment default. */
export function profile(code?: string): Profile | null {
  const want = code || doc?.default
  return profiles().find((p) => p.code === want) ?? profiles()[0] ?? null
}


// The rule and field logic lives in rules.ts, which imports nothing: it has to
// agree with internal/species byte for byte, and it is tested directly under
// node rather than through a React Native runtime.
export { dispositionKey }
export {
  fires,
  mustRelease,
  notTaggable,
  measures,
  vocabs,
  required,
  scaleValue,
  displayValue,
  show,
  label,
  missing,
  outOfRange
} from './rules'
