/* Goocommerce country select (country-select.js): swaps the state field between a select and a text
   input when the country changes (states from goo_country_select_params.countries). */
(function () {
	'use strict';
	var cfg = window.goo_country_select_params || { countries: {} };

	function stateFieldFor(country) {
		var form = country.closest('form') || document;
		var rel = country.getAttribute('rel');
		if (rel) {
			return form.querySelector('#' + rel);
		}
		var id = country.id.replace(/country$/, 'state');
		return form.querySelector('#' + id);
	}

	function swap(country) {
		var field = stateFieldFor(country);
		if (!field) {
			return;
		}
		var states = cfg.countries[country.value];
		var name = field.name;
		var id = field.id;
		var value = field.value;
		var row = field.closest('.form-row');
		var label = row ? row.querySelector('label') : null;
		if (states && Object.keys(states).length > 0) {
			var sel = document.createElement('select');
			sel.name = name;
			sel.id = id;
			sel.className = 'state_select';
			sel.setAttribute('autocomplete', 'address-level1');
			sel.setAttribute('data-placeholder', cfg.i18n_select_state_text || 'Select an option…');
			var empty = document.createElement('option');
			empty.value = '';
			empty.textContent = cfg.i18n_select_state_text || 'Select an option…';
			sel.appendChild(empty);
			Object.keys(states).forEach(function (code) {
				var o = document.createElement('option');
				o.value = code;
				o.textContent = states[code];
				if (code === value) {
					o.selected = true;
				}
				sel.appendChild(o);
			});
			field.replaceWith(sel);
			if (row) {
				row.style.display = '';
				row.classList.add('validate-required');
			}
			setRequired(label, true);
		} else if (states && Object.keys(states).length === 0) {
			// A country without states: the field goes away.
			var hidden = document.createElement('input');
			hidden.type = 'hidden';
			hidden.name = name;
			hidden.id = id;
			hidden.className = 'hidden';
			field.replaceWith(hidden);
			if (row) {
				row.style.display = 'none';
			}
		} else {
			if (field.tagName === 'INPUT' && field.type === 'text') {
				return;
			}
			var input = document.createElement('input');
			input.type = 'text';
			input.name = name;
			input.id = id;
			input.className = 'input-text';
			input.setAttribute('autocomplete', 'address-level1');
			field.replaceWith(input);
			if (row) {
				row.style.display = '';
				row.classList.remove('validate-required');
			}
			setRequired(label, false);
		}
		country.dispatchEvent(new CustomEvent('country_to_state_changed', { bubbles: true, detail: { country: country.value } }));
	}

	function setRequired(label, on) {
		if (!label) {
			return;
		}
		var req = label.querySelector('.required');
		var opt = label.querySelector('.optional');
		if (on && !req) {
			if (opt) {
				opt.remove();
			}
			label.insertAdjacentHTML('beforeend', '&nbsp;<abbr class="required" title="required">*</abbr>');
		} else if (!on && req) {
			req.remove();
			if (!opt) {
				label.insertAdjacentHTML('beforeend', '&nbsp;<span class="optional">(optional)</span>');
			}
		}
	}

	document.addEventListener('change', function (e) {
		if (e.target.matches && e.target.matches('select.country_to_state, input.country_to_state')) {
			swap(e.target);
		}
	});
})();
