/**
 * Reading a scanned code, and deriving the key it carries.
 *
 * The derivation has to match internal/tagkey exactly: the domain string is an
 * input to a hash, so one character out produces a different key, every
 * redemption fails, and nothing on either side looks wrong. The fixture below
 * is a real tag from a real batch, checked against what the Go side derives.
 */
import { strict as assert } from 'node:assert'
import test from 'node:test'

import { BadTagCode, decodeSecret, parse, privateKey } from '../src/wildtag/tagkey.ts'

// A throwaway tag that exists nowhere: the same fixture secret
// internal/web/crosslang_test.go uses, so both cross-language tests derive from
// the same material. Deliberately not a tag from a real batch — a bearer secret
// in a public repository is a bearer secret anyone can spend, and the fact that
// this one is testnet is luck rather than a policy.
const QR = 'https://tags.example.gov/t/DTBFDDZ#rRr2cgtt-OsSKlo3k6goPQ'

test('a scanned code yields the tag, its secret and its deployment', () => {
  const tag = parse(QR)
  assert.equal(tag.tagID, 'DTBFDDZ')
  assert.equal(tag.display, 'DTBFDDZ')
  assert.equal(tag.origin, 'https://tags.example.gov')
  assert.equal(tag.secret.length, 16)
})

test('the displayed dash is stripped, because the record does not carry it', () => {
  // A tag id is printed grouped -- "ECX-ZMJP" -- and the canonical form has no
  // dash. Signing under the displayed form derives a different key, which is a
  // bug that reached a live deployment once.
  const tag = parse('https://example.gov/t/DTB-FDDZ#rRr2cgtt-OsSKlo3k6goPQ')
  assert.equal(tag.tagID, 'DTBFDDZ')
  assert.equal(tag.display, 'DTB-FDDZ')
})

test('a link with no fragment is refused, with an explanation', () => {
  // The secret lives in the fragment so it never reaches a server log. A
  // forwarded link has lost it, and the person holding the phone needs to be
  // told to scan the tag rather than left with a dead page.
  assert.throws(() => parse('https://example.gov/t/DTBFDDZ'), (err) => {
    assert.ok(err instanceof BadTagCode)
    assert.match(err.message, /scan the tag/)
    return true
  })
})

test('a QR that is not a tag is refused', () => {
  assert.throws(() => parse('not a url at all'), BadTagCode)
  assert.throws(() => parse('https://example.gov/something/else#abc'), BadTagCode)
})

test('a truncated secret is refused rather than silently padded', () => {
  assert.throws(() => decodeSecret('rRr2cgtt-OsS'), /bytes, not 16/)
})

test('the tag key matches what the server derives', () => {
  // What internal/tagkey derives for this secret, taken from Go rather than
  // from this file. If the two ever drift, every printed tag stops working --
  // the output is locked to the server's key and the phone signs with a
  // different one -- and nothing else notices until somebody is standing in a
  // marsh. Regenerate with tagkey.Secret.PrivateKey().PubKey() if the
  // derivation domain ever changes.
  const key = privateKey(parse(QR).secret)
  assert.equal(
    key.toPublicKey().toString(),
    '0338d96819bbb7dad7f52783313978f2c85c9d9dc37eacaa680237837945f1c7c3'
  )
})

// --- inputs a camera will actually meet -------------------------------------

test('every kind of QR a phone might see is refused, not thrown at', () => {
  // The scanner points at whatever is in front of it, so parse() is handed
  // arbitrary strings tens of times a second. Anything other than a BadTagCode
  // escaping from here is a crash on the app's most-used screen.
  const hostile = [
    '',
    '   ',
    'hello world',
    'example.com',
    'WIFI:S:marina;T:WPA;P:hunter2;;',
    'BEGIN:VCARD\nVERSION:3.0\nEND:VCARD',
    'tel:+18435551234',
    'mailto:tags@dnr.sc.gov',
    // A '#' with no scheme: React Native's URL polyfill throws a bare
    // TypeError on exactly this, which is why parse() no longer uses it.
    'foo#bar',
    '#justafragment',
    'https://example.gov',
    'https://example.gov/',
    'https://example.gov/t/',
    'https://example.gov/t/#abc',
    'https://example.gov/nott/DTBFDDZ#rRr2cgtt-OsSKlo3k6goPQ',
    'ftp://example.gov/t/DTBFDDZ#rRr2cgtt-OsSKlo3k6goPQ',
    'https://example.gov/t/DTBFDDZ#tooshort',
    'https://example.gov/t/%E0%A4%A#rRr2cgtt-OsSKlo3k6goPQ',
    'https://example.gov/t/DTBFDDZ#' + 'A'.repeat(5000)
  ]

  for (const input of hostile) {
    assert.throws(
      () => parse(input),
      (err) => {
        assert.ok(
          err instanceof BadTagCode,
          `${JSON.stringify(input.slice(0, 40))} threw ${err.constructor.name}: ${err.message}`
        )
        assert.ok(err.message.length > 0, 'a refusal with nothing to show the finder')
        return true
      },
      `${JSON.stringify(input.slice(0, 40))} was accepted as a tag`
    )
  }
})

test('a tag link with no fragment says to scan the tag, not that it is not a tag', () => {
  // Somebody forwarded the link. They are probably holding the real tag, and
  // sending them away from it would be the one unhelpful thing to say.
  for (const input of [
    'https://example.gov/t/DTBFDDZ',
    'https://tags.example.gov/t/DTB-FDDZ'
  ]) {
    assert.throws(() => parse(input), (err) => {
      assert.match(err.message, /scan the tag/)
      return true
    })
  }
})

test('a port, a subpath and a query do not confuse the parser', () => {
  const withPort = parse('http://192.168.1.10:8120/t/DTBFDDZ#rRr2cgtt-OsSKlo3k6goPQ')
  assert.equal(withPort.origin, 'http://192.168.1.10:8120')
  assert.equal(withPort.tagID, 'DTBFDDZ')

  // A deployment served under a path prefix.
  const nested = parse('https://dnr.sc.gov/wildtag/t/DTBFDDZ#rRr2cgtt-OsSKlo3k6goPQ')
  assert.equal(nested.origin, 'https://dnr.sc.gov')
  assert.equal(nested.tagID, 'DTBFDDZ')
})

test('surrounding whitespace from a scanner is tolerated', () => {
  const tag = parse('  https://example.gov/t/DTBFDDZ#rRr2cgtt-OsSKlo3k6goPQ\n')
  assert.equal(tag.tagID, 'DTBFDDZ')
})
