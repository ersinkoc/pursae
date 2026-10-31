/* Pursae Commerce catalog admin: the Product data box (tabs, type-dependent fields, attributes,
 * variations, downloads, gallery, linked products), the products list (sorting, featured star)
 * and small helpers of the product edit screen. jQuery, jQuery UI and wp.media come from core. */
(function ($) {
	'use strict';

	var coreTypes = ['simple', 'variable', 'grouped', 'external', 'variable-subscription'];
	// variable subscriptions use the variable product fields (show_if_variable) as well as their own.
	var variableLike = ['variable', 'variable-subscription'];

	/* Product data box ---------------------------------------------------------------------------- */

	function productType() {
		return $('#product-type').val() || 'simple';
	}

	// show_if_* / hide_if_* like meta-boxes-product.js (show_and_hide_panels).
	function showHidePanels() {
		var type = productType(),
			isVirtual = $('#_virtual').is(':checked'),
			isDownloadable = $('#_downloadable').is(':checked'),
			isVariable = variableLike.indexOf(type) !== -1,
			simpleLike = coreTypes.indexOf(type) === -1; // module types behave like simple products
		var $box = $('#woocommerce-product-data');
		if (!$box.length) {
			return;
		}
		$box.find('.show_if_simple, .show_if_variable, .show_if_grouped, .show_if_external, .show_if_downloadable, .show_if_virtual').hide();
		$box.find('[class*="show_if_"]').each(function () {
			var $el = $(this), cls = this.className.split(/\s+/), show = false;
			cls.forEach(function (c) {
				if (c === 'show_if_' + type || (simpleLike && c === 'show_if_simple') || (isVariable && c === 'show_if_variable')) {
					show = true;
				}
				if (c === 'show_if_downloadable' && isDownloadable && (type === 'simple' || simpleLike)) {
					show = true;
				}
				if (c === 'show_if_virtual' && isVirtual) {
					show = true;
				}
			});
			$el.toggle(show);
		});
		$box.find('[class*="hide_if_"]').each(function () {
			var cls = this.className.split(/\s+/), hide = false;
			cls.forEach(function (c) {
				if (c === 'hide_if_' + type || (isVariable && c === 'hide_if_variable') || (c === 'hide_if_virtual' && isVirtual) || (c === 'hide_if_downloadable' && isDownloadable)) {
					hide = true;
				}
			});
			if (hide) {
				$(this).hide();
			}
		});
		// The type box's Virtual / Downloadable checkboxes are for simple products only.
		if (isVariable || type === 'grouped' || type === 'external') {
			$box.find('.type_box .has-checkbox').hide();
		}
		var $active = $box.find('ul.wc-tabs li.active:visible');
		if (!$active.length) {
			$box.find('ul.wc-tabs li:visible').eq(0).find('a').trigger('click');
		}
	}

	function initTabs() {
		var $box = $('#woocommerce-product-data');
		$box.on('click', 'ul.wc-tabs a', function (e) {
			e.preventDefault();
			var $li = $(this).parent();
			$box.find('ul.wc-tabs li').removeClass('active');
			$li.addClass('active');
			$box.find('div.panel').addClass('hidden').hide();
			$($(this).attr('href')).removeClass('hidden').show();
			var href = ($(this).attr('href') || '').replace('#', ''),
				tab = {product_attributes: 'attributes', variable_product_options: 'variations'}[href] || href.replace('_product_data', '');
			$('#goo-current-tab').val(tab);
		});
		var $start = $box.find('ul.wc-tabs li.active a');
		if (!$start.length) {
			$start = $box.find('ul.wc-tabs li:first a');
		}
		$start.trigger('click');
		// Clicks on the type select must not fold the box.
		$box.find('.type_box').on('click', function (e) {
			e.stopPropagation();
		});
	}

	function initSaleSchedules() {
		$(document).on('click', '#woocommerce-product-data .sale_schedule', function (e) {
			e.preventDefault();
			var $wrap = $(this).closest('.options_group, .variable_pricing');
			$(this).addClass('hidden').hide();
			$wrap.find('.sale_price_dates_fields').removeClass('hidden').show();
		});
		$(document).on('click', '#woocommerce-product-data .cancel_sale_schedule', function (e) {
			e.preventDefault();
			var $wrap = $(this).closest('.options_group, .variable_pricing');
			$wrap.find('.sale_schedule').removeClass('hidden').show();
			$wrap.find('.sale_price_dates_fields').addClass('hidden').hide().find('input').val('');
		});
		if ($.fn.datepicker) {
			$('#woocommerce-product-data').find('.goo-datepicker').datepicker({dateFormat: 'yy-mm-dd', numberOfMonths: 1, showButtonPanel: true});
		}
	}

	/* Downloadable files ------------------------------------------------------------------------------- */

	function initDownloads() {
		$(document).on('click', '.downloadable_files a.insert', function (e) {
			e.preventDefault();
			var html = $('#goo-download-row-template').html() || '';
			var prefix = $(this).data('prefix'), suffix = $(this).data('suffix');
			if (prefix) {
				html = html.split('_wc_file_').join(prefix + '_').split('[]').join((suffix || '') + '[]');
			}
			$(this).closest('.downloadable_files').find('tbody').append(html);
		});
		$(document).on('click', '.downloadable_files a.delete', function (e) {
			e.preventDefault();
			$(this).closest('tr').remove();
		});
		$(document).on('click', '.downloadable_files a.upload_file_button', function (e) {
			e.preventDefault();
			if (!window.wp || !wp.media) {
				return;
			}
			var $input = $(this).closest('tr').find('td.file_url input'), $name = $(this).closest('tr').find('td.file_name input[type=text]');
			var frame = wp.media({title: $(this).data('choose') || 'Choose file', button: {text: $(this).data('update') || 'Insert file URL'}, multiple: false});
			frame.on('select', function () {
				var a = frame.state().get('selection').first().toJSON();
				$input.val(a.url);
				if (!$name.val()) {
					$name.val(a.title || a.filename || '');
				}
			});
			frame.open();
		});
		if ($.fn.sortable) {
			$('.downloadable_files tbody').sortable({items: 'tr', cursor: 'move', axis: 'y', handle: 'td.sort'});
		}
	}

	/* Product gallery ----------------------------------------------------------------------------------- */

	function syncGallery() {
		var ids = [];
		$('#product_images_container ul li.image').each(function () {
			ids.push($(this).data('attachment_id'));
		});
		$('#product_image_gallery').val(ids.join(','));
	}

	function initGallery() {
		var $list = $('#product_images_container ul.product_images');
		$('.add_product_images').on('click', 'a', function (e) {
			e.preventDefault();
			if (!window.wp || !wp.media) {
				return;
			}
			var $a = $(this);
			var frame = wp.media({title: $a.data('choose'), button: {text: $a.data('update')}, states: [new wp.media.controller.Library({
				title: $a.data('choose'), filterable: 'all', multiple: true, library: wp.media.query({type: 'image'})})]});
			frame.on('select', function () {
				frame.state().get('selection').each(function (att) {
					att = att.toJSON();
					if (!att.id) {
						return;
					}
					var src = att.sizes && att.sizes.thumbnail ? att.sizes.thumbnail.url : att.url;
					$list.append('<li class="image" data-attachment_id="' + att.id + '"><img src="' + src + '" alt="" /><ul class="actions"><li><a href="#" class="delete" title="' +
						$a.data('delete') + '">' + $a.data('text') + '</a></li></ul></li>');
				});
				syncGallery();
			});
			frame.open();
		});
		$('#product_images_container').on('click', 'a.delete', function (e) {
			e.preventDefault();
			$(this).closest('li.image').remove();
			syncGallery();
		});
		if ($.fn.sortable) {
			$list.sortable({items: 'li.image', cursor: 'move', scrollSensitivity: 40, forcePlaceholderSize: true, helper: 'clone', opacity: 0.65,
				placeholder: 'wc-metabox-sortable-placeholder', update: syncGallery});
		}
	}

	/* Attributes ---------------------------------------------------------------------------------------- */

	function attributeIndex() {
		var max = -1;
		$('.product_attributes input[name^="attribute_names["]').each(function () {
			var m = /\[(\d+)\]/.exec(this.name);
			if (m) {
				max = Math.max(max, parseInt(m[1], 10));
			}
		});
		return max + 1;
	}

	function syncAttributePositions() {
		$('.product_attributes .woocommerce_attribute').each(function (i) {
			$(this).find('.attribute_position').val(i);
		});
	}

	function addAttribute(taxonomy) {
		var $tpl = $('#goo-attribute-templates template.goo-attr-template').filter(function () {
			return ($(this).attr('data-taxonomy') || '') === (taxonomy || '');
		}).first();
		if (!$tpl.length) {
			return;
		}
		if (taxonomy && $('.product_attributes .woocommerce_attribute[data-taxonomy="' + taxonomy + '"]').length) {
			window.alert('This attribute is already added.');
			return;
		}
		var html = $tpl.html().split('__i__').join(String(attributeIndex()));
		var $row = $(html).appendTo('.product_attributes');
		$row.find('.attribute_position').val($('.product_attributes .woocommerce_attribute').length - 1);
		if (variableLike.indexOf(productType()) !== -1) {
			$row.find('input[name^="attribute_variation["]').prop('checked', true);
		}
		showHidePanels();
		$row.find('input.attribute_name, select.attribute_values').first().trigger('focus');
	}

	function initAttributes() {
		var $wrap = $('#product_attributes');
		$wrap.on('click', 'button.add_attribute', function () {
			addAttribute($wrap.find('select.attribute_taxonomy').val());
			$wrap.find('select.attribute_taxonomy').val('');
		});
		$wrap.on('click', 'button.add_custom_attribute', function () {
			addAttribute('');
		});
		$wrap.on('click', '.remove_row', function (e) {
			e.preventDefault();
			if (window.confirm('Remove this attribute?')) {
				$(this).closest('.woocommerce_attribute').remove();
				syncAttributePositions();
			}
		});
		$wrap.on('click', 'button.select_all_attributes', function () {
			$(this).closest('td').find('select.attribute_values option').prop('selected', true);
		});
		$wrap.on('click', 'button.select_no_attributes', function () {
			$(this).closest('td').find('select.attribute_values option').prop('selected', false);
		});
		$wrap.on('input', 'input.attribute_name', function () {
			$(this).closest('.woocommerce_attribute').find('strong.attribute_name').text(this.value || 'New attribute');
		});
		if ($.fn.sortable) {
			$wrap.find('.product_attributes').sortable({items: '.woocommerce_attribute', cursor: 'move', axis: 'y', handle: 'h3',
				scrollSensitivity: 40, forcePlaceholderSize: true, helper: 'clone', opacity: 0.65, placeholder: 'wc-metabox-sortable-placeholder',
				update: syncAttributePositions});
		}
	}

	// Expand / Close and the fold of each attribute / variation (wc-metaboxes).
	function initMetaboxes() {
		$(document).on('click', '.wc-metaboxes-wrapper .expand_all', function (e) {
			e.preventDefault();
			$(this).closest('.wc-metaboxes-wrapper').find('.wc-metabox').removeClass('closed').find('> .wc-metabox-content').show();
		});
		$(document).on('click', '.wc-metaboxes-wrapper .close_all', function (e) {
			e.preventDefault();
			$(this).closest('.wc-metaboxes-wrapper').find('.wc-metabox').addClass('closed').find('> .wc-metabox-content').hide();
		});
		$(document).on('click', '.wc-metabox > h3', function (e) {
			if ($(e.target).is('select, input, option, a, button, label')) {
				return;
			}
			var $box = $(this).parent();
			$box.toggleClass('closed').find('> .wc-metabox-content').toggle(!$box.hasClass('closed'));
		});
	}

	/* Variations ----------------------------------------------------------------------------------------- */

	function variationToggles($scope) {
		$scope.find('.woocommerce_variation').each(function () {
			var $v = $(this);
			$v.find('.show_if_variation_manage_stock').toggle($v.find('input.variable_manage_stock').is(':checked'));
			$v.find('.hide_if_variation_manage_stock').toggle(!$v.find('input.variable_manage_stock').is(':checked'));
			$v.find('.show_if_variation_downloadable').toggle($v.find('input.variable_is_downloadable').is(':checked'));
			$v.find('.hide_if_variation_virtual').toggle(!$v.find('input.variable_is_virtual').is(':checked'));
		});
	}

	function initVariations() {
		var $wrap = $('#variable_product_options');
		variationToggles($wrap);
		$wrap.on('change', 'input.variable_manage_stock, input.variable_is_downloadable, input.variable_is_virtual', function () {
			variationToggles($(this).closest('.woocommerce_variations'));
		});
		$wrap.on('click', '.remove_variation', function (e) {
			e.preventDefault();
			if (!window.confirm('Are you sure you want to remove this variation?')) {
				return;
			}
			var id = $(this).attr('rel');
			$(this).closest('form').append('<input type="hidden" name="goo_delete_variations[]" value="' + parseInt(id, 10) + '" />');
			$(this).closest('.woocommerce_variation').remove();
		});
		$wrap.on('click', 'button.generate_variations', function (e) {
			if (!window.confirm('Do you want to generate all variations? This will create a new variation for each and every possible combination of variation attributes (max 50 per run).')) {
				e.preventDefault();
			}
		});
		$wrap.on('click', 'button.do_variation_action', function (e) {
			var action = $('#field_to_edit').val();
			if (!action) {
				e.preventDefault();
				return;
			}
			if (action === 'delete_all') {
				if (!window.confirm('Are you sure you want to delete all variations? This cannot be undone.')) {
					e.preventDefault();
				}
				return;
			}
			if (/^toggle_|^variable_stock_status_/.test(action)) {
				return;
			}
			var label = /percentage|increase|decrease/.test(action) ? 'Enter a value (fixed or %)' : 'Enter a value';
			var value = window.prompt(label);
			if (value === null) {
				e.preventDefault();
				return;
			}
			$('#variable_bulk_value').val(value);
		});
		$wrap.on('click', '.upload_image_button', function (e) {
			e.preventDefault();
			var $btn = $(this), $input = $btn.find('.upload_image_id');
			if ($btn.hasClass('remove')) {
				$input.val('0');
				$btn.removeClass('remove').find('img').replaceWith('<span class="goo-no-image dashicons dashicons-format-image"></span>');
				return;
			}
			if (!window.wp || !wp.media) {
				return;
			}
			var frame = wp.media({title: 'Choose an image', button: {text: 'Set variation image'}, library: {type: 'image'}, multiple: false});
			frame.on('select', function () {
				var a = frame.state().get('selection').first().toJSON();
				var src = a.sizes && a.sizes.thumbnail ? a.sizes.thumbnail.url : a.url;
				$input.val(a.id);
				$btn.addClass('remove').find('.goo-no-image, img').replaceWith('<img src="' + src + '" alt="" />');
			});
			frame.open();
		});
		if ($.fn.sortable) {
			$wrap.find('.woocommerce_variations').sortable({items: '.woocommerce_variation', cursor: 'move', axis: 'y', handle: '.sort',
				update: function () {
					$wrap.find('.woocommerce_variation').each(function (i) {
						$(this).find('.variation_menu_order').val(i);
					});
				}});
		}
	}

	/* Linked products (wc-product-search) ------------------------------------------------------------- */

	function initProductSearch() {
		$('select.wc-product-search').each(function () {
			var $sel = $(this).hide(), url = $sel.data('url') || '/wp-admin/edit?post_type=product', timer = null;
			var $ui = $('<div class="goo-product-search"><ul class="goo-tokens"></ul><input type="text" class="goo-search-input" autocomplete="off" /><ul class="goo-results" hidden></ul></div>');
			$ui.find('.goo-search-input').attr('placeholder', $sel.data('placeholder') || '');
			$sel.after($ui);
			function render() {
				var $t = $ui.find('.goo-tokens').empty();
				$sel.find('option:selected').each(function () {
					var $li = $('<li class="goo-token"></li>').text($(this).text()).attr('data-id', this.value);
					$li.append('<button type="button" class="goo-token-remove" aria-label="Remove">&times;</button>');
					$t.append($li);
				});
			}
			render();
			$ui.on('click', '.goo-token-remove', function () {
				var id = $(this).parent().attr('data-id');
				$sel.find('option').filter(function () { return this.value === id; }).remove();
				render();
			});
			var $results = $ui.find('.goo-results');
			$ui.find('.goo-search-input').on('input', function () {
				var term = $.trim(this.value);
				clearTimeout(timer);
				if (term.length < 2) {
					$results.attr('hidden', true).empty();
					return;
				}
				timer = setTimeout(function () {
					var exclude = [$sel.data('exclude')];
					$sel.find('option').each(function () { exclude.push(this.value); });
					$.getJSON(url, {goo_search_products: term, exclude: exclude.join(','), include_variations: $sel.data('include-variations') ? 1 : 0}, function (data) {
						$results.empty();
						$.each(data || {}, function (id, name) {
							$('<li tabindex="0"></li>').text(name).attr('data-id', id).appendTo($results);
						});
						if (!$results.children().length) {
							$('<li class="goo-no-results"></li>').text('No matches found').appendTo($results);
						}
						$results.removeAttr('hidden');
					});
				}, 250);
			});
			$results.on('click keydown', 'li[data-id]', function (e) {
				if (e.type === 'keydown' && e.which !== 13) {
					return;
				}
				e.preventDefault();
				var id = $(this).attr('data-id');
				$('<option selected="selected"></option>').val(id).text($(this).text()).appendTo($sel);
				$results.attr('hidden', true).empty();
				$ui.find('.goo-search-input').val('');
				render();
			});
		});
	}

	/* Side boxes -------------------------------------------------------------------------------------- */

	function initSideBoxes() {
		// Catalog visibility (WC_Admin_Post_Types / meta-boxes-product.js).
		var $cv = $('#catalog-visibility-select');
		$('#catalog-visibility .edit-catalog-visibility').on('click', function (e) {
			e.preventDefault();
			if ($cv.is(':hidden')) {
				$cv.slideDown('fast');
				$(this).hide();
			}
		});
		$cv.on('click', '.save-post-visibility', function (e) {
			e.preventDefault();
			$cv.slideUp('fast');
			$('#catalog-visibility .edit-catalog-visibility').show();
			var label = $cv.find('input[name="_visibility"]:checked').data('label') || '';
			if ($('#_featured').is(':checked')) {
				label += ', Featured';
			}
			$('#catalog-visibility-display').text(label);
		});
		$cv.on('click', '.cancel-post-visibility', function (e) {
			e.preventDefault();
			$cv.slideUp('fast');
			$('#catalog-visibility .edit-catalog-visibility').show();
			var cur = $('#current_visibility').val() || 'visible';
			$cv.find('input[name="_visibility"][value="' + cur + '"]').prop('checked', true);
			$('#_featured').prop('checked', $('#current_featured').val() === 'yes');
		});

		// + Add new category: the term is created when the product is saved.
		$('#product_cat-add-toggle').on('click', function (e) {
			e.preventDefault();
			$('#product_cat-adder').toggleClass('wp-hidden-children');
			$('#newproduct_cat').trigger('focus');
		});
		$('#product_cat-add-submit').on('click', function () {
			var name = $.trim($('#newproduct_cat').val());
			if (!name) {
				return;
			}
			var parent = parseInt($('#newproduct_cat_parent').val(), 10);
			parent = parent > 0 ? parent : 0;
			var $li = $('<li><label class="selectit"><input type="checkbox" name="tax_input[product_cat][]" checked="checked" /> </label></li>');
			$li.find('input').val('new:' + parent + ':' + name);
			$li.find('label').append(document.createTextNode(name));
			$('#product_catchecklist').prepend($li);
			$('#newproduct_cat').val('');
		});
		$('#newproduct_cat').on('keydown', function (e) {
			if (e.which === 13) {
				e.preventDefault();
				$('#product_cat-add-submit').trigger('click');
			}
		});

		// Tags: the most used tags fill the comma list.
		$('#link-product_tag').on('click', function () {
			var $cloud = $('#tagcloud-product_tag').toggle();
			$(this).attr('aria-expanded', $cloud.is(':visible'));
		});
		$('#tagcloud-product_tag').on('click', 'a.goo-tag-pick', function (e) {
			e.preventDefault();
			var $in = $('#new-tag-product_tag'), tags = $.map(($in.val() || '').split(','), $.trim).filter(Boolean), t = $(this).text();
			if (tags.indexOf(t) === -1) {
				tags.push(t);
			}
			$in.val(tags.join(', '));
		});

		// The slug editor answers with the posts' permalink structure: show the product base instead.
		var $slugBox = $('#edit-slug-box'), base = $slugBox.data('goo-base');
		if (base) {
			$(document).ajaxComplete(function (e, xhr, opts) {
				if (!opts || typeof opts.data !== 'string' || opts.data.indexOf('action=sample-permalink') === -1) {
					return;
				}
				var $a = $slugBox.find('#sample-permalink a'), slug = $slugBox.find('#editable-post-name-full').text();
				if ($a.length && slug) {
					$a.contents().filter(function () { return this.nodeType === 3; }).remove();
					$a.prepend(document.createTextNode(base)).append(document.createTextNode('/'));
					$a.attr('href', base + slug + '/');
				}
			});
		}

		// The Product image box reuses the featured image box: keep WooCommerce's wording.
		var box = document.getElementById('postimagediv');
		if (box && window.MutationObserver) {
			var fix = function () {
				$(box).find('a#set-post-thumbnail').each(function () {
					if ($(this).text() === 'Set featured image') {
						$(this).text('Set product image');
					}
				});
				$(box).find('a#remove-post-thumbnail').each(function () {
					if ($(this).text() === 'Remove featured image') {
						$(this).text('Remove product image');
					}
				});
			};
			new MutationObserver(fix).observe(box, {childList: true, subtree: true});
		}
	}

	/* Products list ---------------------------------------------------------------------------------------- */

	function initList() {
		var $table = $('table.goo-sorting');
		if ($table.length && $.fn.sortable) {
			$table.find('tbody').sortable({items: 'tr:not(.inline-edit-row)', cursor: 'move', axis: 'y', handle: '.column-name, .column-thumb',
				helper: function (e, ui) {
					ui.children().each(function () { $(this).width($(this).width()); });
					return ui;
				},
				update: function (e, ui) {
					var id = ui.item.attr('id').replace('post-', ''), prev = ui.item.prev().attr('id'), next = ui.item.next().attr('id');
					$.getJSON('/wp-admin/edit', {post_type: 'product', action: 'woocommerce_product_ordering', id: id,
						previd: prev ? prev.replace('post-', '') : '', nextid: next ? next.replace('post-', '') : '', _wpnonce: $table.data('ordering-nonce')});
				}});
		}
		$('#the-list').on('click', 'a.goo-featured-toggle', function (e) {
			var $a = $(this);
			e.preventDefault();
			$.ajax({url: $a.attr('href'), dataType: 'json', headers: {'X-Requested-With': 'XMLHttpRequest'}}).done(function (r) {
				if (r && r.success) {
					$a.find('.wc-featured').toggleClass('not-featured', !r.featured).text(r.featured ? 'Yes' : 'No');
					$a.closest('tr').toggleClass('featured', !!r.featured);
				}
			}).fail(function () {
				window.location = $a.attr('href');
			});
		});
	}

	$(function () {
		if ($('#woocommerce-product-data').length) {
			initTabs();
			$(document).on('change', '#product-type, #_virtual, #_downloadable', showHidePanels);
			initSaleSchedules();
			initDownloads();
			initAttributes();
			initMetaboxes();
			initVariations();
			initProductSearch();
			showHidePanels();
		}
		if ($('#product_images_container').length) {
			initGallery();
		}
		if ($('#submitpost').length) {
			initSideBoxes();
		}
		initList();
	});
})(jQuery);
