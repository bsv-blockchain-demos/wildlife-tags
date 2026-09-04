/**
 * The tag's spending key, from the code printed on it.
 *
 * A scanned QR carries `<public url>/t/<TAG-ID>#<secret>`. The secret is in the
 * fragment on purpose: a fragment never leaves the client, so it stays out of
 * access logs, Referer headers and anything sitting in front of the server. The
 * key derived from it is half of the two-of-two lock on the reward, and holding
 * it is the only proof of physical possession the system has.
 *
 * Keep the domain strings in step with internal/tagkey. They are inputs to a
 * hash, so a one-character difference produces a different key, every
 * redemption fails, and nothing on either side looks wrong.
 */
import { Hash, PrivateKey, Utils } from '@bsv/sdk'

const KEY_DOMAIN = 'wildtag-v1-key|'
const SECRET_BYTES = 16

export class BadTagCode extends Error {
  constructor(why: string) {
    super(why)
    this.name = 'BadTagCode'
  }
}

export interface ScannedTag {
  /** Canonical id: no dash, as the record and the server use it. */
  tagID: string
  /** The display form, as printed. */
  display: string
  secret: number[]
  /** The deployment the code points at, which is where to send the report. */
  origin: string
}

/**
 * TAG_URL matches the payload we print, and nothing else.
 *
 *   <scheme>://<host>[:port]/…/t/<TAG-ID>#<secret>
 *
 * Deliberately a regex rather than `new URL()`. React Native does not ship the
 * WHATWG URL — it ships a regex approximation in Libraries/Blob/URL.js that
 * differs from the standard in ways that matter here: it does not throw on
 * `"hello world"`, and it *does* throw a bare TypeError on a string containing
 * `#` with no `://`. So a parser built on it behaves one way in a node test and
 * another on the device, and the tests stop describing the app.
 *
 * The payload is a format this project defines and prints. Parsing it directly
 * is a few characters of regex, behaves identically everywhere, and makes the
 * test suite mean what it says.
 */
const TAG_URL = /^(https?:\/\/[^/?#\s]+)(\/[^?#\s]*)?#(\S+)$/i

/**
 * parse reads a scanned QR payload.
 *
 * It is deliberately strict about the shape. A code that nearly parses is worse
 * than one that does not: it would send a report to the wrong deployment, or
 * derive a key for a tag that is not the one in the finder's hand.
 */
export function parse(scanned: string): ScannedTag {
  const text = scanned.trim()

  const withFragment = TAG_URL.exec(text)
  if (!withFragment) {
    // Distinguish the two failures a person can act on: a code that is not a
    // tag at all, and a tag link that has lost its secret. Telling somebody
    // holding a real tag that it "is not a tag" would send them away from the
    // one thing that would work -- scanning it directly.
    if (/^https?:\/\/[^/?#\s]+\/(?:[^?#\s]*\/)?t\/[^/?#\s]+$/i.test(text)) {
      throw new BadTagCode(
        'That link is missing its tag code. The code only travels when the QR is ' +
          'scanned directly, so scan the tag rather than following a forwarded link.'
      )
    }
    throw new BadTagCode('That code is not a wildlife tag. Scan the QR on the tag itself.')
  }

  // The regex has three capturing groups and matched, so all three are
  // present; the optional-path group defaults rather than being undefined.
  const origin = withFragment[1] ?? ''
  const path = withFragment[2] ?? ''
  const fragment = withFragment[3] ?? ''

  const parts = (path ?? '').split('/').filter(Boolean)
  if (parts.length < 2 || parts[parts.length - 2] !== 't') {
    throw new BadTagCode('That QR code is not a wildlife tag.')
  }

  let display: string
  try {
    display = decodeURIComponent(parts[parts.length - 1] ?? '')
  } catch {
    // A stray percent sign. decodeURIComponent throws URIError, which would
    // otherwise escape as an unhandled error rather than a readable refusal.
    throw new BadTagCode('That tag code is damaged.')
  }

  const tagID = display.replace(/-/g, '').toUpperCase()
  if (tagID === '') throw new BadTagCode('That tag code has no id in it.')

  return {
    tagID,
    display,
    secret: decodeSecret(fragment),
    origin
  }
}

/** decodeSecret reads the base64url secret from a tag's fragment. */
export function decodeSecret(fragment: string): number[] {
  const padded = fragment.length % 4 === 0 ? fragment : fragment + '='.repeat(4 - (fragment.length % 4))
  let bytes: number[]
  try {
    bytes = Utils.toArray(padded.replace(/-/g, '+').replace(/_/g, '/'), 'base64')
  } catch {
    throw new BadTagCode('That tag code is damaged.')
  }
  if (bytes.length !== SECRET_BYTES) {
    throw new BadTagCode(`That tag code is ${bytes.length} bytes, not ${SECRET_BYTES}.`)
  }
  return bytes
}

/** privateKey derives the tag's half of the two-of-two lock. */
export function privateKey(secret: number[]): PrivateKey {
  const material = Utils.toArray(KEY_DOMAIN, 'utf8').concat(secret)
  return new PrivateKey(Hash.sha256(material))
}
