// First run: a short walkthrough shown once, before anything else on the
// page, to whoever this is the first of our pages they have ever opened --
// which, for a QR code wired to a crab, is at least as often a shared
// /t/{tagID} link as it is the dashboard. Modelled on the same shape a
// wallet app's own first run uses: art held to one side (a band above the
// form on a phone, a column beside it on a desktop), a short run of steps
// with a skip always available, and a last screen that goes somewhere
// rather than just closing.
//
// The veil itself is shown before this file even runs -- see the inline
// script in each page's <head>, the same technique theme.js uses to avoid
// a flash -- so there is nothing here to do on a repeat visit but stay out
// of the way.
(function () {
  'use strict';

  const $ = (id) => document.getElementById(id);
  const STORAGE_KEY = 'wildtag.onboarded.v1';

  // The tag mark, as an outline -- the same path as the favicon and every
  // other place this app draws its own icon.
  const TAG_PATH = 'M11 3H4.5A1.5 1.5 0 0 0 3 4.5V11l7.3 7.3a1.5 1.5 0 0 0 2.1 0l5.9-5.9a1.5 1.5 0 0 0 0-2.1L11 3Z';
  const CHECK_PATH = 'M4 10.5 8.5 15 16 6';

  function svg(inner, opts) {
    opts = opts || {};
    const stroke = opts.stroke !== false;
    const cls = opts.className ? ` class="${opts.className}"` : '';
    return (
      `<svg${cls} viewBox="0 0 20 20" fill="${opts.fill || 'none'}" ${stroke ? 'stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round"' : ''} aria-hidden="true">${inner}</svg>`
    );
  }

  // ---- the scenes -----------------------------------------------------
  //
  // Ghost blocks in neutral, one accent-colored focal element per scene --
  // structure says nothing, color marks the one thing the step is about.
  // Each plays once on arrival and holds still; see .onboard-rise/-settle/
  // -sweep in style.css, and DESIGN's motion rules for why nothing loops.

  function welcomeScene() {
    return (
      `<div class="onboard-settle" style="width:76px;height:76px;border-radius:50%;background:var(--grad-estuary);display:flex;align-items:center;justify-content:center;box-shadow:var(--shadow-raised);">` +
      `<span style="width:34px;height:34px;color:#fff;">${svg(`<path d="${TAG_PATH}"/><circle cx="7" cy="7" r="1.1"/>`)}</span>` +
      `</div>`
    );
  }

  function scanScene() {
    return (
      `<div style="display:flex;align-items:center;gap:14px;">` +
      `<div class="onboard-surface onboard-rise" style="width:76px;height:96px;padding:8px;display:grid;grid-template-columns:repeat(3,1fr);grid-template-rows:repeat(3,1fr);gap:3px;">` +
      [0, 1, 2, 3, 4, 5, 6, 7, 8]
        .map((i) => `<span class="onboard-ghost" style="border-radius:2px;${[0, 2, 6].includes(i) ? 'background:var(--accent);' : ''}"></span>`)
        .join('') +
      `</div>` +
      `<span class="onboard-rise" style="animation-delay:120ms;color:var(--ink-dim);width:20px;height:20px;">${svg('<path d="M4 10h12M11 5l5 5-5 5"/>')}</span>` +
      `<div class="onboard-surface onboard-settle" style="animation-delay:220ms;width:44px;height:44px;border-radius:50%;display:flex;align-items:center;justify-content:center;background:var(--good);">` +
      // draw-mark (see style.css): the circle settles at 220ms + --dur-base
      // (roughly 470ms all in); the check waits for that rather than
      // riding along already drawn, so "the scan lands" and "it's good"
      // read as two beats, not one.
      `<span style="width:22px;height:22px;color:#fff;">${svg(`<path d="${CHECK_PATH}" stroke-width="2.2" style="animation-delay:470ms"/>`, { className: 'draw-mark' })}</span>` +
      `</div>` +
      `</div>`
    );
  }

  function recordScene() {
    return (
      `<div style="width:100%;max-width:220px;">` +
      `<div class="onboard-surface" style="padding:10px;">` +
      [
        ['var(--accent)', '70%'],
        ['', '90%'],
        ['', '55%'],
      ]
        .map(
          ([color, width], i) =>
            `<div class="onboard-sweep" style="animation-delay:${i * 90}ms;display:flex;align-items:center;gap:8px;${i ? 'margin-top:8px;' : ''}">` +
            `<span class="onboard-ghost" style="width:10px;height:10px;flex-shrink:0;${color ? `background:${color};` : ''}"></span>` +
            `<span class="onboard-ghost" style="height:6px;width:${width};"></span>` +
            `</div>`
        )
        .join('') +
      `</div>` +
      `</div>`
    );
  }

  function honestyScene() {
    // draw-mark (see style.css): each row rises in over --dur-base, so its
    // own mark -- the check or the cross this row exists to draw attention
    // to -- waits until that settles before drawing itself, rather than
    // riding in already formed.
    const row = (ok, delay) =>
      `<div class="onboard-surface onboard-rise" style="animation-delay:${delay}ms;display:flex;align-items:center;gap:8px;padding:9px 12px;">` +
      `<span style="width:16px;height:16px;flex-shrink:0;color:${ok ? 'var(--good)' : 'var(--warn)'};">` +
      svg(
        (ok ? `<path d="${CHECK_PATH}"` : '<path d="M6 6l8 8M14 6l-8 8"') +
          ` stroke-width="2.2" style="animation-delay:${delay + 250}ms"/>`,
        { className: 'draw-mark' }
      ) +
      `</span>` +
      `<span class="onboard-ghost" style="height:6px;flex:1;"></span>` +
      `</div>`;
    return `<div style="width:100%;max-width:220px;display:flex;flex-direction:column;gap:8px;">${row(true, 0)}${row(false, 90)}</div>`;
  }

  function readyScene() {
    return (
      `<div class="onboard-surface onboard-settle" style="width:64px;height:64px;border-radius:50%;display:flex;align-items:center;justify-content:center;">` +
      // draw-mark (see style.css): drawn after the circle's own --dur-base
      // settle, the same "arrives, then confirmed" beat as everywhere else
      // this app uses the technique.
      `<span style="width:28px;height:28px;color:var(--good);">${svg(`<path d="${CHECK_PATH}" stroke-width="2.4" style="animation-delay:250ms"/>`, { className: 'draw-mark' })}</span>` +
      `</div>`
    );
  }

  // ---- the steps --------------------------------------------------------
  //
  // Four informational steps, the same regardless of which page first run
  // started on, plus a last screen whose next-actions differ by context --
  // see build()'s `ctx` argument. Nothing here collects anything: a finder
  // has no profile to set up, so unlike a wallet's own first run this is a
  // walkthrough to read, not a form to fill in.
  const STEPS = [
    {
      eyebrow: 'SCDNR wildlife tags',
      title: 'A reward locked to the tag itself',
      body: 'Scan a tag, report what you found, and get paid in seconds. No mailing anything in, no waiting weeks for a check that might not come.',
      caption: 'the reward is the record',
      scene: welcomeScene,
    },
    {
      eyebrow: 'How it works',
      title: 'One scan, one transaction',
      body: "The QR code on the tag opens a page like this one. Confirm what you found and where, and the same transaction that records it pays you. The payment and the data can't come apart.",
      caption: 'report it, get paid',
      scene: scanScene,
    },
    {
      eyebrow: 'What goes on chain',
      title: 'Your position, in your own words',
      body: "Reporting writes your phone's own position and the measurements you enter into the record, permanently. Nobody can edit it later, not a crabber, not SCDNR.",
      caption: 'nothing quietly moves',
      scene: recordScene,
    },
    {
      eyebrow: "What this doesn't prove",
      title: 'A record, not a witness',
      body: "The chain proves when something happened and that whoever reported it held the tag. It can't prove where you were standing, only where your phone said it was.",
      caption: 'an attestation, not a witness',
      scene: honestyScene,
    },
  ];

  function readyStep(ctx) {
    const links =
      ctx === 'redeem'
        ? [
            { href: '#form', label: 'Report this tag', body: 'Confirm what you found below and get paid.', dismiss: true },
            { href: '/', label: 'See the live dashboard', body: 'What the programme is paying out and holding, right now.' },
          ]
        : [
            { href: '#stats', label: 'Start exploring', body: 'The dashboard, live.', dismiss: true },
            { href: '/about', label: 'Read about the programme', body: 'Why this exists, and what it costs to try.' },
          ];
    return { title: 'Ready', body: 'That covers it.', links };
  }

  // ---- the wizard ---------------------------------------------------------

  // releaseTrap undoes window.FocusTrap.activate() from the last time this
  // veil opened -- see build()/finish(). Module-level rather than local to
  // build() because finish() is a separate function that needs it too, and
  // because "Replay first run" (see the dev-mode registration at the bottom
  // of this file) can call build() again on a veil that never got a
  // matching finish(), which must release the previous trap before
  // installing a new one rather than stacking listeners.
  let releaseTrap = null;

  function finish() {
    try {
      localStorage.setItem(STORAGE_KEY, '1');
    } catch (_) {
      // Private window, or storage blocked. The veil still closes for this
      // load; it just may show again next time, which is a much smaller
      // problem than never being able to close it at all.
    }
    document.documentElement.classList.remove('onboarding-pending');
    if (releaseTrap) {
      releaseTrap();
      releaseTrap = null;
    }
  }

  function build(ctx) {
    const veil = $('onboardVeil');
    if (!veil) return;

    if (releaseTrap) releaseTrap();
    releaseTrap = window.FocusTrap.activate(veil);

    let step = 0;
    const total = STEPS.length + 1; // + the ready screen

    function renderDots() {
      const dots = $('onboardDots');
      if (!dots) return;
      dots.innerHTML = Array.from({ length: total })
        .map((_, i) => `<span class="${i === step ? 'current' : i < step ? 'done' : ''}"></span>`)
        .join('');
    }

    function renderStep() {
      const isReady = step === STEPS.length;
      const data = isReady ? readyStep(ctx) : STEPS[step];

      $('onboardScene').innerHTML = `<div class="onboard-scene-inner">${(isReady ? readyScene : data.scene)()}</div>`;
      $('onboardSkip').classList.toggle('hidden', isReady);
      $('onboardEyebrow').textContent = isReady ? '' : data.eyebrow;
      $('onboardTitle').textContent = data.title;

      if (isReady) {
        $('onboardBody').innerHTML =
          `<p>${data.body}</p>` +
          `<div class="onboard-ready-links">` +
          data.links
            .map(
              (l) =>
                `<a class="onboard-ready-link" href="${l.href}"${l.dismiss ? ' data-dismiss' : ''}>` +
                svg('<path d="M4 10h12M11 5l5 5-5 5"/>') +
                `<span><span style="display:block;">${l.label}</span>` +
                `<span style="display:block;font-weight:400;color:var(--ink-dim);font-size:13px;margin-top:1px;">${l.body}</span></span></a>`
            )
            .join('') +
          `</div>`;
      } else {
        $('onboardBody').innerHTML = `<p>${data.body}</p><p class="onboard-caption">${data.caption}</p>`;
      }

      $('onboardActions').innerHTML = isReady
        ? ''
        : step === 0
          ? `<button type="button" class="primary wide" id="onboardNext">Continue</button>`
          : `<button type="button" class="wide" id="onboardBack">Back</button><button type="button" class="primary wide" id="onboardNext">Continue</button>`;

      renderDots();

      if (!isReady) {
        $('onboardNext').addEventListener('click', () => {
          step += 1;
          renderStep();
        });
        const back = $('onboardBack');
        if (back) back.addEventListener('click', () => {
          step -= 1;
          renderStep();
        });
      } else {
        // Any ready-screen link both navigates and ends first run -- a
        // person landing on /report clicked from here should never see
        // this veil again just because the click happened before the flag
        // was set.
        $('onboardBody').querySelectorAll('.onboard-ready-link').forEach((a) => {
          a.addEventListener('click', () => finish());
        });
      }
    }

    $('onboardSkip').addEventListener('click', finish);
    document.addEventListener('keydown', (e) => {
      if (e.key === 'Escape' && document.documentElement.classList.contains('onboarding-pending')) finish();
    });

    renderStep();
  }

  document.addEventListener('DOMContentLoaded', () => {
    const ctx = document.body.dataset.onboard || 'home';
    if (document.documentElement.classList.contains('onboarding-pending')) {
      build(ctx);
    }

    // Registered here rather than in each page's own script, so this
    // stays a one-file feature: whichever page loaded onboarding.js gets
    // a "Replay first run" scenario in its own dev panel, not just the
    // pages whose scripts happened to remember to add one. Runs before
    // devpanel.js's own DOMContentLoaded listener, since this file is
    // loaded first in every page's <script> list.
    const page = document.body.dataset.page;
    if (page) {
      window.DevMocks = window.DevMocks || {};
      window.DevMocks[page] = window.DevMocks[page] || {};
      window.DevMocks[page]['Replay first run'] = () => {
        document.documentElement.classList.add('onboarding-pending');
        build(ctx);
      };
    }
  });
})();
