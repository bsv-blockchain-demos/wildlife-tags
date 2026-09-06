// A plain ARIA tabs pattern: click a [role=tab], show the [role=tabpanel] it
// names via aria-controls, hide the rest. One tabbar per page is assumed;
// if a page ever needs two, this can take a container argument instead of
// querying the whole document.
//
// The shown panel fades and settles in rather than just appearing -- the
// same opacity/transform-only treatment used everywhere else motion happens
// in this app (see accordion.js), so switching tabs doesn't feel like the
// one interaction in the console that has no transition at all.
(function () {
  'use strict';

  function select(tab) {
    const tabs = document.querySelectorAll('.tabbar [role="tab"]');
    tabs.forEach((t) => {
      const on = t === tab;
      t.setAttribute('aria-selected', String(on));
      const panel = document.getElementById(t.getAttribute('aria-controls'));
      if (!panel) return;
      if (on) {
        panel.hidden = false;
        panel.classList.remove('in');
        requestAnimationFrame(() => panel.classList.add('in'));
      } else {
        panel.classList.remove('in');
        panel.hidden = true;
      }
    });
  }

  document.addEventListener('DOMContentLoaded', () => {
    document.querySelectorAll('.tabbar [role="tab"]').forEach((tab) => {
      tab.addEventListener('click', () => select(tab));
    });
    // The first tab's panel is visible in the markup by default (no
    // `hidden`) so the console isn't blank before JS runs; give it the
    // same "in" state instantly, with no animation, on load.
    const current = document.querySelector('.tabbar [role="tab"][aria-selected="true"]');
    if (current) {
      const panel = document.getElementById(current.getAttribute('aria-controls'));
      if (panel) panel.classList.add('in');
    }
  });
})();
