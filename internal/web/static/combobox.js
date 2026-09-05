// Progressively enhances a <select> into a searchable popover with an icon
// in front of each entry.
//
// The <select> is not replaced, only hidden: it stays the real form control,
// still answers to .value, still fires a real 'change' event on selection --
// every existing consumer (schema.js's read(), admin.js's clearing and
// defaulting logic, the required-field checks) keeps working unmodified,
// because as far as they can tell nothing changed. This file only owns the
// visual layer in front of it.
//
// Built rather than reached for off the shelf for the same reason every
// other interactive piece in this app is hand-rolled (see wallet.js,
// tabs.js, datamenu.js): no JavaScript framework here for a component
// library to plug into.
(function () {
  'use strict';

  // A small registry of simple line icons for the vocabulary values that
  // have an obvious, universal pictogram -- a crab pot, a hook, the sex
  // symbols. A value with no obvious one (a moult stage, a life-history
  // code) just sets no icon on the Go side (species.VocabValue.Icon) and
  // renders with none here rather than a fabricated shape nobody would
  // recognise.
  const ICONS = {
    'gender-male': '<circle cx="8" cy="12" r="5"/><path d="M12 8l5-5M12 3h5v5"/>',
    'gender-female': '<circle cx="10" cy="7" r="5"/><path d="M10 12v6M7 16h6"/>',
    'gender-unknown': '<circle cx="10" cy="10" r="7"/><path d="M7.8 8a2.3 2.3 0 1 1 3.3 2c-.7.4-1.1.9-1.1 2"/><circle cx="10" cy="14.3" r=".4" fill="currentColor" stroke="none"/>',
    'gear-trap': '<rect x="4" y="5" width="12" height="10" rx="1"/><path d="M4 8.5h12M4 12h12M8 5v10M12 5v10"/>',
    'gear-line': '<path d="M3 16c2-7 12-7 14 0"/><circle cx="10" cy="10.3" r=".9" fill="currentColor" stroke="none"/>',
    'gear-net': '<path d="M10 3 3 7v7l7 6 7-6V7Z"/><path d="M3 7h14M6 5.3v13.4M14 5.3v13.4M3 10.5h14M3 13.5h14"/>',
    'gear-hook': '<path d="M8 3v9a3 3 0 1 0 3-3"/>',
    'gear-other': '<circle cx="5" cy="10" r="1.2" fill="currentColor" stroke="none"/><circle cx="10" cy="10" r="1.2" fill="currentColor" stroke="none"/><circle cx="15" cy="10" r="1.2" fill="currentColor" stroke="none"/>',
    release: '<path d="M4 12c2-5 10-5 12 0"/><path d="M10 12V4M7.5 6.5 10 4l2.5 2.5"/>',
    harvest: '<path d="M4 8h12l-1.3 8.5a1 1 0 0 1-1 .8H6.3a1 1 0 0 1-1-.8L4 8Z"/><path d="M7 8a3 3 0 0 1 6 0"/>',
    tag: '<path d="M11 3H4.5A1.5 1.5 0 0 0 3 4.5V11l7.3 7.3a1.5 1.5 0 0 0 2.1 0l5.9-5.9a1.5 1.5 0 0 0 0-2.1L11 3Z"/><circle cx="7" cy="7" r="1.1"/>',
  };

  function iconSVG(name) {
    if (!name || !ICONS[name]) return '';
    return `<svg viewBox="0 0 20 20" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round">${ICONS[name]}</svg>`;
  }

  let openInstance = null; // at most one popover open at a time

  function closeOpen() {
    if (openInstance) openInstance.close();
  }

  // enhance wraps one <select> in the custom UI. Idempotent: a select this
  // has already run on (re-enhanced after schema.js rebuilds the form for
  // a new species, say) is rebuilt fresh rather than double-wrapped.
  function enhance(select, opts) {
    opts = opts || {};
    const existingWrap = select.closest('.combo');
    const host = existingWrap ? existingWrap.parentNode : select.parentNode;
    if (existingWrap) host.insertBefore(select, existingWrap);
    if (existingWrap) existingWrap.remove();

    const iconHTML = opts.iconHTML || ((option) => iconSVG(option.dataset.icon));
    const hasAnyIcon = Array.from(select.options).some((o) => iconHTML(o));

    const wrap = document.createElement('div');
    wrap.className = 'combo';
    host.insertBefore(wrap, select);
    wrap.appendChild(select);
    select.classList.add('combo-native');
    select.setAttribute('aria-hidden', 'true');
    select.tabIndex = -1;

    const listId = `${select.id || 'combo'}-listbox`;
    const trigger = document.createElement('button');
    trigger.type = 'button';
    trigger.className = 'combo-trigger';
    trigger.setAttribute('aria-haspopup', 'listbox');
    trigger.setAttribute('aria-expanded', 'false');
    trigger.setAttribute('aria-controls', listId);
    if (select.id) trigger.setAttribute('aria-labelledby', `${select.id}-label`);
    wrap.appendChild(trigger);

    const popover = document.createElement('div');
    popover.className = 'combo-popover';
    popover.hidden = true;
    popover.innerHTML =
      `<div class="combo-search-wrap"><input type="text" class="combo-search" placeholder="Search…" aria-label="Search options"></div>` +
      `<ul class="combo-list" role="listbox" id="${listId}"></ul>`;
    wrap.appendChild(popover);

    const search = popover.querySelector('.combo-search');
    const list = popover.querySelector('.combo-list');

    // Associate the field's own <label for="..."> with the trigger, since
    // the trigger -- not the now-hidden native select -- is what a screen
    // reader user actually operates. schema.js and admin.html already give
    // every select's label a matching id via this same select.id.
    if (select.id) {
      const label = document.getElementById(`${select.id}-label`) || document.querySelector(`label[for="${select.id}"]`);
      if (label && !label.id) label.id = `${select.id}-label`;
      if (label) trigger.setAttribute('aria-labelledby', label.id);
    }

    function optionsData() {
      return Array.from(select.options).map((o, i) => ({
        el: o,
        index: i,
        value: o.value,
        label: o.textContent,
        disabled: o.disabled,
        icon: iconHTML(o),
      }));
    }

    function renderTrigger() {
      const chosen = select.selectedIndex >= 0 ? select.options[select.selectedIndex] : null;
      const placeholder = !chosen || chosen.disabled || chosen.value === '';
      trigger.innerHTML =
        (hasAnyIcon ? `<span class="combo-trigger-icon">${chosen ? iconHTML(chosen) : ''}</span>` : '') +
        `<span class="combo-trigger-label${placeholder ? ' placeholder' : ''}">${chosen ? chosen.textContent : 'Choose…'}</span>` +
        `<svg class="combo-caret" viewBox="0 0 20 20" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M5.5 8 10 12.5 14.5 8"/></svg>`;
    }

    let activeIndex = -1;

    function renderList(filter) {
      const q = (filter || '').trim().toLowerCase();
      const opts = optionsData().filter((o) => o.value !== '' && (!q || o.label.toLowerCase().includes(q)));
      list.innerHTML = opts.length
        ? opts
            .map(
              (o) =>
                `<li role="option" id="${listId}-${o.index}" data-index="${o.index}" aria-selected="${o.value === select.value}">` +
                (hasAnyIcon ? `<span class="combo-option-icon${o.icon ? '' : ' blank'}">${o.icon}</span>` : '') +
                `<span>${o.label}</span></li>`
            )
            .join('')
        : `<li class="combo-empty">No matches</li>`;
      activeIndex = opts.length ? opts.findIndex((o) => o.value === select.value) : -1;
      if (activeIndex < 0 && opts.length) activeIndex = 0;
      markActive();
      return opts;
    }

    function markActive() {
      const items = list.querySelectorAll('li[role="option"]');
      items.forEach((li, i) => {
        const on = i === activeIndex;
        li.setAttribute('data-active', String(on));
        if (on) search.setAttribute('aria-activedescendant', li.id);
      });
      if (!items.length) search.removeAttribute('aria-activedescendant');
    }

    let currentOpts = [];

    function open() {
      closeOpen();
      wrap.setAttribute('data-open', 'true');
      trigger.setAttribute('aria-expanded', 'true');
      popover.hidden = false;
      search.value = '';
      currentOpts = renderList('');
      requestAnimationFrame(() => search.focus());
      openInstance = { close };
    }

    function close(returnFocus) {
      wrap.removeAttribute('data-open');
      trigger.setAttribute('aria-expanded', 'false');
      popover.hidden = true;
      if (openInstance && openInstance.close === close) openInstance = null;
      if (returnFocus) trigger.focus();
    }

    function choose(index) {
      const opt = select.options[index];
      if (!opt || opt.disabled) return;
      select.value = opt.value;
      renderTrigger();
      close(true);
      // A real change event, not a custom one: schema.js's read() and
      // every field-change listener in admin.js/redeem.js is already
      // listening for this on the native select and needs no changes.
      select.dispatchEvent(new Event('change', { bubbles: true }));
    }

    trigger.addEventListener('click', () => {
      if (wrap.getAttribute('data-open') === 'true') close(true);
      else open();
    });

    search.addEventListener('input', () => {
      currentOpts = renderList(search.value);
    });

    search.addEventListener('keydown', (e) => {
      if (e.key === 'ArrowDown') {
        e.preventDefault();
        if (currentOpts.length) {
          activeIndex = Math.min(activeIndex + 1, currentOpts.length - 1);
          markActive();
        }
      } else if (e.key === 'ArrowUp') {
        e.preventDefault();
        if (currentOpts.length) {
          activeIndex = Math.max(activeIndex - 1, 0);
          markActive();
        }
      } else if (e.key === 'Enter') {
        e.preventDefault();
        if (currentOpts[activeIndex]) choose(currentOpts[activeIndex].index);
      } else if (e.key === 'Escape') {
        e.preventDefault();
        close(true);
      } else if (e.key === 'Tab') {
        close(false);
      }
    });

    list.addEventListener('click', (e) => {
      const li = e.target.closest('li[role="option"]');
      if (li) choose(Number(li.dataset.index));
    });
    list.addEventListener('mousemove', (e) => {
      const li = e.target.closest('li[role="option"]');
      if (!li) return;
      const i = currentOpts.findIndex((o) => o.index === Number(li.dataset.index));
      if (i >= 0 && i !== activeIndex) {
        activeIndex = i;
        markActive();
      }
    });

    document.addEventListener('click', (e) => {
      if (wrap.getAttribute('data-open') === 'true' && !wrap.contains(e.target)) close(false);
    });

    // sync is what admin.js calls after setting select.value directly
    // (clearing the form between animals): a programmatic .value= does not
    // fire 'change', so nothing else here would otherwise notice.
    wrap._comboSync = renderTrigger;

    renderTrigger();
  }

  function enhanceAll(root, opts) {
    (root || document).querySelectorAll('select[data-combobox]').forEach((s) => enhance(s, opts));
  }

  function refresh(select) {
    const wrap = select.closest('.combo');
    if (wrap && wrap._comboSync) wrap._comboSync();
  }

  function refreshAll(root) {
    (root || document).querySelectorAll('select[data-combobox]').forEach(refresh);
  }

  document.addEventListener('keydown', (e) => {
    if (e.key === 'Escape') closeOpen();
  });

  window.Combobox = { enhance, enhanceAll, refresh, refreshAll };
})();
