// Builds the pages' forms from a real schema document and reads them back.
//
// static/schema.js is what turns a species profile into the inputs a person
// fills in, and reads those inputs back into the two maps a record carries. It
// is the piece that makes the pages species-agnostic, and nothing else tests
// it: TestSchemaDrivesTheForms only checks that no vocabulary was hardcoded,
// which a form that renders nothing at all would also pass.
//
// The DOM here is a stub rather than jsdom, on purpose. schema.js touches four
// things -- innerHTML, querySelectorAll, dataset and value -- and a
// twenty-line stub that supports exactly those keeps this test a dependency-free
// `node` invocation, the same way sign_test.js is.
const fs = require('fs')
const path = require('path')

const Schema = require(path.join(__dirname, '..', 'static', 'schema.js'))

const doc = JSON.parse(fs.readFileSync(process.argv[2], 'utf8'))

// --- the smallest DOM that schema.js actually uses --------------------------

/**
 * parse pulls the fields back out of rendered HTML.
 *
 * Regex over HTML is normally a mistake; here the HTML is produced two hundred
 * lines away in the same repository, in one function, and pulling in a parser
 * to read it would be more moving parts than the thing under test.
 */
function elementsFrom(html) {
  const out = []
  const inputs = html.matchAll(/<input ([^>]*)>/g)
  for (const [, attrs] of inputs) out.push(attributesOf(attrs, 'input'))
  const selects = html.matchAll(/<select ([^>]*)>([\s\S]*?)<\/select>/g)
  for (const [, attrs, body] of selects) {
    const el = attributesOf(attrs, 'select')
    // [^>]* between the value and the closing angle bracket, not a bare ">":
    // an <option> for a vocab value with an icon (species.VocabValue.Icon)
    // also carries a data-icon attribute.
    el.options = [...body.matchAll(/<option value="([^"]*)"[^>]*>([^<]*)<\/option>/g)].map((m) => ({
      code: m[1],
      label: m[2]
    }))
    out.push(el)
  }
  return out
}

function attributesOf(attrs, tag) {
  const el = { tag, dataset: {}, value: '', required: / required/.test(attrs) }
  for (const [, name, value] of attrs.matchAll(/([a-z-]+)="([^"]*)"/g)) {
    if (name.startsWith('data-')) el.dataset[camel(name.slice(5))] = value
    else el[name] = value
  }
  return el
}

const camel = (s) => s.replace(/-([a-z])/g, (_, c) => c.toUpperCase())

/** container is what renderFields writes into and read() reads back from. */
function container() {
  const box = {
    innerHTML: '',
    elements: [],
    querySelectorAll(selector) {
      const key = selector === '[data-meas]' ? 'meas' : 'attr'
      return box.elements.filter((el) => el.dataset[key] !== undefined)
    },
    querySelector(selector) {
      if (selector === '[data-disp-note]') return { textContent: '' }
      return null
    }
  }
  // renderFields assigns innerHTML; parse it into elements at that moment.
  Object.defineProperty(box, 'innerHTML', {
    get: () => box._html ?? '',
    set: (html) => {
      box._html = html
      box.elements = elementsFrom(html)
    }
  })
  return box
}

// --- the checks -------------------------------------------------------------

const failures = []
const check = (ok, what) => {
  if (!ok) failures.push(what)
}

// schema.js caches through localStorage and fetches; neither exists in node.
global.localStorage = {
  store: {},
  getItem(k) {
    return this.store[k] ?? null
  },
  setItem(k, v) {
    this.store[k] = v
  }
}
global.fetch = async () => ({
  ok: true,
  status: 200,
  headers: { get: () => '"stub"' },
  json: async () => doc
})

async function main() {
  await Schema.load()

  const crab = Schema.profile('CALSAP')
  const drum = Schema.profile('SCIOCE')
  check(!!crab && !!drum, 'both shipped profiles are reachable by code')

  // A finder's crab form: the profile's fields, minus the tagger-only ones,
  // plus the disposition.
  const finder = container()
  Schema.renderFields(crab, finder, { tagging: false })
  const finderKeys = finder.elements.map((el) => el.dataset.meas ?? el.dataset.attr).sort()
  check(finderKeys.includes('cw'), "a finder is asked for the crab's width")
  check(finderKeys.includes('disp'), 'a finder is asked what they did with it')
  check(!finderKeys.includes('stage'), 'a finder is not asked to stage a crab')

  // A tagger's crab form: the reverse.
  const tagger = container()
  Schema.renderFields(crab, tagger, { tagging: true })
  const taggerKeys = tagger.elements.map((el) => el.dataset.meas ?? el.dataset.attr).sort()
  check(taggerKeys.includes('stage'), 'a tagger must stage the crab; it decides taggability')
  check(!taggerKeys.includes('disp'), 'a tagger releases the animal by definition')

  // A fish form is a different form, from the same code.
  const fish = container()
  Schema.renderFields(drum, fish, { tagging: true })
  const fishKeys = fish.elements.map((el) => el.dataset.meas ?? el.dataset.attr).sort()
  check(fishKeys.includes('tl'), 'a red drum is measured by total length')
  check(!fishKeys.includes('cw'), 'a fish has no carapace')
  check(fishKeys.includes('cond'), 'a red drum records its condition at release')

  // Every vocabulary renders its own codes, not a fixed list.
  const sex = fish.elements.find((el) => el.dataset.attr === 'sex')
  check(
    JSON.stringify(sex.options.map((o) => o.code)) === JSON.stringify(['U', 'M', 'F']),
    "a fish's sex codes are the fish profile's, not the crab's"
  )

  // Reading back: a typed decimal becomes the scaled integer the record carries.
  const temp = finder.elements.find((el) => el.dataset.meas === 'wt')
  const width = finder.elements.find((el) => el.dataset.meas === 'cw')
  const dispEl = finder.elements.find((el) => el.dataset.attr === 'disp')
  temp.value = '28.4'
  width.value = '150'
  dispEl.value = 'RELEASED'
  const read = Schema.read(finder)
  check(read.meas.wt === 2840, `28.4 degrees read back as ${read.meas.wt}, want the scaled 2840`)
  check(read.meas.cw === 150, `150 mm read back as ${read.meas.cw}`)
  check(read.attr.disp === 'RELEASED', 'the disposition did not read back')
  check(!('sal' in read.meas), 'a blank measurement was sent as zero rather than omitted')

  // The rules, evaluated the way the page evaluates them.
  check(
    !!Schema.mustRelease(crab, { cw: 120 }, { sex: 'M' }),
    'an undersized crab was not flagged for release'
  )
  check(
    !!Schema.mustRelease(crab, { cw: 160 }, { sex: 'FS' }),
    'an egg-bearing female was not flagged for release'
  )
  check(Schema.mustRelease(crab, { cw: 150 }, { sex: 'M' }) === null, 'a legal crab was flagged')
  check(
    !!Schema.notTaggable(crab, { cw: 150 }, { stage: 'SOFT' }),
    'a soft-shell crab was accepted for tagging'
  )
  check(
    !!Schema.mustRelease(drum, { tl: 700 }, {}),
    'a red drum over the slot maximum was not flagged'
  )
  check(Schema.mustRelease(drum, { tl: 500 }, {}) === null, 'an in-slot red drum was flagged')

  // complete() is what enables the submit button.
  check(
    !Schema.complete(crab, {}, {}, { tagging: false }),
    'an empty form reported itself as complete'
  )
  check(
    Schema.complete(crab, { cw: 150 }, { sex: 'M', gear: 'TRAP', disp: 'RELEASED' }, { tagging: false }),
    'a filled-in finder form reported itself as incomplete'
  )
  check(
    !Schema.complete(crab, { cw: 150 }, { sex: 'M', gear: 'TRAP' }, { tagging: true }),
    'a tagger form with no shell condition reported itself as complete'
  )

  process.stdout.write(JSON.stringify({ failures }))
}

main().catch((err) => {
  process.stderr.write(String(err && err.stack ? err.stack : err))
  process.exit(1)
})
