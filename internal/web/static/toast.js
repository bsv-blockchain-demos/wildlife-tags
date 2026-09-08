// This app's stand-in for DESIGN.md's "sonner" toasts.
//
// DESIGN.md specifies shadcn/sonner as the only implementation, which
// presumes React; this app is a Go binary serving vanilla HTML with no
// JavaScript framework at all (see wallet.js and vendor/README.md for the
// same reasoning applied to a wallet client and a bundled SDK). So this is
// sonner's contract, hand-rolled: proactive, one per mutation, never two
// stacked copies of the same message, worded in the app's own vocabulary
// ("Batch created", not "Success"), with an inline action where one exists.
//
// Deliberately a single slot rather than a real stack: this app's
// mutations (arm a tag, mint a batch, put one back in service) happen
// seconds apart at the fastest, standing in a boat, not in rapid
// concurrent bursts a stacking layout would need to earn its complexity for.
(function () {
  'use strict';

  let container = null;
  let current = null; // { el, message, kind }
  let hideTimer = null;

  function ensureContainer() {
    if (container) return container;
    container = document.createElement('div');
    container.className = 'toast-stack';
    container.setAttribute('role', 'status');
    container.setAttribute('aria-live', 'polite');
    document.body.appendChild(container);
    return container;
  }

  function resetTimer(duration) {
    if (hideTimer) window.clearTimeout(hideTimer);
    hideTimer = window.setTimeout(dismiss, duration);
  }

  function dismiss() {
    if (!current) return;
    const { el } = current;
    current = null;
    if (hideTimer) {
      window.clearTimeout(hideTimer);
      hideTimer = null;
    }
    el.classList.remove('in');
    window.setTimeout(() => el.remove(), 200);
  }

  function show(message, kind, opts) {
    opts = opts || {};

    // Never stack duplicates: the same message showing again just resets
    // its own clock instead of piling up a second copy underneath it.
    if (current && current.message === message && current.kind === kind) {
      resetTimer(opts.duration || (kind === 'bad' ? 6000 : 4000));
      return;
    }
    if (current) {
      current.el.remove();
      current = null;
      if (hideTimer) window.clearTimeout(hideTimer);
    }

    const el = document.createElement('div');
    el.className = `toast toast-${kind}`;

    // A colored circle carries the meaning now, not the whole card: see
    // the admin console's status pills for the same "color is a small
    // accent, not a wash" choice.
    const iconPath = kind === 'good' ? 'M4 10.5 8.5 15 16 6' : kind === 'bad' ? 'M6 6l8 8M14 6l-8 8' : '';
    if (iconPath) {
      const icon = document.createElement('span');
      icon.className = 'toast-icon';
      icon.setAttribute('aria-hidden', 'true');
      // draw-mark (see style.css): the check or the X draws itself rather
      // than just appearing inside the circle, a beat after the toast
      // itself starts sliding in so the two don't compete for the eye.
      icon.innerHTML =
        `<svg class="draw-mark" viewBox="0 0 20 20" fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="round" stroke-linejoin="round">` +
        `<path d="${iconPath}" style="animation-delay:100ms"/></svg>`;
      el.appendChild(icon);
    }

    const text = document.createElement('span');
    text.className = 'toast-text';
    text.textContent = message;
    el.appendChild(text);

    if (opts.actionLabel && opts.onAction) {
      const btn = document.createElement('button');
      btn.type = 'button';
      btn.className = 'toast-action';
      btn.textContent = opts.actionLabel;
      btn.addEventListener('click', () => {
        opts.onAction();
        dismiss();
      });
      el.appendChild(btn);
    }

    const close = document.createElement('button');
    close.type = 'button';
    close.className = 'toast-close';
    close.setAttribute('aria-label', 'Dismiss');
    close.textContent = '×';
    close.addEventListener('click', dismiss);
    el.appendChild(close);

    ensureContainer().appendChild(el);
    requestAnimationFrame(() => el.classList.add('in'));
    current = { el, message, kind };
    resetTimer(opts.duration || (kind === 'bad' ? 6000 : 4000));
  }

  window.Toast = {
    show: (message, opts) => show(message, 'default', opts),
    success: (message, opts) => show(message, 'good', opts),
    error: (message, opts) => show(message, 'bad', opts),
    dismiss,
  };
})();
