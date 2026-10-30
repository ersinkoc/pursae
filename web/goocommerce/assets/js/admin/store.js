/* Goocommerce store admin behaviour (no dependencies): order preview modal, order editor toggles
   (address editing, item editing, refunds, inline modals, Load / copy addresses), coupon data tabs and
   code generator, settings helpers (show_if_checked, select all, tax rows, row reordering, toggles),
   confirmations and copy buttons. Every screen works without it: forms post normally. */
(function () {
	'use strict';
	var $ = function (sel, root) { return (root || document).querySelector(sel); };
	var $$ = function (sel, root) { return Array.prototype.slice.call((root || document).querySelectorAll(sel)); };
	var on = function (sel, ev, fn) {
		document.addEventListener(ev, function (e) {
			var t = e.target.closest && e.target.closest(sel);
			if (t) { fn.call(t, e, t); }
		});
	};

	// Confirmations (data-confirm on buttons and links).
	on('[data-confirm]', 'click', function (e, el) {
		if (!window.confirm(el.getAttribute('data-confirm'))) { e.preventDefault(); e.stopPropagation(); }
	});

	// Orders list: preview modal.
	on('.order-preview', 'click', function (e, el) {
		e.preventDefault();
		var tpl = document.getElementById('goo-order-preview-' + el.getAttribute('data-order-id'));
		var modal = document.getElementById('goo-order-preview-modal');
		if (!tpl || !modal) { return; }
		var art = $('article', modal);
		art.innerHTML = '';
		art.appendChild(tpl.content.cloneNode(true));
		var h = $('h1', art);
		$('header h1', modal).textContent = h ? h.textContent : '';
		modal.hidden = false;
	});
	on('.goo-modal .modal-close', 'click', function (e, el) {
		e.preventDefault();
		el.closest('.goo-modal').hidden = true;
	});
	document.addEventListener('keydown', function (e) {
		if (e.key === 'Escape') { $$('.goo-modal').forEach(function (m) { m.hidden = true; }); }
	});

	// Order editor: addresses.
	on('a.edit_address', 'click', function (e, el) {
		e.preventDefault();
		var col = el.closest('.order_data_column');
		col.classList.toggle('editing');
		$$('.load_customer_billing, .load_customer_shipping', col).forEach(function (a) { a.style.display = col.classList.contains('editing') ? '' : 'none'; });
	});
	function loadAddress(type, el) {
		var user = $('#customer_user');
		if (!user || !user.value) { window.alert('No customer selected'); return; }
		if (!window.confirm('Load the customer\'s ' + type + ' information? This will remove any currently entered ' + type + ' information.')) { return; }
		fetch('/wp-admin/goocommerce?page=orders&action=customer_details&user_id=' + encodeURIComponent(user.value), { credentials: 'same-origin' })
			.then(function (r) { return r.json(); })
			.then(function (data) {
				var addr = data[type] || {};
				$$('[name^="_' + type + '_"]').forEach(function (input) {
					var key = input.name.replace('_' + type + '_', '');
					input.value = addr[key] || '';
				});
			});
	}
	on('.load_customer_billing', 'click', function (e, el) { e.preventDefault(); loadAddress('billing', el); });
	on('.load_customer_shipping', 'click', function (e, el) { e.preventDefault(); loadAddress('shipping', el); });
	on('.billing-same-as-shipping', 'click', function (e, el) {
		e.preventDefault();
		if (!window.confirm('Copy billing information to shipping information? This will remove any currently entered shipping information.')) { return; }
		$$('[name^="_billing_"]').forEach(function (input) {
			var target = document.getElementsByName(input.name.replace('_billing_', '_shipping_'))[0];
			if (target) { target.value = input.value; }
		});
		var col = el.closest('.order_data_column');
		col.classList.add('editing');
	});

	// Select filters (customer / product / region pickers).
	document.addEventListener('input', function (e) {
		var f = e.target;
		if (!f.classList || !f.classList.contains('goo-select-filter')) { return; }
		var sel = document.getElementById(f.getAttribute('data-target'));
		if (!sel) { return; }
		var q = f.value.toLowerCase();
		$$('option', sel).forEach(function (o) {
			if (!o.value) { return; }
			o.hidden = q !== '' && o.textContent.toLowerCase().indexOf(q) === -1 && !o.selected;
		});
	});

	// Order editor: item rows.
	var items = $('#woocommerce-order-items');
	function showRow(cls) {
		$$('.wc-order-data-row-toggle', items).forEach(function (r) { r.style.display = r.classList.contains(cls) ? '' : 'none'; });
	}
	on('#woocommerce-order-items .add-line-item', 'click', function () { showRow('wc-order-add-item'); });
	on('#woocommerce-order-items .cancel-action', 'click', function () {
		showRow('wc-order-bulk-actions');
		$$('div.refund', items).forEach(function (d) { d.style.display = 'none'; });
		$$('.goo-inline-modal', items).forEach(function (m) { m.hidden = true; });
	});
	on('#woocommerce-order-items .edit-order-item', 'click', function (e, el) {
		e.preventDefault();
		var tr = el.closest('tr');
		$$('div.view', tr).forEach(function (d) { d.style.display = 'none'; });
		$$('div.edit', tr).forEach(function (d) { d.style.display = ''; });
		showRow('wc-order-add-item');
	});
	on('#woocommerce-order-items .refund-items', 'click', function () {
		showRow('wc-order-refund-items');
		$$('div.refund', items).forEach(function (d) { d.style.display = ''; });
	});
	on('[data-modal]', 'click', function (e, el) {
		var m = document.getElementById(el.getAttribute('data-modal'));
		if (m) { $$('.goo-inline-modal').forEach(function (x) { x.hidden = x !== m; }); m.hidden = false; var f = $('input,select', m); if (f) { f.focus(); } }
	});
	on('.goo-inline-modal .modal-close', 'click', function (e, el) { el.closest('.goo-inline-modal').hidden = true; });

	// Refund amounts: line totals from quantities, then the refund amount.
	function money(v) { v = parseFloat(String(v || '').replace(',', '.')); return isNaN(v) ? 0 : v; }
	function sumRefund() {
		var total = 0;
		$$('.refund_line_total, .refund_line_tax', items).forEach(function (i) { total += money(i.value); });
		var amount = $('#refund_amount');
		if (amount) { amount.value = total ? total.toFixed(2) : ''; }
		$$('.wc-order-refund-amount .amount').forEach(function (s) { s.textContent = (amount && money(amount.value) || 0).toFixed(2); });
	}
	document.addEventListener('change', function (e) {
		var t = e.target;
		if (!items || !items.contains(t)) { return; }
		if (t.classList.contains('refund_order_item_qty')) {
			var tr = t.closest('tr');
			var qtyInput = $('input.quantity', tr);
			var qty = qtyInput ? money(qtyInput.getAttribute('data-qty')) : 0;
			var n = money(t.value);
			var line = $('input.line_total', tr);
			if (qty > 0 && line) { $('.refund_line_total', tr).value = (money(line.value) / qty * n).toFixed(2); }
			$$('input.line_tax', tr).forEach(function (tax) {
				var td = tax.closest('td');
				var r = $('.refund_line_tax', td);
				if (r && qty > 0) { r.value = (money(tax.value) / qty * n).toFixed(2); }
			});
			sumRefund();
		}
		if (t.classList.contains('refund_line_total') || t.classList.contains('refund_line_tax')) { sumRefund(); }
		if (t.id === 'refund_amount') {
			$$('.wc-order-refund-amount .amount').forEach(function (s) { s.textContent = money(t.value).toFixed(2); });
		}
	});
	on('.do-api-refund, .do-manual-refund', 'click', function (e) {
		var amount = $('#refund_amount');
		if (!window.confirm('Are you sure you wish to process this refund? This action cannot be undone.')) { e.preventDefault(); return; }
		if (amount && !amount.value) { sumRefund(); }
	});

	// Coupon editor: tabs and code generator.
	on('.coupon_data_tabs a', 'click', function (e, el) {
		e.preventDefault();
		var box = el.closest('#coupon_options');
		$$('.coupon_data_tabs li', box).forEach(function (li) { li.classList.remove('active'); });
		el.parentNode.classList.add('active');
		$$('.panel', box).forEach(function (p) { p.hidden = '#' + p.id !== el.getAttribute('href'); });
	});
	on('#generate-coupon-code', 'click', function (e) {
		e.preventDefault();
		var chars = 'ABCDEFGHJKMNPQRSTUVWXYZ23456789', code = '';
		var buf = new Uint32Array(8);
		window.crypto.getRandomValues(buf);
		for (var i = 0; i < 8; i++) { code += chars[buf[i] % chars.length]; }
		var t = $('#title');
		if (t) { t.value = code.toLowerCase(); }
	});

	// Settings: show_if_checked dependents.
	function applyShowIf() {
		$$('.goo-show-if').forEach(function (el) {
			var dep = document.getElementsByName(el.getAttribute('data-show-if'))[0];
			if (dep) { el.style.display = dep.checked ? '' : 'none'; }
		});
	}
	document.addEventListener('change', function (e) { if (e.target.type === 'checkbox') { applyShowIf(); } });
	applyShowIf();
	on('a.select_all', 'click', function (e, el) {
		e.preventDefault();
		$$('option', el.closest('td').querySelector('select')).forEach(function (o) { o.selected = true; });
	});
	on('a.select_none', 'click', function (e, el) {
		e.preventDefault();
		$$('option', el.closest('td').querySelector('select')).forEach(function (o) { o.selected = false; });
	});
	// Toggles mirror their checkbox.
	document.addEventListener('change', function (e) {
		var t = e.target;
		if (!t.closest || !t.closest('.goo-toggle')) { return; }
		var span = t.parentNode.querySelector('.woocommerce-input-toggle');
		span.classList.toggle('woocommerce-input-toggle--enabled', t.checked);
		span.classList.toggle('woocommerce-input-toggle--disabled', !t.checked);
		span.textContent = t.checked ? 'Yes' : 'No';
	});
	// Zone postcodes.
	on('.wc-shipping-zone-postcodes-toggle', 'change', function (e, el) {
		var box = $('.wc-shipping-zone-postcodes');
		if (box) { box.hidden = !el.checked; }
	});
	// Tax rates: insert a row.
	var newRows = 1;
	on('.wc_tax_rates .insert', 'click', function (e) {
		e.preventDefault();
		var tpl = $('.wc_tax_rates tr.goo-new-row');
		if (!tpl) { return; }
		var row = tpl.cloneNode(true);
		var id = 'new-' + (newRows++);
		$$('input', row).forEach(function (i) {
			i.name = i.name.replace(/\[new-\d+\]/, '[' + id + ']');
			if (i.type === 'text') { i.value = ''; }
			if (i.classList.contains('goo-order')) { i.value = String(9999 + newRows); }
		});
		row.setAttribute('data-id', id);
		tpl.parentNode.appendChild(row);
		var first = $('input[type=text]', row);
		if (first) { first.focus(); }
	});
	// Payments: reorder rows with the arrows.
	on('.wc_gateways .wc-move-up, .wc_gateways .wc-move-down', 'click', function (e, el) {
		var tr = el.closest('tr');
		if (el.classList.contains('wc-move-up') && tr.previousElementSibling) { tr.parentNode.insertBefore(tr, tr.previousElementSibling); }
		if (el.classList.contains('wc-move-down') && tr.nextElementSibling) { tr.parentNode.insertBefore(tr.nextElementSibling, tr); }
	});
	// Drag ordering of rows that carry a hidden .goo-order input (zones, methods, tax rates).
	$$('tbody').forEach(function (tbody) {
		var rows = $$('tr', tbody).filter(function (r) { return $('.goo-order', r); });
		if (rows.length < 2) { return; }
		var dragging = null;
		rows.forEach(function (r) {
			r.draggable = true;
			r.addEventListener('dragstart', function () { dragging = r; r.style.opacity = '.5'; });
			r.addEventListener('dragend', function () {
				r.style.opacity = '';
				dragging = null;
				$$('tr', tbody).forEach(function (row, i) { var o = $('.goo-order', row); if (o && !row.classList.contains('goo-new-row')) { o.value = i; } });
			});
			r.addEventListener('dragover', function (e) {
				e.preventDefault();
				if (dragging && dragging !== r) {
					var rect = r.getBoundingClientRect();
					tbody.insertBefore(dragging, e.clientY < rect.top + rect.height / 2 ? r : r.nextSibling);
				}
			});
		});
	});
	// BACS accounts.
	on('.wc_input_table .add', 'click', function (e, el) {
		e.preventDefault();
		var tbody = $('tbody.accounts', el.closest('table'));
		var last = tbody.lastElementChild;
		if (!last) { return; }
		var row = last.cloneNode(true);
		$$('input', row).forEach(function (i) { i.value = ''; });
		tbody.appendChild(row);
	});
	on('.wc_input_table .remove_rows', 'click', function (e, el) {
		e.preventDefault();
		var focused = $('tbody.accounts tr.current', el.closest('table'));
		if (focused) { focused.parentNode.removeChild(focused); }
	});
	document.addEventListener('focusin', function (e) {
		var tr = e.target.closest && e.target.closest('tbody.accounts tr');
		if (!tr) { return; }
		$$('tr', tr.parentNode).forEach(function (r) { r.classList.toggle('current', r === tr); });
	});

	// Copy buttons and the system report.
	on('.copy-key, .copy-secret', 'click', function (e, el) {
		var input = document.getElementById(el.getAttribute('data-target'));
		if (!input) { return; }
		input.select();
		if (navigator.clipboard) { navigator.clipboard.writeText(input.value); } else { document.execCommand('copy'); }
		el.textContent = 'Copied!';
	});
	on('.debug-report', 'click', function (e) {
		e.preventDefault();
		var box = $('#debug-report');
		if (box) { box.hidden = !box.hidden; }
	});
})();
