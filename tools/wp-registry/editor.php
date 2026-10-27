<?php
error_reporting(E_ALL & ~E_DEPRECATED & ~E_NOTICE & ~E_WARNING);
define('ABSPATH', $argv[1] . '/'); define('WPINC','wp-includes'); define('WP_CONTENT_DIR', ABSPATH.'wp-content'); define('WP_CONTENT_URL','/wp-content');
define('WP_DEBUG', false); define('MEDIA_TRASH', false); define('KB_IN_BYTES',1024); define('MB_IN_BYTES',1048576); define('GB_IN_BYTES',1073741824); define('TB_IN_BYTES',1099511627776); define('PB_IN_BYTES',1125899906842624); define('EB_IN_BYTES',1152921504606846976); define('ZB_IN_BYTES',1180591620717411303424); define('YB_IN_BYTES',1208925819614629174706176);
define('MINUTE_IN_SECONDS',60); define('HOUR_IN_SECONDS',3600); define('DAY_IN_SECONDS',86400); define('WEEK_IN_SECONDS',604800); define('MONTH_IN_SECONDS',2592000); define('YEAR_IN_SECONDS',31536000);
$GLOBALS['wpdb']=new class { public $prefix='wp_'; public $posts='wp_posts'; public $options='wp_options'; function __call($n,$a){ return in_array($n,array('get_results','get_col'))?array():null; } };
$GLOBALS['content_width']=840; $GLOBALS['captured']=array();
function add_shortcode(...$a){return null;}
function wp_installing(...$a){return null;}
function wp_cache_get(...$a){return false;}
function wp_cache_get_multiple(...$a){$o=array(); foreach(($a[0]??array()) as $k){$o[$k]=false;} return $o;}
function wp_cache_set_multiple(...$a){return array();}
function wp_cache_add(...$a){return true;}
function wp_cache_set(...$a){return true;}
function wp_cache_delete(...$a){return true;}
function get_user_option(...$a){return null;}
function get_post_mime_types(...$a){return null;}
function get_post_type_object(...$a){return null;}
function is_admin(...$a){return true;}
function wp_convert_hr_to_bytes(...$a){return 67108864;}
function wp_is_mobile(...$a){return false;}
function is_multisite(...$a){return false;}
function is_ssl(...$a){return false;}
function absint(...$a){return null;}
function _is_utf8_charset(...$a){return null;}
function post_type_supports(...$a){return null;}
function wp_enqueue_script(...$a){return null;}
function wp_enqueue_style(...$a){return null;}
function wp_print_styles(...$a){return null;}
function wp_print_scripts(...$a){return null;}
function wp_style_is(...$a){return true;}
function wp_script_is(...$a){return true;}
function wp_localize_script(...$a){return true;}
function current_theme_supports(...$a){return false;}
function wp_editor_set_quality(...$a){return null;}
function get_editor_stylesheets(...$a){return array();}
function current_user_can(...$a){return true;}
function admin_url(...$a){return "/wp-admin/".ltrim($a[0]??"","/");}
function includes_url(...$a){return "/wp-includes/".ltrim($a[0]??"","/");}
function wp_create_nonce(...$a){return "NONCE:".($a[0]??"");}
function get_post(...$a){ return (object)array('ID'=>0,'post_type'=>'post','post_status'=>'draft','page_template'=>''); }
function get_post_status(...$a){return 'draft';}
function get_post_format(...$a){return false;}
function get_page_template_slug(...$a){return '';}
function has_blocks(...$a){return null;}
function is_user_logged_in(...$a){return null;}
define('SCRIPT_DEBUG','');
function wp_get_current_user(...$a){return null;}
function script_concat_settings(...$a){return null;}
//MARK
require_once ABSPATH.WPINC.'/plugin.php';
add_filter('pre_wp_load_alloptions', function(){ return array(); });
require_once ABSPATH.WPINC.'/functions.php';
require_once ABSPATH.WPINC.'/formatting.php';
require_once ABSPATH.WPINC.'/kses.php';
require_once ABSPATH.WPINC.'/pomo/translations.php';
require_once ABSPATH.WPINC.'/l10n.php';
require_once ABSPATH.WPINC.'/general-template.php';
require_once ABSPATH.WPINC.'/class-wp-editor.php';
$GLOBALS['post']=get_post();
add_filter('user_can_richedit','__return_true');
add_action('media_buttons', function(){ echo '<button type="button" id="insert-media-button" class="button insert-media add_media" data-editor="content"><span class="wp-media-buttons-icon"></span> Add Media</button>'; });
$GLOBALS['wp_version']='7.1.3'; $GLOBALS['tinymce_version']='49110-20250317';
ob_start();
_WP_Editors::editor('POSTCONTENT', 'content', array('_content_editor_dfw'=>true,'drag_drop_upload'=>true,'tabfocus_elements'=>'content-html,save-post','editor_height'=>300,
 'tinymce'=>array('resize'=>false,'wp_autoresize_on'=>true,'add_unload_trigger'=>false,'wp_keep_scroll_position'=>true)));
$markup=ob_get_clean();
ob_start(); _WP_Editors::editor_js(); $js=ob_get_clean();
ob_start(); _WP_Editors::wp_link_dialog(); $link=ob_get_clean();
ob_start(); echo '<script>'."\n"; echo "tinymce.addI18n( 'en', " . json_encode(array()) . ");\n"; $tr=ob_get_clean();
ob_start(); $mt=_WP_Editors::wp_mce_translation('', true); ob_end_clean();
echo json_encode(array('i18n'=>$mt,'markup'=>$markup,'js'=>$js,'link'=>$link), JSON_PRETTY_PRINT|JSON_UNESCAPED_SLASHES);
