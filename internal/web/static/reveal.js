// A subtle, staggered entrance for each page's top-level content blocks as
// they scroll into view.
//
// IntersectionObserver only -- never a scroll listener recomputing
// getBoundingClientRect on every frame, which is the difference between
// this being free and this being the reason a long page drops frames on a
// mid-range phone. Each element animates once (unobserved right after) and
// only opacity/transform move, so there is nothing here for a layout or
// paint to redo -- see DESIGN's motion rules, the same reason the "Paid"
// confirmation and the skeleton pulse are built the same way.
(function () {
  'use strict';

  // One level, not a nested cascade: a .card's own .grad-cards or stat
  // tiles appear with their parent rather than staggering a second time
  // inside it, which read as busy rather than premium when tried.
  //
  // :not([data-no-reveal]) excludes cards a flow state shows rather than a
  // scroll -- redeem.js's #pay/#paid/#prov, most notably #paid, which has
  // its own signature entrance (see .paid-badge) that has to play the
  // instant it is shown, not wait on an intersection callback to catch up.
  const SELECTOR = '.page-title, .card:not([data-no-reveal])';
  const STAGGER_MS = 60;
  // However many happen to intersect in the same batch (typically what is
  // above the fold on load) -- past that, extra delay would just be a long
  // pause on an item nobody is watching for yet.
  const MAX_STAGGER = 8;

  function setup() {
    if (window.matchMedia('(prefers-reduced-motion: reduce)').matches) return;
    if (!('IntersectionObserver' in window)) return; // reveal is a nicety; everything just shows

    const els = Array.from(document.querySelectorAll(SELECTOR));
    if (!els.length) return;

    els.forEach((el, i) => {
      el.classList.add('reveal');
      el.style.transitionDelay = `${Math.min(i, MAX_STAGGER) * STAGGER_MS}ms`;
    });

    const io = new IntersectionObserver(
      (entries) => {
        for (const entry of entries) {
          if (!entry.isIntersecting) continue;
          entry.target.classList.add('reveal-in');
          io.unobserve(entry.target);
        }
      },
      { threshold: 0.1, rootMargin: '0px 0px -10% 0px' }
    );
    els.forEach((el) => io.observe(el));
  }

  document.addEventListener('DOMContentLoaded', setup);
})();
