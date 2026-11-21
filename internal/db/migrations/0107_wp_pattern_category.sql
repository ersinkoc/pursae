-- wp_pattern_category: the categories of user patterns (wp_block), a built-in flat taxonomy that is not
-- public (create_initial_taxonomies()). Served by /wp/v2/wp_pattern_category and the editor's pattern
-- category picker; terms reference their taxonomy, so the row must exist before the first term.
INSERT INTO taxonomies (slug, name, hierarchical, object_types, rest_base, public, is_builtin, labels) VALUES
('wp_pattern_category', 'Pattern Categories', false, '["wp_block"]', 'wp_pattern_category', false, true,
 '{"name":"Pattern Categories","singular_name":"Pattern Category","search_items":"Search Pattern Categories","popular_items":"Popular Pattern Categories","all_items":"All Pattern Categories","edit_item":"Edit Pattern Category","view_item":"View Pattern Category","update_item":"Update Pattern Category","add_new_item":"Add New Category","new_item_name":"New Pattern Category Name","separate_items_with_commas":"Separate pattern categories with commas","add_or_remove_items":"Add or remove pattern categories","choose_from_most_used":"Choose from the most used pattern categories","not_found":"No pattern categories found.","no_terms":"No pattern categories","back_to_items":"&larr; Go to Pattern Categories","menu_name":"Pattern Categories"}')
ON CONFLICT (slug) DO NOTHING;
