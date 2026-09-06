// The About page's section accordions and inline "Read more" expanders.
//
// Both use the same shallow disclosure pattern: a <button aria-expanded>
// controls a sibling body via aria-controls, toggling `hidden` and fading
// the revealed content in. There is no height animation -- animating
// height/auto forces layout on every frame, which is exactly what this
// app's motion rules exist to avoid (only transform and opacity animate
// anywhere in this app; see style.css). The body's final size is correct
// the instant it becomes visible; only its opacity eases in, and only when
// a person actually clicks something -- a section that starts open on page
// load must not visibly fade in on its own.
(function () {
  'use strict';

  // setInitialState mirrors the markup's own aria-expanded onto `hidden`,
  // with no transition: this runs once at load, before anyone has touched
  // anything, and a fade-in here would just be an unexplained flash.
  function setInitialState(trigger, body) {
    const open = trigger.getAttribute('aria-expanded') === 'true';
    body.hidden = !open;
    body.classList.toggle('in', open);
  }

  // toggle is the animated path, used only from a click.
  function toggle(trigger, body, labels) {
    const open = trigger.getAttribute('aria-expanded') !== 'true';
    trigger.setAttribute('aria-expanded', String(open));
    if (labels) trigger.textContent = open ? labels.close : labels.open;

    if (open) {
      body.hidden = false;
      body.classList.remove('in');
      // Toggling `hidden` and starting the opacity transition in the same
      // frame skips the transition; give the browser one paint first.
      requestAnimationFrame(() => body.classList.add('in'));
    } else {
      body.classList.remove('in');
      body.hidden = true;
    }
  }

  function wire(trigger, body, labels) {
    if (!trigger || !body) return;
    setInitialState(trigger, body);
    trigger.addEventListener('click', () => toggle(trigger, body, labels));
  }

  document.addEventListener('DOMContentLoaded', () => {
    document.querySelectorAll('.accordion').forEach((card) => {
      wire(card.querySelector('.accordion-trigger'), card.querySelector('.accordion-body'));
    });

    document.querySelectorAll('[data-readmore]').forEach((wrap) => {
      wire(wrap.querySelector('.readmore'), wrap.querySelector('.readmore-body'), {
        open: 'Read more',
        close: 'Show less',
      });
    });
  });
})();
