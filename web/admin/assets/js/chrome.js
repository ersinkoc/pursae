/**
 * Admin chrome behaviour: menu collapse, admin bar dropdowns and the responsive
 * menu (<= 782px). The markup is rendered server-side; this only wires events,
 * like wp-admin/js/common.js does for these parts.
 */
(function () {
  'use strict';

  var mq = window.matchMedia('(max-width: 782px)');

  function setMenuToggleExpanded(open) {
    var a = document.querySelector('#wp-admin-bar-menu-toggle > a');
    if (a) a.setAttribute('aria-expanded', open ? 'true' : 'false');
  }

  function closeResponsiveMenu() {
    var wrap = document.getElementById('wpwrap');
    if (!wrap) return;
    wrap.classList.remove('wp-responsive-open');
    setMenuToggleExpanded(false);
  }

  function bindResponsive() {
    var wrap = document.getElementById('wpwrap');
    var menu = document.getElementById('adminmenu');
    var bar = document.getElementById('wpadminbar');
    if (!wrap) return;

    if (!document.getElementById('wp-responsive-overlay')) {
      var overlay = document.createElement('div');
      overlay.id = 'wp-responsive-overlay';
      overlay.addEventListener('click', closeResponsiveMenu);
      wrap.appendChild(overlay);
    }

    function syncMode() {
      if (mq.matches) {
        if (menu) menu.setAttribute('data-wp-responsive', '1');
        if (bar) bar.classList.add('mobile');
      } else {
        closeResponsiveMenu();
        if (menu) menu.removeAttribute('data-wp-responsive');
        if (bar) bar.classList.remove('mobile');
      }
    }
    syncMode();
    if (mq.addEventListener) mq.addEventListener('change', syncMode);

    var toggle = document.getElementById('wp-admin-bar-menu-toggle');
    if (toggle) {
      toggle.addEventListener('click', function (e) {
        e.preventDefault();
        if (!mq.matches) return;
        var open = !wrap.classList.contains('wp-responsive-open');
        wrap.classList.toggle('wp-responsive-open', open);
        setMenuToggleExpanded(open);
      });
    }

    if (menu) {
      menu.addEventListener('click', function (e) {
        if (!mq.matches || !menu.getAttribute('data-wp-responsive')) return;
        var a = e.target.closest('li.wp-has-submenu > a.menu-top');
        if (!a) return;
        var li = a.parentElement;
        if (li.classList.contains('selected') || li.classList.contains('wp-menu-open')) return;
        e.preventDefault();
        menu.querySelectorAll('li.wp-has-submenu').forEach(function (other) {
          if (other !== li) other.classList.remove('selected', 'wp-menu-open', 'opensub');
        });
        li.classList.add('selected', 'wp-menu-open', 'opensub');
      });
    }
  }

  function bindChrome() {
    var collapse = document.getElementById('collapse-button');
    if (collapse) {
      var folded = false;
      try { folded = localStorage.getItem('wp-admin-folded') === '1'; } catch (e) {}
      if (folded && !mq.matches) {
        document.body.classList.add('folded');
        collapse.setAttribute('aria-expanded', 'false');
      }
      collapse.addEventListener('click', function () {
        if (mq.matches) return;
        document.body.classList.toggle('folded');
        var isFolded = document.body.classList.contains('folded');
        try { localStorage.setItem('wp-admin-folded', isFolded ? '1' : '0'); } catch (e) {}
        collapse.setAttribute('aria-expanded', isFolded ? 'false' : 'true');
      });
    }

    // Flyout submenus for non-current top-level items (hover on desktop).
    document.querySelectorAll('#adminmenu li.menu-top').forEach(function (li) {
      li.addEventListener('mouseenter', function () { if (!mq.matches) li.classList.add('opensub'); });
      li.addEventListener('mouseleave', function () { if (!mq.matches) li.classList.remove('opensub'); });
    });

    document.querySelectorAll('#wpadminbar .menupop').forEach(function (li) {
      li.addEventListener('mouseenter', function () { if (!mq.matches) li.classList.add('hover'); });
      li.addEventListener('mouseleave', function () { li.classList.remove('hover'); });
      li.addEventListener('click', function (e) {
        if (!mq.matches || li.id === 'wp-admin-bar-menu-toggle') return;
        var item = e.target.closest('.ab-item');
        if (!item || item.closest('.ab-sub-wrapper')) return;
        e.preventDefault();
        var was = li.classList.contains('hover');
        document.querySelectorAll('#wpadminbar .menupop.hover').forEach(function (o) { o.classList.remove('hover'); });
        if (!was) li.classList.add('hover');
      });
    });

    bindResponsive();
  }

  if (document.readyState === 'loading') document.addEventListener('DOMContentLoaded', bindChrome);
  else bindChrome();
})();
