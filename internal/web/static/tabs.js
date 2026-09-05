// A plain ARIA tabs pattern: click a [role=tab], show the [role=tabpanel] it
// names via aria-controls, hide the rest. One tabbar per page is assumed;
// if a page ever needs two, this can take a container argument instead of
// querying the whole document.
(function () {
  'use strict';

  function select(tab) {
    const tabs = document.querySelectorAll('.tabbar [role="tab"]');
    tabs.forEach((t) => {
      const on = t === tab;
      t.setAttribute('aria-selected', String(on));
      const panel = document.getElementById(t.getAttribute('aria-controls'));
      if (panel) panel.hidden = !on;
    });
  }

  document.addEventListener('DOMContentLoaded', () => {
    document.querySelectorAll('.tabbar [role="tab"]').forEach((tab) => {
      tab.addEventListener('click', () => select(tab));
    });
  });
})();
