/*
 * Pursae admin actions without inline event handlers.
 *
 * A Content-Security-Policy with a script nonce blocks onclick="…" and javascript: URLs, so the
 * admin markup declares behaviour with data-* attributes and this file (loaded on every admin page,
 * listening on the document, so markup added later by wp.template / innerHTML works too) carries it
 * out:
 *
 *   data-confirm="Message"           on a link, button or input: cancel the click unless confirmed;
 *                                    on a form: cancel the submit unless confirmed
 *   data-confirm-if-action="value"   (form) ask only when the bulk action select has this value
 *   data-select-on-focus             select the field's text on focus and click
 *   data-action="remove"             remove the closest data-target (a selector) and cancel the click
 *   data-action="add-row"            insert a copy of <template id=data-template> (or the template
 *                                    named by the value of the select data-template-select) at the
 *                                    end of data-into (a selector); "{i}" in it becomes a counter
 *   data-check-all="selector"        (checkbox) tick or untick every checkbox matching the selector
 *   data-action="print"              window.print()
 *   data-no-submit                   (form) never submit
 *   data-autosubmit                  (select/input) submit its form on change
 *   data-navigate-on-change          (select) go to the selected option's value
 *   data-live-output="suffix"        (input) show its value + suffix in the next element
 *   data-action="generate-password"  fill data-target (a selector) with a random password
 *   data-copy-from="selector"        copy that element's text or value to the clipboard
 *   data-imgedit-<event>="method"    call imageEdit.method(…args) for the WP image editor markup;
 *                                    args from data-imgedit-<event>-args, else data-imgedit-args;
 *                                    only imageEdit's own methods can be named
 */
(function () {
  'use strict';
  if (window.wpgoCSPActions) { return; }
  window.wpgoCSPActions = true;
  var doc = document;

  function closest(el, sel) {
    return el && el.nodeType === 1 && el.closest ? el.closest(sel) : null;
  }

  // Confirmations: clicks (links, buttons, inputs) and form submits.
  doc.addEventListener('click', function (e) {
    var el = closest(e.target, '[data-confirm]');
    if (!el || el.tagName === 'FORM') { return; }
    if (!window.confirm(el.getAttribute('data-confirm'))) {
      e.preventDefault();
      e.stopImmediatePropagation();
    }
  }, true);
  doc.addEventListener('submit', function (e) {
    var form = e.target;
    if (!form || form.tagName !== 'FORM') { return; }
    if (form.hasAttribute('data-no-submit')) {
      e.preventDefault();
      return;
    }
    if (!form.hasAttribute('data-confirm')) { return; }
    var only = form.getAttribute('data-confirm-if-action');
    if (only) {
      var sel = form.querySelectorAll('select[name="action"], select[name="action2"]');
      var hit = false;
      for (var i = 0; i < sel.length; i++) { if (sel[i].value === only) { hit = true; } }
      if (!hit) { return; }
    }
    if (!window.confirm(form.getAttribute('data-confirm'))) {
      e.preventDefault();
      e.stopImmediatePropagation();
    }
  }, true);

  // Select a field's text.
  function selectText(e) {
    var el = closest(e.target, '[data-select-on-focus]');
    if (el && el.select) { el.select(); }
  }
  doc.addEventListener('focusin', selectText);
  doc.addEventListener('click', selectText);

  var rowCounter = 0;
  doc.addEventListener('click', function (e) {
    var el = closest(e.target, '[data-action]');
    if (!el) { return; }
    var action = el.getAttribute('data-action');
    var target;
    switch (action) {
      case 'remove':
        e.preventDefault();
        target = closest(el, el.getAttribute('data-target') || 'tr');
        if (target) { target.parentNode.removeChild(target); }
        return;
      case 'add-row': {
        e.preventDefault();
        var id = el.getAttribute('data-template');
        var pick = el.getAttribute('data-template-select');
        if (pick) {
          var s = doc.querySelector(pick);
          if (s) { id = (el.getAttribute('data-template-prefix') || '') + s.value; }
        }
        var tpl = id && doc.getElementById(id);
        var into = doc.querySelector(el.getAttribute('data-into'));
        if (!tpl || !into) { return; }
        rowCounter++;
        var html = tpl.innerHTML.split('{i}').join(String(Date.now()) + rowCounter);
        into.insertAdjacentHTML('beforeend', html);
        return;
      }
      case 'print':
        e.preventDefault();
        window.print();
        return;
      case 'generate-password': {
        e.preventDefault();
        target = doc.querySelector(el.getAttribute('data-target'));
        if (!target) { return; }
        var chars = 'abcdefghijkmnopqrstuvwxyzABCDEFGHJKLMNPQRSTUVWXYZ23456789!@#$%^&*()';
        var buf = new Uint32Array(24);
        window.crypto.getRandomValues(buf);
        var pw = '';
        for (var j = 0; j < buf.length; j++) { pw += chars.charAt(buf[j] % chars.length); }
        target.value = pw;
        target.dispatchEvent(new Event('input', { bubbles: true }));
        return;
      }
    }
  });

  doc.addEventListener('click', function (e) {
    var el = closest(e.target, '[data-copy-from]');
    if (!el) { return; }
    e.preventDefault();
    var src = doc.querySelector(el.getAttribute('data-copy-from'));
    if (!src) { return; }
    var text = 'value' in src && src.tagName !== 'DIV' ? src.value : src.textContent;
    if (navigator.clipboard) { navigator.clipboard.writeText(text); }
  });

  // Check-all boxes.
  doc.addEventListener('change', function (e) {
    var el = closest(e.target, '[data-check-all]');
    if (el) {
      var boxes = doc.querySelectorAll(el.getAttribute('data-check-all'));
      for (var i = 0; i < boxes.length; i++) {
        if (boxes[i] !== el && !boxes[i].disabled) { boxes[i].checked = el.checked; }
      }
    }
    var sub = closest(e.target, '[data-autosubmit]');
    if (sub && sub.form) {
      if (sub.form.requestSubmit) { sub.form.requestSubmit(); } else { sub.form.submit(); }
    }
    var nav = closest(e.target, '[data-navigate-on-change]');
    if (nav && nav.value) { window.location.href = nav.value; }
  });

  // Range inputs with a live value next to them.
  function liveOutput(el) {
    var out = el.nextElementSibling;
    if (out) { out.textContent = el.value + (el.getAttribute('data-live-output') || ''); }
  }
  doc.addEventListener('input', function (e) {
    var el = closest(e.target, '[data-live-output]');
    if (el) { liveOutput(el); }
  });

  // The WP image editor (imgedit.html) calls imageEdit's methods. Only a method of imageEdit can be
  // named, with JSON arguments; "$this" stands for the element and "$event" for the event.
  function imgedit(e) {
    var attr = 'data-imgedit-' + e.type;
    var el = closest(e.target, '[' + attr + ']');
    if (!el || !window.imageEdit) { return; }
    var method = el.getAttribute(attr);
    if (!/^[A-Za-z]+$/.test(method) || typeof window.imageEdit[method] !== 'function' ||
        !Object.prototype.hasOwnProperty.call(window.imageEdit, method)) { return; }
    var args = [];
    var raw = el.getAttribute(attr + '-args');
    if (raw === null) { raw = el.getAttribute('data-imgedit-args'); }
    try { args = JSON.parse(raw || '[]'); } catch (err) { return; }
    if (!Array.isArray(args)) { return; }
    args = args.map(function (a) { return a === '$this' ? el : a === '$event' ? e : a; });
    var r = window.imageEdit[method].apply(window.imageEdit, args);
    if (r === false || el.hasAttribute('data-imgedit-prevent')) { e.preventDefault(); }
  }
  ['click', 'keyup', 'keydown', 'change', 'input'].forEach(function (t) { doc.addEventListener(t, imgedit); });
  doc.addEventListener('focusout', function (e) { imgedit({ type: 'blur', target: e.target, preventDefault: function () {} }); });
  doc.addEventListener('load', function (e) { imgedit({ type: 'load', target: e.target, preventDefault: function () {} }); }, true);
})();
