// Light/dark theme. Light is the hard default, regardless of the OS
// preference -- a person opts into dark explicitly (the toggle in the
// desktop top bar, or the switch in the mobile hamburger menu), and that
// choice is what's remembered. There is a third state, "system", reached
// only by clearing the stored preference (not exposed in the UI here), which
// tracks prefers-color-scheme via the :root[data-theme="system"] rules in
// style.css.
//
// Applied as early as possible via an inline snippet in <head> (see each
// page's <script id="theme-init">) so the page never flashes light-then-dark
// for a person who chose dark; this file only wires up the toggle controls
// once the DOM is ready.
(function () {
  'use strict';

  const KEY = 'wildtag.theme';

  function current() {
    try {
      return localStorage.getItem(KEY) || 'light';
    } catch (_) {
      return 'light';
    }
  }

  function apply(theme) {
    document.documentElement.setAttribute('data-theme', theme);
    try {
      localStorage.setItem(KEY, theme);
    } catch (_) {
      // Private window, or storage full/blocked. The theme still applies
      // for this load; it just won't be remembered next time.
    }
  }

  function toggle() {
    apply(current() === 'dark' ? 'light' : 'dark');
  }

  document.addEventListener('DOMContentLoaded', () => {
    document.querySelectorAll('[data-theme-toggle]').forEach((el) => {
      el.addEventListener('click', toggle);
    });
  });
})();
