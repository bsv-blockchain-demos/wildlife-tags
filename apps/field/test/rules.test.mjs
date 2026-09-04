/**
 * The evaluator must agree with the server, using the server's real profiles.
 *
 * These are the outcomes internal/species/species_test.go pins on the Go side.
 * Running both against the same shipped JSON is what stops the phone telling
 * somebody they may keep an animal the server then refuses -- after they have
 * already killed it.
 *
 * Node strips the types; nothing here imports React Native, which is why the
 * pure half lives in its own module.
 */
import { strict as assert } from 'node:assert'
import { readFileSync } from 'node:fs'
import path from 'node:path'
import test from 'node:test'
import { fileURLToPath } from 'node:url'

import * as rules from '../src/wildtag/rules.ts'

const here = path.dirname(fileURLToPath(import.meta.url))
const profiles = path.resolve(here, '..', '..', '..', 'internal', 'species', 'profiles')

const load = (file) => {
  const p = JSON.parse(readFileSync(path.join(profiles, file), 'utf8'))
  // The registry injects this into every mark-recapture profile; see
  // species.DispositionKey. The shipped JSON does not declare it.
  if (p.workflow === 'mark-recapture') {
    p.vocabs.push({
      key: 'disp',
      label: 'What happened to the animal',
      required: false,
      values: [
        { code: 'RELEASED', label: 'Released with the tag still on' },
        { code: 'HARVESTED', label: 'Kept' }
      ]
    })
  }
  return p
}

const crab = load('calsap.json')
const drum = load('scioce.json')

test('the crab rules refuse what the law refuses', () => {
  const attr = { sex: 'M', stage: 'HARD', gear: 'TRAP' }

  // A sponge female goes back whatever her size.
  const sponge = rules.mustRelease(crab, { cw: 160 }, { ...attr, sex: 'FS' })
  assert.ok(sponge, 'an egg-bearing female was not required to be released')
  assert.match(sponge.reason, /eggs/)

  // Under five inches goes back.
  const small = rules.mustRelease(crab, { cw: 120 }, attr)
  assert.ok(small, 'an undersized crab was not required to be released')
  assert.match(small.reason, /five-inch/)

  // Exactly five inches is legal, and so is anything above it.
  assert.equal(rules.mustRelease(crab, { cw: 127 }, attr), null)
  assert.equal(rules.mustRelease(crab, { cw: 150 }, attr), null)
})

test('only hard-shell crabs are taggable', () => {
  for (const stage of ['PEELER_WHITE', 'PEELER_PINK', 'PEELER_RED', 'SOFT', 'PAPER']) {
    const rule = rules.notTaggable(crab, { cw: 150 }, { sex: 'M', gear: 'TRAP', stage })
    assert.ok(rule, `tagging a ${stage} crab was allowed`)
  }
  assert.equal(rules.notTaggable(crab, { cw: 150 }, { sex: 'M', gear: 'TRAP', stage: 'HARD' }), null)
})

test('red drum has a slot limit rather than a minimum', () => {
  const attr = { sex: 'U', cond: 'GOOD', gear: 'HANDLINE' }
  for (const [mm, mustGoBack] of [[300, true], [381, false], [500, false], [584, false], [700, true]]) {
    const rule = rules.mustRelease(drum, { tl: mm }, attr)
    assert.equal(!!rule, mustGoBack, `${mm} mm`)
  }
  assert.equal(drum.vocabs.find((v) => v.key === 'stage'), undefined, 'a fish has no moult stage')
})

test('a measurement nobody recorded cannot fire a rule about it', () => {
  // The requirement is a separate check; a rule on a missing value must not
  // fire, or an empty form would show a legal refusal before anything is typed.
  assert.equal(rules.mustRelease(crab, {}, { sex: 'M', stage: 'HARD', gear: 'TRAP' }), null)
})

test('tagger-only fields are asked of a tagger and not of a finder', () => {
  const taggerVocabs = rules.vocabs(crab, true).map((v) => v.key)
  const finderVocabs = rules.vocabs(crab, false).map((v) => v.key)

  assert.ok(taggerVocabs.includes('stage'), 'a tagger must stage the crab; that decides taggability')
  assert.ok(!finderVocabs.includes('stage'), 'a member of the public should not be asked to stage a crab')
  assert.ok(!taggerVocabs.includes('disp'), 'a tagger releases the animal by definition')
  assert.ok(finderVocabs.includes('disp'), 'a finder must say what they did with it')
})

test('what is still missing is reported rather than silently blocking', () => {
  const partial = rules.missing(crab, {}, {}, false)
  assert.ok(partial.includes('Carapace width'))
  assert.ok(partial.includes('What happened to the animal'))
  assert.ok(!partial.includes('Shell condition'), 'a finder is not asked to stage a crab')

  const complete = rules.missing(
    crab,
    { cw: 150 },
    { sex: 'M', gear: 'TRAP', disp: 'RELEASED' },
    false
  )
  assert.deepEqual(complete, [])
})

test('a typed decimal becomes the scaled integer the record carries', () => {
  const temp = crab.measures.find((m) => m.key === 'wt')
  // 28.4 * 100 is 2839.9999... in binary floating point. Truncating would put a
  // measurement one hundredth short of what somebody typed into the dataset.
  assert.equal(rules.scaleValue('28.4', temp), 2840)
  assert.equal(rules.scaleValue('', temp), undefined)
  assert.equal(rules.scaleValue('  ', temp), undefined)
  assert.equal(rules.scaleValue('nonsense', temp), undefined)

  const width = crab.measures.find((m) => m.key === 'cw')
  assert.equal(rules.scaleValue('150', width), 150)
  assert.equal(rules.displayValue(2840, temp), '28.4')
  assert.equal(rules.show(crab, 'wt', 2840), '28.40 °C')
})

test('a value outside the profile is refused with a readable range', () => {
  const [message] = rules.outOfRange(crab, { cw: 20 })
  assert.match(message, /Carapace width must be between 50 and 260 mm/)
  assert.deepEqual(rules.outOfRange(crab, { cw: 150 }), [])
})
