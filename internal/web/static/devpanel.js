// Dev-mode mock content trigger.
//
// Renders a floating "Dev" button, bottom right, only when this deployment
// is either running the offline WILDTAG_NETWORK=test (today's local dev
// setup) or the page was loaded with ?dev=1. Either check happening at
// startup, not something a user can flip from inside the page, is what keeps
// this from ever showing up in front of a real finder on a live network by
// accident.
//
// Each page registers its own scenarios on window.DevMocks[pageName] --
// plain functions that push fake data straight into that page's own render
// functions, bypassing the network entirely. This file only knows how to
// list and invoke them; it has no idea what a "tag" or a "batch" is.
(function () {
  'use strict';

  function forcedOn() {
    try {
      return new URLSearchParams(location.search).get('dev') === '1';
    } catch (_) {
      return false;
    }
  }

  async function testNetwork() {
    try {
      const res = await fetch('/api/info');
      if (!res.ok) return false;
      const info = await res.json();
      return info.network === 'test';
    } catch (_) {
      return false;
    }
  }

  function build(scenarios) {
    const names = Object.keys(scenarios);

    const btn = document.createElement('button');
    btn.type = 'button';
    btn.className = 'dev-fab';
    btn.setAttribute('aria-haspopup', 'true');
    btn.setAttribute('aria-expanded', 'false');
    btn.setAttribute('aria-label', 'Developer mock content');
    btn.textContent = 'Dev';

    const panel = document.createElement('div');
    panel.className = 'dev-panel';
    panel.setAttribute('role', 'menu');
    panel.hidden = true;

    const head = document.createElement('div');
    head.className = 'dev-panel-head';
    head.innerHTML = '<span>Mock content</span><span>dev only, network=test</span>';
    panel.appendChild(head);

    const list = document.createElement('div');
    list.className = 'dev-panel-list';
    panel.appendChild(list);

    if (!names.length) {
      const empty = document.createElement('p');
      empty.className = 'dev-panel-empty';
      empty.textContent = 'Nothing on this page has mock scenarios yet.';
      list.appendChild(empty);
    }

    for (const name of names) {
      const item = document.createElement('button');
      item.type = 'button';
      item.className = 'dev-panel-item';
      item.setAttribute('role', 'menuitem');
      item.textContent = name;
      item.addEventListener('click', () => {
        try {
          scenarios[name]();
        } catch (e) {
          // A broken mock shouldn't take the debug tool down with it.
          console.error('devpanel: scenario failed', name, e);
        }
        closePanel();
      });
      list.appendChild(item);
    }

    function openPanel() {
      panel.hidden = false;
      // Toggling `hidden` and the transition class in the same frame skips
      // the transition; give the browser one paint to register the start
      // state first.
      requestAnimationFrame(() => panel.classList.add('open'));
      btn.setAttribute('aria-expanded', 'true');
    }
    function closePanel() {
      panel.classList.remove('open');
      btn.setAttribute('aria-expanded', 'false');
      window.setTimeout(() => { panel.hidden = true; }, 250);
    }

    btn.addEventListener('click', () => (panel.hidden ? openPanel() : closePanel()));
    document.addEventListener('keydown', (e) => {
      if (e.key === 'Escape' && !panel.hidden) closePanel();
    });
    document.addEventListener('click', (e) => {
      if (!panel.hidden && !panel.contains(e.target) && e.target !== btn) closePanel();
    });

    document.body.appendChild(panel);
    document.body.appendChild(btn);
  }

  // Agentation (agentation.com) is a React devtool overlay for annotating a
  // running app; DESIGN.md's scaffolding section asks for it on every
  // project "so the running app is inspectable and steerable by agents
  // during development." This app has no React anywhere a browser DOM
  // overlay could mount into -- no bundler, no build step, on purpose (see
  // the README) -- so there's nothing to `npm install` it into. Loading
  // React and Agentation as ES modules straight from a CDN at runtime is the
  // one way to get the toolbar without adding a build step to the rest of
  // the app. It's gated behind the exact same dev-mode check as the mock
  // panel above, so it never loads for a real finder on a live deployment,
  // and a failure (offline, CDN down) is swallowed rather than breaking the
  // page -- this is a nicety for whoever is coding against this app, not
  // something the report flow depends on.
  async function mountAgentation() {
    try {
      const [{ default: React }, ReactDOMClient, { Agentation }] = await Promise.all([
        import('https://esm.sh/react@18'),
        import('https://esm.sh/react-dom@18/client'),
        import('https://esm.sh/agentation@3?deps=react@18,react-dom@18'),
      ]);
      const host = document.createElement('div');
      host.id = 'agentation-root';
      document.body.appendChild(host);
      ReactDOMClient.createRoot(host).render(React.createElement(Agentation));
    } catch (e) {
      console.warn('devpanel: Agentation did not load (dev-only overlay; safe to ignore, e.g. offline)', e);
    }
  }

  async function boot() {
    const enabled = forcedOn() || (await testNetwork());
    if (!enabled) return;

    const page = document.body.dataset.page;
    const scenarios = (window.DevMocks && window.DevMocks[page]) || {};
    build(scenarios);
    mountAgentation();
  }

  document.addEventListener('DOMContentLoaded', boot);
})();
