<?php
function __($s,$d=null){return $s;} function _x($s,$c=null,$d=null){return $s;} function esc_attr__($s,$d=null){return $s;} function esc_html__($s,$d=null){return $s;} function esc_url($s){return $s;}
function get_template_directory_uri(){return '/wp-content/themes/twentysixteen';} function function_exists_($f){return true;}
$GLOBALS['pat']=array(); $GLOBALS['cat']=array();
function register_block_pattern($n,$p){ $p['name']=$n; $GLOBALS['pat'][]=$p; }
function register_block_pattern_category($n,$p){ $GLOBALS['cat'][]=array('name'=>$n,'label'=>$p['label']); }
include $argv[1].'/inc/block-patterns.php';
echo json_encode(array('patterns'=>$GLOBALS['pat'],'categories'=>$GLOBALS['cat']), JSON_PRETTY_PRINT|JSON_UNESCAPED_SLASHES);
