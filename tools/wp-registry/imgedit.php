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
function get_post_type_object(...$a){return null;}
function is_admin(...$a){return true;}
function wp_convert_hr_to_bytes(...$a){return 67108864;}
function wp_is_mobile(...$a){return false;}
function is_multisite(...$a){return false;}
function is_ssl(...$a){return false;}
function _is_utf8_charset(...$a){return null;}
function post_type_supports(...$a){return null;}
function wp_create_nonce(...$a){return "NONCEVALUE";}
function admin_url(...$a){return "/wp-admin/".ltrim($a[0]??"","/");}
function wp_get_attachment_metadata(...$a){ return array('width'=>4447,'height'=>4448,'file'=>'FILEPATH','sizes'=>array('thumbnail'=>array('width'=>150,'height'=>150,'file'=>'t.jpg'))); }
function get_post_mime_type(...$a){return 'image/jpeg';}
function wp_image_editor_supports(...$a){return true;}
function get_post_meta(...$a){return BACKUPSIZES;}
function current_user_can(...$a){return true;}
function absint($v){return abs((int)$v);}
function wp_get_image_editor(...$a){ return new WP_Error('x','y'); }
function wp_get_registered_image_subsizes(...$a){return array('thumbnail'=>array('width'=>150,'height'=>150,'crop'=>true));}
function image_get_intermediate_size(...$a){return null;}
//MARK
require_once ABSPATH.WPINC.'/plugin.php';
add_filter('pre_wp_load_alloptions', function(){ return array(); });
require_once ABSPATH.WPINC.'/functions.php';
require_once ABSPATH.WPINC.'/formatting.php';
require_once ABSPATH.WPINC.'/kses.php';
require_once ABSPATH.WPINC.'/pomo/translations.php';
require_once ABSPATH.WPINC.'/l10n.php';
require_once ABSPATH.WPINC.'/general-template.php';
require_once ABSPATH.WPINC.'/class-wp-error.php';
require_once ABSPATH.'wp-admin/includes/image-edit.php';
define('BACKUPSIZES', '');
ob_start(); wp_image_editor(424242, false); $html=ob_get_clean();
echo $html;
