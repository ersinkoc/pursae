/* Goocommerce variation forms (add-to-cart-variation.js): matches the chosen attributes against
   data-product_variations (or asks ?goo-ajax=get_variation for large products), shows the variation's
   price, stock and description, sets variation_id, swaps the image and the SKU, and greys out the
   values no variation offers. */
(function () {
	'use strict';
	var params = window.goocommerce_params || {};
	var store = window.gooStore || {};

	function ready(fn) {
		if (document.readyState === 'loading') {
			document.addEventListener('DOMContentLoaded', fn);
		} else {
			fn();
		}
	}

	function matches(variation, chosen) {
		var attrs = variation.attributes || {};
		for (var k in chosen) {
			if (!Object.prototype.hasOwnProperty.call(chosen, k)) {
				continue;
			}
			var want = attrs[k];
			if (want === undefined) {
				continue;
			}
			if (want !== '' && want !== chosen[k] && chosen[k] !== '') {
				return false;
			}
		}
		return true;
	}

	function VariationForm(form) {
		this.form = form;
		var raw = form.getAttribute('data-product_variations');
		this.variations = raw && raw !== 'false' ? JSON.parse(raw) : null;
		this.useAjax = this.variations === null;
		this.selects = Array.prototype.slice.call(form.querySelectorAll('.variations select'));
		this.single = form.querySelector('.single_variation');
		this.button = form.querySelector('.single_add_to_cart_button');
		this.idInput = form.querySelector('input[name="variation_id"], input.variation_id');
		this.reset = form.querySelector('.reset_variations');
		this.qty = form.querySelector('input.qty');
		var product = form.closest('.product');
		this.sku = product ? product.querySelector('.product_meta .sku') : null;
		if (this.sku && !this.sku.hasAttribute('data-o_content')) {
			this.sku.setAttribute('data-o_content', this.sku.textContent);
		}
		this.price = product ? product.querySelector('.summary > p.price') : null;
		var self = this;
		this.selects.forEach(function (s) {
			s.addEventListener('change', function () { self.update(); });
		});
		if (this.reset) {
			this.reset.addEventListener('click', function (e) {
				e.preventDefault();
				self.selects.forEach(function (s) { s.value = ''; });
				self.update();
				var alert = form.querySelector('.reset_variations_alert');
				if (alert && params.i18n_reset_alert_text) {
					alert.textContent = params.i18n_reset_alert_text;
				}
			});
		}
		form.addEventListener('submit', function (e) {
			if (self.button && self.button.classList.contains('disabled')) {
				e.preventDefault();
				var msg = self.button.classList.contains('wc-variation-is-unavailable') ? params.i18n_unavailable_text : params.i18n_make_a_selection_text;
				if (msg) {
					window.alert(msg);
				}
			}
		});
		this.update();
	}

	VariationForm.prototype.chosen = function () {
		var out = {};
		this.selects.forEach(function (s) {
			out[s.getAttribute('data-attribute_name') || s.name] = s.value;
		});
		return out;
	};

	VariationForm.prototype.allChosen = function () {
		return this.selects.every(function (s) { return s.value !== ''; });
	};

	// refreshOptions marks the values that can still be combined with the other choices.
	VariationForm.prototype.refreshOptions = function () {
		if (!this.variations) {
			return;
		}
		var chosen = this.chosen();
		var self = this;
		this.selects.forEach(function (s) {
			var name = s.getAttribute('data-attribute_name') || s.name;
			var others = {};
			Object.keys(chosen).forEach(function (k) {
				if (k !== name) {
					others[k] = chosen[k];
				}
			});
			var avail = {};
			var any = false;
			self.variations.forEach(function (v) {
				if (!v.variation_is_active || !matches(v, others)) {
					return;
				}
				var val = (v.attributes || {})[name];
				if (val === '') {
					any = true;
				} else if (val !== undefined) {
					avail[val] = true;
				}
			});
			Array.prototype.forEach.call(s.options, function (o) {
				if (o.value === '') {
					return;
				}
				var ok = any || avail[o.value];
				o.disabled = !ok;
				o.className = ok ? 'attached enabled' : 'attached';
			});
		});
	};

	VariationForm.prototype.show = function (v) {
		var self = this;
		var html = '';
		if (v) {
			html = '<div class="woocommerce-variation-description">' + (v.variation_description || '') + '</div>' +
				'<div class="woocommerce-variation-price">' + (v.price_html || '') + '</div>' +
				'<div class="woocommerce-variation-availability">' + (v.availability_html || '') + '</div>';
		}
		if (this.single) {
			this.single.innerHTML = html;
			this.single.style.display = v ? '' : 'none';
		}
		if (this.idInput) {
			this.idInput.value = v ? v.variation_id : '';
		}
		if (this.sku) {
			this.sku.textContent = v && v.sku ? v.sku : this.sku.getAttribute('data-o_content');
		}
		if (this.qty && v) {
			this.qty.min = v.min_qty || 1;
			this.qty.max = v.max_qty === '' ? '' : v.max_qty;
			if (v.is_sold_individually === 'yes') {
				this.qty.value = '1';
				this.qty.closest('.quantity').style.display = 'none';
			} else {
				this.qty.closest('.quantity').style.display = '';
			}
		}
		if (this.button) {
			var buyable = v && v.is_purchasable && v.is_in_stock && v.variation_is_visible;
			this.button.classList.toggle('disabled', !buyable);
			this.button.classList.toggle('wc-variation-selection-needed', !v);
			this.button.classList.toggle('wc-variation-is-unavailable', !!v && !buyable);
		}
		if (this.reset) {
			this.reset.style.visibility = this.selects.some(function (s) { return s.value !== ''; }) ? 'visible' : 'hidden';
		}
		this.form.dispatchEvent(new CustomEvent('goo:variation-image', { bubbles: true, detail: { variation: v } }));
		this.form.dispatchEvent(new CustomEvent(v ? 'found_variation' : 'reset_data', { bubbles: true, detail: { variation: v } }));
		self.form.classList.toggle('goo-variation-found', !!v);
	};

	VariationForm.prototype.update = function () {
		this.refreshOptions();
		if (!this.allChosen()) {
			this.show(null);
			return;
		}
		var chosen = this.chosen();
		var self = this;
		if (this.useAjax) {
			var data = Object.assign({ product_id: this.form.getAttribute('data-product_id') }, chosen);
			store.post('get_variation', data).then(function (v) {
				if (!v) {
					self.noMatch();
					return;
				}
				self.show(v);
			});
			return;
		}
		var found = null;
		for (var i = 0; i < this.variations.length; i++) {
			if (matches(this.variations[i], chosen)) {
				found = this.variations[i];
				break;
			}
		}
		if (found) {
			this.show(found);
		} else {
			this.noMatch();
		}
	};

	VariationForm.prototype.noMatch = function () {
		this.show(null);
		if (this.single) {
			this.single.innerHTML = '<p class="wc-no-matching-variations woocommerce-info">' + (params.i18n_no_matching_variations_text || '') + '</p>';
			this.single.style.display = '';
		}
	};

	ready(function () {
		document.querySelectorAll('form.variations_form').forEach(function (f) {
			if (!f.gooVariationForm) {
				f.gooVariationForm = new VariationForm(f);
			}
		});
	});
})();
