(function () {
  'use strict';

  function ready(fn) {
    if (document.readyState !== 'loading') fn();
    else document.addEventListener('DOMContentLoaded', fn);
  }

  /**
   * Rewrite legacy « ‹ 1 / 1 › » pagination into WP tablenav-pages markup
   * so list-tables.css applies (siblings should still ship correct HTML long-term).
   */
  function normalizeTablenav() {
    document.querySelectorAll('.tablenav-pages').forEach(function (pages) {
      if (pages.querySelector('.paging-input, .tablenav-pages-navspan')) return;
      var links = pages.querySelector('.pagination-links');
      if (!links) return;
      var num = pages.querySelector('.displaying-num');
      var numHtml = num ? num.outerHTML : '';
      var totalMatch = (links.textContent || '').match(/\/\s*(\d+)/);
      var total = totalMatch ? totalMatch[1] : '1';
      var uid = 'current-page-selector-' + Math.random().toString(36).slice(2, 8);
      pages.classList.add('one-page');
      pages.innerHTML =
        numHtml +
        '<span class="pagination-links">' +
        '<span class="tablenav-pages-navspan button disabled" aria-hidden="true">&laquo;</span>' +
        '<span class="tablenav-pages-navspan button disabled" aria-hidden="true">&lsaquo;</span>' +
        '<span class="paging-input">' +
        '<label for="' + uid + '" class="screen-reader-text">Current Page</label>' +
        '<input class="current-page" id="' + uid + '" type="text" name="paged" value="1" size="1" aria-describedby="table-paging" />' +
        '<span class="tablenav-paging-text"> / <span class="total-pages">' + total + '</span></span>' +
        '</span>' +
        '<span class="tablenav-pages-navspan button disabled" aria-hidden="true">&rsaquo;</span>' +
        '<span class="tablenav-pages-navspan button disabled" aria-hidden="true">&raquo;</span>' +
        '</span>';
    });
  }

  function bindUi() {
    normalizeTablenav();

    document.querySelectorAll('.notice-dismiss').forEach(function (btn) {
      if (btn._bound) return;
      btn._bound = true;
      btn.addEventListener('click', function () {
        var notice = btn.closest('.notice');
        if (notice) notice.remove();
      });
    });

    document.querySelectorAll('[data-toggle-screen]').forEach(function (btn) {
      if (btn._bound) return;
      btn._bound = true;
      btn.addEventListener('click', function () {
        var id = btn.getAttribute('data-toggle-screen');
        var panel = document.getElementById(id);
        if (!panel) return;
        var wasOpen = !panel.classList.contains('hidden');
        document.querySelectorAll('#screen-meta [id$="-wrap"]').forEach(function (p) {
          p.classList.add('hidden');
        });
        document.querySelectorAll('[data-toggle-screen]').forEach(function (b) {
          b.setAttribute('aria-expanded', 'false');
        });
        if (!wasOpen) {
          panel.classList.remove('hidden');
          btn.setAttribute('aria-expanded', 'true');
        }
      });
    });

    document.querySelectorAll('.nav-tab-wrapper').forEach(function (wrap) {
      if (wrap._bound) return;
      wrap._bound = true;
      wrap.addEventListener('click', function (e) {
        var tab = e.target.closest('.nav-tab');
        if (!tab || !tab.dataset.tab) return;
        e.preventDefault();
        wrap.querySelectorAll('.nav-tab').forEach(function (t) {
          t.classList.toggle('nav-tab-active', t === tab);
        });
        var group = tab.dataset.tabGroup || 'default';
        document.querySelectorAll('[data-tab-panel][data-tab-group="' + group + '"]').forEach(function (p) {
          p.classList.toggle('hidden', p.dataset.tabPanel !== tab.dataset.tab);
        });
      });
    });

    document.querySelectorAll('.wp-list-table thead .check-column input, .wp-list-table tfoot .check-column input').forEach(function (master) {
      if (master._bound) return;
      master._bound = true;
      master.addEventListener('change', function () {
        var table = master.closest('table');
        table.querySelectorAll('tbody .check-column input').forEach(function (cb) {
          cb.checked = master.checked;
        });
      });
    });
  }

  ready(bindUi);
  document.addEventListener('wp-admin-chrome-ready', bindUi);
})();
