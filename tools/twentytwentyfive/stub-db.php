<?php
class Fake_WPDB extends wpdb {
	public function __construct() {
		$this->dbuser = 'x'; $this->ready = true; $this->is_mysql = true;
		$this->set_prefix( 'wp_' );
		$this->charset = 'utf8mb4'; $this->collate = '';
	}
	public function db_connect( $allow_bail = true ) { return true; }
	public function check_connection( $allow_bail = true ) { return true; }
	public function query( $query ) { $this->last_query = $query; $this->last_result = array(); $this->num_rows = 0; $GLOBALS['fake_queries'][] = $query; return 0; }
	public function get_results( $query = null, $output = OBJECT ) { if ( $query ) { $this->query( $query ); } return array(); }
	public function get_var( $query = null, $x = 0, $y = 0 ) { if ( $query ) { $this->query( $query ); } if ( $query && strpos( $query, 'siteurl' ) !== false ) { return 'http://example.test'; } return null; }
	public function get_row( $query = null, $output = OBJECT, $y = 0 ) { if ( $query ) { $this->query( $query ); } return null; }
	public function get_col( $query = null, $x = 0 ) { if ( $query ) { $this->query( $query ); } return array(); }
	public function _real_escape( $data ) { return is_scalar( $data ) ? addslashes( (string) $data ) : ''; }
	public function db_version() { return '8.0'; }
	public function db_server_info() { return '8.0'; }
	public function has_cap( $c ) { return true; }
	public function get_charset_collate() { return ''; }
	public function determine_charset( $c, $col ) { return array( 'charset' => $c, 'collate' => $col ); }
	public function init_charset() {}
	public function set_charset( $dbh, $charset = null, $collate = null ) {}
	public function set_sql_mode( $modes = array() ) {}
	public function close() { return true; }
	public function check_database_version() {}
	public function get_table_charset( $table ) { return 'utf8mb4'; }
	public function get_col_charset( $table, $column ) { return 'utf8mb4'; }
	public function strip_invalid_text_for_column( $table, $column, $value ) { return $value; }
}
$GLOBALS['wpdb'] = new Fake_WPDB();
