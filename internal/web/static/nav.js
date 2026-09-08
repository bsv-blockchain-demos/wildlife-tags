// The top bar's mobile behaviour: hamburger button <-> full-screen overlay
// menu. Shared by every page that has one; a page with no #navToggle (the
// redemption flow, which has no nav at all by design) just has nothing to
// wire up here.
(function () {
  'use strict';

  function setupMenu(toggle, menu) {
    if (!toggle || !menu) return;

    const isOpen = () => toggle.getAttribute('aria-expanded') === 'true';
    // Undoes window.FocusTrap.activate() from the last time this menu
    // opened -- see open()/close(). This is a full-screen overlay exactly
    // like the confirm-before-arm sheet and the QR scanner, which already
    // get this treatment; missing it here just meant a keyboard or screen
    // reader user could tab straight out of the "open" menu into the page
    // supposedly hidden behind it.
    let releaseTrap = null;

    function open() {
      toggle.setAttribute('aria-expanded', 'true');
      menu.classList.add('open');
      menu.setAttribute('aria-hidden', 'false');
      // A full-screen menu behind a scrollable page is a menu you can
      // accidentally scroll past; lock the page while it's open.
      document.body.style.overflow = 'hidden';
      if (window.FocusTrap) releaseTrap = window.FocusTrap.activate(menu);
    }

    function close() {
      toggle.setAttribute('aria-expanded', 'false');
      menu.classList.remove('open');
      menu.setAttribute('aria-hidden', 'true');
      document.body.style.overflow = '';
      if (releaseTrap) {
        releaseTrap();
        releaseTrap = null;
      }
    }

    toggle.addEventListener('click', () => (isOpen() ? close() : open()));
    menu.querySelectorAll('a').forEach((a) => a.addEventListener('click', close));

    window.addEventListener('keydown', (e) => {
      if (e.key === 'Escape' && isOpen()) close();
    });

    // Widening past the breakpoint (rotating a tablet) shouldn't leave the
    // overlay open with no trigger left visible to close it.
    const mq = window.matchMedia('(min-width: 768px)');
    const onChange = (e) => { if (e.matches) close(); };
    if (mq.addEventListener) mq.addEventListener('change', onChange);
    else mq.addListener(onChange); // Safari < 14
  }

  document.addEventListener('DOMContentLoaded', () => {
    setupMenu(document.getElementById('navToggle'), document.getElementById('mobileMenu'));
  });
})();
