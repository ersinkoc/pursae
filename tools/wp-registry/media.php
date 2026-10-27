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
function admin_url(...$a){return "/wp-admin/".ltrim($a[0]??"","/");}
function get_post_mime_types(...$a){return null;}
function wp_create_nonce(...$a){return "NONCE:".($a[0]??"");}
function rest_url(...$a){return "/wp-json/".ltrim($a[0]??"","/");}
function get_post_type_object(...$a){return null;}
function wp_enqueue_script(...$a){return null;}
function wp_enqueue_style(...$a){return null;}
function is_admin(...$a){return true;}
function wp_scripts(...$a){ static $o; if(!$o){ $o=new class { function __call($n,$a){ if($n==="add_data"){ $GLOBALS["captured"]["plupload:".$a[1]]=$a[2]; return true;} return ""; } }; } return $o; }
function wp_convert_hr_to_bytes(...$a){return 67108864;}
function wp_is_mobile(...$a){return false;}
function is_multisite(...$a){return false;}
function is_ssl(...$a){return false;}
function absint(...$a){return null;}
function _is_utf8_charset(...$a){return null;}
function post_type_supports(...$a){return null;}
function selected(...$a){return null;}
function get_bloginfo(...$a){return "7.1.3";}
//MARK
require_once ABSPATH.WPINC.'/plugin.php';
add_filter('pre_wp_load_alloptions', function(){ return array(); });
require_once ABSPATH.WPINC.'/functions.php';
require_once ABSPATH.WPINC.'/formatting.php';
require_once ABSPATH.WPINC.'/kses.php';
require_once ABSPATH.WPINC.'/pomo/translations.php';
require_once ABSPATH.WPINC.'/l10n.php';
require_once ABSPATH.WPINC.'/class-wp-locale.php'; $GLOBALS['wp_locale']=new WP_Locale();
require_once ABSPATH.WPINC.'/media.php';
require_once ABSPATH.WPINC.'/media-template.php';
function wp_localize_script($h,$n,$d){ $GLOBALS['captured'][$n]=$d; return true; }
wp_enqueue_media();
ob_start(); wp_print_media_templates(); $tpl=ob_get_clean();
echo json_encode(array('l10n'=>$GLOBALS['captured'],'templates'=>$tpl), JSON_PRETTY_PRINT|JSON_UNESCAPED_SLASHES);
