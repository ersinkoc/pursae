<?php
function __($s,$d=null){return $s;} function _x($s,$c=null,$d=null){return $s;} function esc_attr__($s){return $s;} function esc_html__($s){return $s;} function esc_url($s){return $s;} function esc_attr($s){return $s;} function esc_html($s){return $s;} function esc_attr_e($s){echo $s;} function esc_html_e($s){echo $s;}
function _e($s){echo $s;} function wp_kses_post($s){return $s;} function get_template_directory_uri(){return '';} function includes_url($p=''){return '/wp-includes/'.$p;} function sprintf_($s){return $s;}
define('ABSPATH', $argv[1].'/');
$out=array();
foreach (glob($argv[1].'/wp-includes/block-patterns/*.php') as $f) { $p = include $f; if (is_array($p)) { $p['name']='core/'.basename($f,'.php'); $out[]=$p; } }
echo json_encode($out, JSON_PRETTY_PRINT|JSON_UNESCAPED_SLASHES);
