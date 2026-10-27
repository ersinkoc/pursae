<?php
error_reporting(E_ALL & ~E_DEPRECATED & ~E_NOTICE & ~E_WARNING);
define('ABSPATH', $argv[1] . '/'); define('WPINC','wp-includes'); define('SCRIPT_DEBUG', false); define('CONCATENATE_SCRIPTS', false);
define('WP_CONTENT_URL','/wp-content'); define('WP_PLUGIN_URL','/wp-content/plugins');
$GLOBALS['wp_version']='7.1.3'; $GLOBALS['tinymce_version']='49110-20250317';

function __($s){return $s;}
function _x($s){return $s;}
function _n($a,$b,$n){return $n==1?$a:$b;}
function esc_html__($s){return $s;}
function esc_attr__($s){return $s;}
function esc_url($s){return $s;}
function esc_attr($s){return $s;}
function esc_html($s){return $s;}
function esc_js($s){return $s;}
function sanitize_key($s){return $s;}
function includes_url($p=''){return '/wp-includes/'.ltrim($p,'/');}
function admin_url($p=''){return '/wp-admin/'.ltrim($p,'/');}
function site_url($p=''){return 'SITEURL'.$p;}
function home_url($p=''){return 'HOMEURL'.$p;}
function content_url($p=''){return '/wp-content'.$p;}
function plugins_url($p=''){return '/wp-content/plugins'.$p;}
function rest_url($p=''){return 'RESTURL'.$p;}
function wp_guess_url(){return 'SITEURL';}
function did_action($a){return 1;}
function doing_action(){return false;}
function apply_filters($h,$v){return $v;}
function do_action(){}
function add_action(){}
function add_filter(){}
function has_filter(){return false;}
function remove_action(){}
function wp_create_nonce($a){return 'NONCE:'.$a;}
function wp_installing(){return false;}
function is_admin(){return true;}
function is_rtl(){return false;}
function get_locale(){return 'en_US';}
function get_user_locale(){return 'en_US';}
function determine_locale(){return 'en_US';}
function wp_json_encode($v,$f=0){return json_encode($v,$f|JSON_UNESCAPED_SLASHES);}
function get_option($k,$d=false){return $d;}
function get_site_option($k,$d=false){return $d;}
function is_multisite(){return false;}
function current_user_can(){return true;}
function get_current_user_id(){return 1;}
function wp_max_upload_size(){return 'MAXUPLOAD';}
function size_format($s){return $s;}
function wp_get_mime_types(){return array();}
function get_allowed_mime_types(){return array();}
function wp_get_ext_types(){return array();}
function wp_is_mobile(){return false;}
function is_ssl(){return false;}
function wp_using_themes(){return true;}
function _deprecated_function(){}
function _doing_it_wrong(){}
function wp_get_environment_type(){return 'production';}
function wp_get_update_php_url(){return '';}
function wp_get_default_update_php_url(){return '';}
function wp_get_wp_version(){return '7.1.3';}
function get_bloginfo($k=''){return '7.1.3';}
function number_format_i18n($n){return $n;}
function wp_timezone_string(){return 'UTC';}
function wp_timezone(){return new DateTimeZone('UTC');}
function wp_parse_url($u,$c=-1){return parse_url($u,$c);}
function current_theme_supports(){return false;}
function get_theme_support(){return false;}
function wp_check_php_version(){return false;}
function user_can_richedit(){return true;}
function wp_get_admin_notice($m){return $m;}
function is_user_logged_in(){return true;}
function wp_get_current_user(){return (object)array('ID'=>1);}
function get_user_option(){return false;}
function __return_false(){return false;}
function _wp_specialchars($s){return $s;}
function wp_kses_post($s){return $s;}
function wp_unslash($s){return $s;}
function sanitize_text_field($s){return $s;}
function wp_get_list_item_separator(){return ', ';}
function wp_get_word_count_type(){return 'words';}
function get_template_directory_uri(){return '/wp-content/themes/twentysixteen';}
function wp_register_script_module(){}
function wp_script_modules(){ return new class { function __call($a,$b){} }; }
function wp_enqueue_script_module(){}
function wp_style_engine_get_stylesheet_from_context(){return '';}
function wp_get_global_stylesheet(){return '';}
function wp_get_upload_dir(){return array('baseurl'=>'/wp-content/uploads');}
function get_post_type_object(){return null;}
function wp_get_theme(){return null;}
function do_action_ref_array(...$a){return null;}
function sanitize_url(...$a){return null;}
function get_rest_url(...$a){return null;}
$GLOBALS['wpdb']=new class { public $prefix='wp_'; public $base_prefix='wp_'; function __call($n,$a){ return $n==='get_blog_prefix' ? 'wp_' : null; } };
function get_user_meta(...$a){return null;}
define('SITECOOKIEPATH','/'); define('COOKIEPATH','/'); define('ADMIN_COOKIE_PATH','/wp-admin'); define('COOKIE_DOMAIN',''); define('WP_DEBUG',false); define('AUTOSAVE_INTERVAL',60); define('EMPTY_TRASH_DAYS',30); define('MINUTE_IN_SECONDS',60); define('HOUR_IN_SECONDS',3600); define('DAY_IN_SECONDS',86400); define('WEEK_IN_SECONDS',604800); define('MONTH_IN_SECONDS',2592000); define('YEAR_IN_SECONDS',31536000); define('KB_IN_BYTES',1024); define('MB_IN_BYTES',1048576);
function wp_theme_has_theme_json(...$a){return null;}
function wp_get_view_transitions_admin_css(...$a){return null;}
//MARK
require_once ABSPATH.'wp-includes/class-wp-locale.php'; $GLOBALS['wp_locale']=new WP_Locale();
require_once ABSPATH.'wp-includes/script-loader.php';
$GLOBALS['wp_scripts']=$s=new WP_Scripts(); $GLOBALS['wp_styles']=$st=new WP_Styles();
$s->base_url=''; $s->default_version='7.1.3'; $st->base_url=''; $st->default_version='7.1.3';
wp_default_packages($s); wp_default_scripts($s); wp_default_styles($st);
$out=array('scripts'=>array(),'styles'=>array());
foreach($s->registered as $h=>$d){ $out['scripts'][$h]=array('src'=>$d->src,'deps'=>array_values($d->deps),'ver'=>$d->ver,'footer'=>((string)$d->args==='1'||!empty($d->extra['group'])),'data'=>$d->extra['data']??null,'before'=>$d->extra['before']??null,'after'=>$d->extra['after']??null,'translations'=>!empty($d->textdomain),'strategy'=>$d->extra['strategy']??null); }
foreach($st->registered as $h=>$d){ $out['styles'][$h]=array('src'=>$d->src,'deps'=>array_values($d->deps),'ver'=>$d->ver,'after'=>$d->extra['after']??null); }
echo json_encode($out, JSON_PRETTY_PRINT|JSON_UNESCAPED_SLASHES);
