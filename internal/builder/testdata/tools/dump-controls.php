<?php
use Elementor\Plugin;
Plugin::$instance->breakpoints->set_responsive_control_duplication_mode( 'off' );
\Elementor\Core\Frontend\Performance::set_use_style_controls( true );
$keep = ['name','type','default','selectors','selectors_dictionary','unit_selectors_dictionary','condition','conditions','responsive','is_responsive','prefix_class','global','groupType','groupPrefix','fields','frontend_available','render_type','parent','device_args','min_affected_device','desktop_default','return_value','options','dynamic','classes_dictionary','control_type','assets'];
function wpgo_clean( $c, $keep ) {
	$o = [];
	foreach ( $c as $k => $v ) {
		if ( in_array( $k, $keep, true ) || preg_match( '/_default$/', $k ) ) {
			if ( 'options' === $k && ! in_array( $c['type'], ['select','choose','switcher'], true ) ) { continue; }
			if ( 'options' === $k ) { $v = array_map( 'strval', array_keys( (array) $v ) ); }
			if ( 'dynamic' === $k ) { $v = ! empty( $v['active'] ); }
			if ( 'fields' === $k ) {
				$f = [];
				foreach ( $v as $fk => $fv ) { $f[] = wpgo_clean( $fv, $keep ); }
				$v = $f;
			}
			$o[ $k ] = $v;
		}
	}
	return $o;
}
function wpgo_stack( $st, $keep ) {
	$out = [];
	foreach ( $st['controls'] ?? [] as $c ) { $out[] = wpgo_clean( $c, $keep ); }
	foreach ( $st['style_controls'] ?? [] as $c ) { $x = wpgo_clean( $c, $keep ); $x['_style'] = true; $out[] = $x; }
	return $out;
}
$res = [ 'stacks' => [], 'widgets' => [], 'types' => [] ];
foreach ( Plugin::$instance->controls_manager->get_controls() as $name => $obj ) {
	$t = [ 'class' => get_class( $obj ), 'data' => $obj instanceof \Elementor\Base_Data_Control ];
	$parents = []; $p = get_class( $obj ); while ( $p = get_parent_class( $p ) ) { $parents[] = $p; }
	$t['parents'] = $parents;
	if ( $t['data'] ) { $t['default'] = $obj->get_default_value(); }
	$s = $obj->get_settings();
	if ( isset( $s['global'] ) ) { $t['global'] = $s['global']; }
	$res['types'][ $name ] = $t;
}
foreach ( Plugin::$instance->widgets_manager->get_widget_types() as $name => $w ) {
	$st = $w->get_stack( false );
	if ( empty( $st['controls'] ) && empty( $st['style_controls'] ) ) { continue; }
	$common = '';
	if ( ! $w instanceof \Elementor\Widget_Common_Base ) {
		$common = $w->has_widget_inner_wrapper() ? 'common' : 'common-optimized';
	}
	$res['widgets'][ $name ] = [ 'common' => $common, 'inner_wrapper' => $w->has_widget_inner_wrapper(), 'controls' => wpgo_stack( $st, $keep ) ];
}
foreach ( [ 'section', 'column', 'container' ] as $el ) {
	$e = Plugin::$instance->elements_manager->get_element_types( $el );
	$res['stacks'][ $el ] = wpgo_stack( $e->get_stack(), $keep );
}
$pm = \Elementor\Core\Settings\Manager::get_settings_managers( 'page' );
$res['stacks']['page'] = wpgo_stack( $pm->get_model( 5 )->get_stack(), $keep );
$res['stacks']['kit'] = wpgo_stack( $pm->get_model( (int) get_option( 'elementor_active_kit' ) )->get_stack(), $keep );
$res['stacks']['page_doc'] = wpgo_stack( Plugin::$instance->documents->get( 5 )->get_stack(), $keep );
file_put_contents( __DIR__ . '/controls.json', wp_json_encode( $res ) );
echo "ok ", count( $res['widgets'] ), " widgets\n";
