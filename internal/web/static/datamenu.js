// The "Dataset" nav item: a small popover on desktop, anchored under
// whichever trigger opened it (no CSS anchor-positioning relied on, so this
// computes it), and a full-width bottom sheet on mobile, layered above even
// the full-screen mobile menu it may have been opened from. One panel in the
// DOM, shared by the desktop nav's trigger and the mobile menu's -- both
// point at it via aria-controls="dataPanel", data-dataset-trigger.
(function () {
  'use strict';

  function setup() {
    const panel = document.getElementById('dataPanel');
    const triggers = Array.from(document.querySelectorAll('[data-dataset-trigger]'));
    if (!panel || !triggers.length) return;

    const isDesktop = () => window.matchMedia('(min-width: 768px)').matches;

    // Popover position on desktop; the bottom-sheet placement on mobile is
    // pure CSS (see .data-panel's media query), so this is a no-op there.
    function position(trigger) {
      if (!isDesktop()) {
        panel.style.top = '';
        panel.style.left = '';
        return;
      }
      const r = trigger.getBoundingClientRect();
      const width = panel.offsetWidth || 280;
      const left = Math.min(Math.max(16, r.left), window.innerWidth - width - 16);
      panel.style.top = `${r.bottom + 8}px`;
      panel.style.left = `${left}px`;
    }

    function open(trigger) {
      triggers.forEach((t) => t.setAttribute('aria-expanded', String(t === trigger)));
      panel.hidden = false;
      position(trigger);
      panel.classList.remove('open');
      requestAnimationFrame(() => panel.classList.add('open'));
      // A mobile sheet covers the screen the same way the hamburger's
      // full-screen menu does, so it gets the same scroll lock; the desktop
      // popover is small and doesn't need one.
      if (!isDesktop()) document.body.style.overflow = 'hidden';
    }

    function close() {
      triggers.forEach((t) => t.setAttribute('aria-expanded', 'false'));
      panel.classList.remove('open');
      document.body.style.overflow = '';
      window.setTimeout(() => { panel.hidden = true; }, 250);
    }

    triggers.forEach((trigger) => {
      trigger.addEventListener('click', () => {
        (trigger.getAttribute('aria-expanded') === 'true' ? close : () => open(trigger))();
      });
    });

    document.addEventListener('keydown', (e) => {
      if (e.key === 'Escape' && !panel.hidden) close();
    });
    document.addEventListener('click', (e) => {
      if (panel.hidden) return;
      if (panel.contains(e.target)) return;
      if (triggers.some((t) => t.contains(e.target))) return;
      close();
    });
    // A resize across the breakpoint (rotating a tablet) leaves a
    // desktop-positioned popover in the wrong place for a mobile sheet, and
    // vice versa; simplest correct fix is to close it.
    window.addEventListener('resize', () => {
      if (!panel.hidden) close();
    });
  }

  document.addEventListener('DOMContentLoaded', setup);
})();
