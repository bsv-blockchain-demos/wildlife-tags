// A minimal, dependency-free focus trap for this app's few true modals: the
// confirm-before-arm sheet, the QR scanner overlay, the first-run onboarding
// veil. Every one of them already carries role="dialog" aria-modal="true" --
// that attribute is accessibility's DECLARATION that keyboard and
// screen-reader focus cannot leave the element while it is open. Nothing in
// the platform enforces that just because the attribute is present; this is
// the enforcement, and until now none of them had it.
//
// Two mechanisms, deliberately both:
//
//   - inert on everything outside the dialog removes the rest of the page
//     from the accessibility tree entirely, which is what stops a screen
//     reader's own virtual cursor from wandering into the page behind a
//     modal -- a plain Tab-key handler alone cannot reach that, since arrow-
//     key browsing in a screen reader's browse mode does not fire keydown
//     events this page ever sees.
//   - a Tab/Shift+Tab handler on the dialog itself guarantees focus wraps
//     within it rather than escaping to browser chrome at either end, which
//     inert does not promise on every browser.
(function () {
  'use strict';

  const FOCUSABLE =
    'a[href], button:not([disabled]), textarea:not([disabled]), input:not([disabled]), ' +
    'select:not([disabled]), [tabindex]:not([tabindex="-1"])';

  // inertify walks from root up to <body>, marking every OTHER child inert
  // at each level -- root's own ancestor chain is left untouched. That
  // makes this work identically whether root is a direct child of <body>
  // (the onboarding veil) or nested several levels deep (the confirm sheet
  // and scanner overlay both sit inside <main>), with no page-specific
  // knowledge required.
  function inertify(root) {
    const marked = [];
    let node = root;
    while (node && node !== document.body) {
      const parent = node.parentElement;
      if (!parent) break;
      for (const sibling of parent.children) {
        if (sibling !== node && !sibling.hasAttribute('inert')) {
          sibling.setAttribute('inert', '');
          marked.push(sibling);
        }
      }
      node = parent;
    }
    return () => marked.forEach((el) => el.removeAttribute('inert'));
  }

  function focusables(root) {
    return Array.from(root.querySelectorAll(FOCUSABLE)).filter((el) => el.offsetParent !== null);
  }

  // activate makes `root` the only thing on the page a keyboard or screen
  // reader can reach, moves focus onto the dialog itself (not its first
  // control) so an assistive technology announces its accessible name
  // before anything else, and returns a deactivate() that undoes all of it
  // and hands focus back to whatever opened the dialog.
  function activate(root) {
    const previouslyFocused = document.activeElement;
    const restoreInert = inertify(root);

    function onKeydown(e) {
      if (e.key !== 'Tab') return;
      const items = focusables(root);
      if (!items.length) {
        e.preventDefault();
        return;
      }
      const first = items[0];
      const last = items[items.length - 1];
      if (e.shiftKey && document.activeElement === first) {
        e.preventDefault();
        last.focus();
      } else if (!e.shiftKey && document.activeElement === last) {
        e.preventDefault();
        first.focus();
      } else if (!items.includes(document.activeElement)) {
        e.preventDefault();
        first.focus();
      }
    }
    root.addEventListener('keydown', onKeydown);

    if (!root.hasAttribute('tabindex')) root.setAttribute('tabindex', '-1');
    // A frame late: several of these dialogs animate open (the confirm
    // sheet slides up, the onboarding veil settles in), and focusing an
    // element while that is still running can make some browsers scroll to
    // wherever it will end up rather than where it visually is right now.
    requestAnimationFrame(() => root.focus());

    return function deactivate() {
      root.removeEventListener('keydown', onKeydown);
      restoreInert();
      if (previouslyFocused && typeof previouslyFocused.focus === 'function') {
        previouslyFocused.focus();
      }
    };
  }

  window.FocusTrap = { activate };
})();
