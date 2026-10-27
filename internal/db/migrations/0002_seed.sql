-- Built-in content types, taxonomies and roles, matching a fresh WordPress install.

INSERT INTO content_types (slug, name, hierarchical, public, rest_base, is_builtin, supports, labels) VALUES
('post', 'Posts', false, true, 'posts', true,
 '{"title":true,"editor":true,"author":true,"thumbnail":true,"excerpt":true,"trackbacks":true,"custom-fields":true,"comments":true,"revisions":true,"post-formats":true,"autosave":true}',
 '{"name":"Posts","singular_name":"Post","add_new":"Add New Post","add_new_item":"Add New Post","edit_item":"Edit Post","new_item":"New Post","view_item":"View Post","view_items":"View Posts","search_items":"Search Posts","not_found":"No posts found.","not_found_in_trash":"No posts found in Trash.","all_items":"All Posts","archives":"Post Archives","menu_name":"Posts"}'),
('page', 'Pages', true, true, 'pages', true,
 '{"title":true,"editor":true,"author":true,"thumbnail":true,"page-attributes":true,"custom-fields":true,"comments":true,"revisions":true,"autosave":true}',
 '{"name":"Pages","singular_name":"Page","add_new":"Add New Page","add_new_item":"Add New Page","edit_item":"Edit Page","new_item":"New Page","view_item":"View Page","view_items":"View Pages","search_items":"Search Pages","not_found":"No pages found.","not_found_in_trash":"No pages found in Trash.","all_items":"All Pages","archives":"Page Archives","parent_item_colon":"Parent Page:","menu_name":"Pages"}'),
('attachment', 'Media', false, true, 'media', true,
 '{"title":true,"author":true,"comments":true}',
 '{"name":"Media","singular_name":"Media","add_new":"Add Media File","edit_item":"Edit Media","view_item":"View Attachment Page","all_items":"Media Library","menu_name":"Media"}'),
('revision', 'Revisions', false, false, 'revisions', true, '{"author":true}', '{"name":"Revisions","singular_name":"Revision"}'),
('wp_block', 'Patterns', false, false, 'blocks', true,
 '{"title":true,"editor":true,"revisions":true,"custom-fields":true}',
 '{"name":"Patterns","singular_name":"Pattern","add_new_item":"Add New Pattern","edit_item":"Edit Block Pattern","all_items":"All Patterns","menu_name":"Patterns"}');

INSERT INTO taxonomies (slug, name, hierarchical, object_types, rest_base, public, is_builtin, labels) VALUES
('category', 'Categories', true, '["post"]', 'categories', true, true,
 '{"name":"Categories","singular_name":"Category","search_items":"Search Categories","all_items":"All Categories","parent_item":"Parent Category","parent_item_colon":"Parent Category:","edit_item":"Edit Category","view_item":"View Category","update_item":"Update Category","add_new_item":"Add New Category","new_item_name":"New Category Name","not_found":"No categories found.","no_terms":"No categories","back_to_items":"&larr; Go to Categories","menu_name":"Categories"}'),
('post_tag', 'Tags', false, '["post"]', 'tags', true, true,
 '{"name":"Tags","singular_name":"Tag","search_items":"Search Tags","popular_items":"Popular Tags","all_items":"All Tags","edit_item":"Edit Tag","view_item":"View Tag","update_item":"Update Tag","add_new_item":"Add New Tag","new_item_name":"New Tag Name","separate_items_with_commas":"Separate tags with commas","add_or_remove_items":"Add or remove tags","choose_from_most_used":"Choose from the most used tags","not_found":"No tags found.","no_terms":"No tags","back_to_items":"&larr; Go to Tags","menu_name":"Tags"}'),
('post_format', 'Formats', false, '["post"]', '', true, true, '{"name":"Formats","singular_name":"Format"}');

INSERT INTO terms (taxonomy, slug, name) VALUES ('category', 'uncategorized', 'Uncategorized');

INSERT INTO roles (slug, name, is_system, capabilities) VALUES
('administrator', 'Administrator', true, (SELECT jsonb_object_agg(c, true) FROM unnest(ARRAY[
	'switch_themes','edit_themes','activate_plugins','edit_plugins','edit_users','edit_files','manage_options',
	'moderate_comments','manage_categories','manage_links','upload_files','import','unfiltered_html',
	'edit_posts','edit_others_posts','edit_published_posts','publish_posts','edit_pages','read',
	'level_10','level_9','level_8','level_7','level_6','level_5','level_4','level_3','level_2','level_1','level_0',
	'edit_others_pages','edit_published_pages','publish_pages','delete_pages','delete_others_pages',
	'delete_published_pages','delete_posts','delete_others_posts','delete_published_posts','delete_private_posts',
	'edit_private_posts','read_private_posts','delete_private_pages','edit_private_pages','read_private_pages',
	'delete_users','create_users','unfiltered_upload','edit_dashboard','update_plugins','delete_plugins',
	'install_plugins','update_themes','install_themes','update_core','list_users','remove_users','promote_users',
	'edit_theme_options','delete_themes','export']) AS c)),
('editor', 'Editor', true, (SELECT jsonb_object_agg(c, true) FROM unnest(ARRAY[
	'moderate_comments','manage_categories','manage_links','upload_files','unfiltered_html',
	'edit_posts','edit_others_posts','edit_published_posts','publish_posts','edit_pages','read',
	'level_7','level_6','level_5','level_4','level_3','level_2','level_1','level_0',
	'edit_others_pages','edit_published_pages','publish_pages','delete_pages','delete_others_pages',
	'delete_published_pages','delete_posts','delete_others_posts','delete_published_posts','delete_private_posts',
	'edit_private_posts','read_private_posts','delete_private_pages','edit_private_pages','read_private_pages']) AS c)),
('author', 'Author', true, (SELECT jsonb_object_agg(c, true) FROM unnest(ARRAY[
	'upload_files','edit_posts','edit_published_posts','publish_posts','read',
	'level_2','level_1','level_0','delete_posts','delete_published_posts']) AS c)),
('contributor', 'Contributor', true, (SELECT jsonb_object_agg(c, true) FROM unnest(ARRAY[
	'edit_posts','read','level_1','level_0','delete_posts']) AS c)),
('subscriber', 'Subscriber', true, '{"read": true, "level_0": true}');
