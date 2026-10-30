package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"path"
	"time"

	"github.com/gin-gonic/gin"

	"wpgo/internal/media"
)

// "Remote storage" inside the media modal (wp.media). enqueueMedia (wp_enqueue_media) calls
// gocdnEnqueueMedia, which adds a router tab next to "Upload files" and "Media Library" to every
// media frame (Select, Post and the frames built on them: featured image, Customizer, widgets,
// ACF-Go, …) for administrators while GoCDN is active and configured. The tab browses the bucket
// (folders, thumbnails, paging) through action=remote-list and imports the clicked object
// through action=remote-import (copy or link, as on Media › Remote Storage); the new attachment
// (or the one already imported from that key) is then added to the library and selected, and the
// frame switches back to the library so "Select" / "Insert" uses it right away.

// gocdnRemoteJSON is the listing the modal tab shows.
func (s *Server) gocdnRemoteJSON(c *gin.Context) {
	st := s.gocdnLoad()
	v := s.gocdnRemoteList(c, st)
	keys := make([]string, 0, len(v.Objects))
	for _, o := range v.Objects {
		keys = append(keys, o.Key)
	}
	ids := map[string]int64{}
	if rows, err := s.store.Pool.Query(c, `SELECT object_key, media_id FROM gocdn_items WHERE object_key = ANY($1) AND bucket = $2 AND media_id IS NOT NULL`, keys, st.Bucket); err == nil {
		for rows.Next() {
			var k string
			var id int64
			if rows.Scan(&k, &id) == nil {
				ids[k] = id
			}
		}
		rows.Close()
	}
	type obj struct {
		Key      string `json:"key"`
		Name     string `json:"name"`
		Size     string `json:"size"`
		Modified string `json:"modified"`
		Preview  string `json:"preview,omitempty"`
		Image    bool   `json:"image"`
		Allowed  bool   `json:"allowed"`
		MediaID  int64  `json:"media_id,omitempty"`
	}
	type folder struct {
		Name   string `json:"name"`
		Prefix string `json:"prefix"`
	}
	out := struct {
		Prefix  string   `json:"prefix"`
		Parent  *string  `json:"parent"`
		Folders []folder `json:"folders"`
		Objects []obj    `json:"objects"`
		Next    string   `json:"next"`
		Error   string   `json:"error,omitempty"`
	}{Prefix: v.Prefix, Folders: []folder{}, Objects: []obj{}, Next: v.NextToken, Error: v.Err}
	if v.Prefix != "" {
		parent := path.Dir(path.Clean(v.Prefix)) + "/"
		if parent == "./" {
			parent = ""
		}
		out.Parent = &parent
	}
	for _, f := range v.Folders {
		out.Folders = append(out.Folders, folder{f.Name, f.Prefix})
	}
	for _, o := range v.Objects {
		out.Objects = append(out.Objects, obj{Key: o.Key, Name: o.Name, Size: o.Size, Modified: o.Modified, Preview: o.Preview, Image: o.Image,
			Allowed: media.MimeFor(path.Base(o.Key)) != "", MediaID: ids[o.Key]})
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, out)
}

// gocdnRemoteImportJSON imports one object for the modal and answers the attachment ID.
func (s *Server) gocdnRemoteImportJSON(c *gin.Context) {
	if !s.checkNonce(c, gocdnNonce) {
		c.JSON(http.StatusForbidden, gin.H{"error": "The link you followed has expired."})
		return
	}
	u := currentUser(c)
	if !u.Can("upload_files", nil) {
		c.JSON(http.StatusForbidden, gin.H{"error": "Sorry, you are not allowed to upload files."})
		return
	}
	key := c.PostForm("key")
	st := s.gocdnLoad()
	cl, err := s.gocdnClient(st)
	if err != nil || key == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "The storage is not configured or no file was given."})
		return
	}
	if media.MimeFor(path.Base(key)) == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Sorry, you are not allowed to upload this file type."})
		return
	}
	// Already imported (copy or link) from this key: select that attachment.
	var existing int64
	_ = s.store.Pool.QueryRow(c, `SELECT media_id FROM gocdn_items WHERE object_key = $1 AND bucket = $2 AND media_id IS NOT NULL ORDER BY id LIMIT 1`, key, st.Bucket).Scan(&existing)
	if existing > 0 {
		c.JSON(http.StatusOK, gin.H{"id": existing, "existing": true})
		return
	}
	ctx, cancel := context.WithTimeout(c, 5*time.Minute)
	defer cancel()
	var id int64
	if c.PostForm("mode") == "link" {
		id, err = s.gocdnImportLink(ctx, st, cl, key, u.ID)
		if errors.Is(err, errAlreadyInLibrary) {
			c.JSON(http.StatusOK, gin.H{"id": id, "existing": true})
			return
		}
	} else {
		id, err = s.gocdnImportCopy(ctx, cl, key, u.ID)
	}
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	s.purgeAll(ctx)
	c.JSON(http.StatusOK, gin.H{"id": id})
}

// gocdnEnqueueMedia adds the modal tab's script after media-views.
func (s *Server) gocdnEnqueueMedia(c *gin.Context, a *assets) {
	u := currentUser(c)
	if !s.gocdnOn() || u == nil || !u.Can("manage_options", nil) {
		return
	}
	st := s.gocdnLoad()
	if !s.gocdnConfigured(st) {
		return
	}
	cfg, _ := json.Marshal(map[string]any{"list": "/wp-admin/gocdn?action=remote-list", "import": "/wp-admin/gocdn?action=remote-import",
		"nonce": s.nonce(c, gocdnNonce), "prefix": st.Prefix, "bucket": st.Bucket})
	a.AddInline("media-views", "window.gocdnModal = "+string(cfg)+";\n"+gocdnModalJS)
}

const gocdnModalJS = `( function( $, media ) {
	if ( ! media || ! media.view || ! media.view.MediaFrame || ! window.gocdnModal ) { return; }
	var cfg = window.gocdnModal, Select = media.view.MediaFrame.Select;
	function esc( s ) { return String( s == null ? '' : s ).replace( /[&<>"']/g, function( c ) { return { '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[ c ]; } ); }
	var View = media.View.extend( {
		className: 'gocdn-remote-browser',
		events: { 'click .gocdn-folder': 'openFolder', 'click .gocdn-up': 'openFolder', 'click .gocdn-more': 'more', 'click .gocdn-object': 'pick' },
		initialize: function() { this.prefix = cfg.prefix; this.token = ''; this.load( false ); },
		load: function( append ) {
			var self = this, url = cfg.list + '&prefix=' + encodeURIComponent( this.prefix ) + ( append && this.token ? '&token=' + encodeURIComponent( this.token ) : '' );
			if ( ! append ) { this.$el.html( '<p class="gocdn-status"><span class="spinner is-active" style="float:none"></span> Loading…</p>' ); }
			$.getJSON( url ).done( function( d ) { self.render( d, append ); } ).fail( function() { self.$el.html( '<p class="gocdn-status">The bucket cannot be listed.</p>' ); } );
		},
		render: function( d, append ) {
			if ( ! d ) { return this; }
			this.token = d.next || '';
			var html = '';
			if ( ! append ) {
				html += '<div class="gocdn-bar"><strong translate="no">' + esc( cfg.bucket ) + '/' + esc( d.prefix ) + '</strong>' + ( d.parent !== null && d.parent !== undefined ? ' <button type="button" class="button-link gocdn-up" data-prefix="' + esc( d.parent ) + '">↑ Up</button>' : '' ) +
					' <label class="gocdn-mode">Import as <select><option value="copy">copy into the Media Library</option><option value="link">link (stays in the bucket)</option></select></label></div>';
				if ( d.error ) { html += '<p class="gocdn-status">' + esc( d.error ) + '</p>'; }
				html += '<ul class="gocdn-grid">';
			}
			var items = '';
			( d.folders || [] ).forEach( function( f ) {
				items += '<li><button type="button" class="gocdn-folder" data-prefix="' + esc( f.prefix ) + '"><span class="dashicons dashicons-portfolio"></span><span class="gocdn-name" translate="no">' + esc( f.name ) + '/</span></button></li>';
			} );
			( d.objects || [] ).forEach( function( o ) {
				var thumb = o.image && o.preview ? '<img src="' + esc( o.preview ) + '" alt="" loading="lazy">' : '<span class="dashicons dashicons-media-default"></span>';
				items += '<li><button type="button" class="gocdn-object' + ( o.media_id ? ' in-library' : '' ) + '"' + ( o.allowed ? '' : ' disabled' ) + ' data-key="' + esc( o.key ) + '" title="' + esc( o.name ) + ' — ' + esc( o.size ) + '">' + thumb +
					'<span class="gocdn-name" translate="no">' + esc( o.name ) + '</span>' + ( o.media_id ? '<span class="gocdn-badge">In library</span>' : '' ) + '</button></li>';
			} );
			if ( append ) { this.$( '.gocdn-grid' ).append( items ); this.$( '.gocdn-more' ).remove(); }
			else { html += items + '</ul>'; this.$el.html( html ); }
			if ( ! items && ! append && ! d.error ) { this.$el.append( '<p class="gocdn-status">This folder is empty.</p>' ); }
			if ( this.token ) { this.$el.append( '<p><button type="button" class="button gocdn-more">Load more</button></p>' ); }
			return this;
		},
		openFolder: function( e ) { this.prefix = $( e.currentTarget ).data( 'prefix' ) || ''; this.token = ''; this.load( false ); },
		more: function() { this.load( true ); },
		pick: function( e ) {
			var btn = $( e.currentTarget ), frame = this.controller, self = this;
			if ( btn.prop( 'disabled' ) ) { return; }
			btn.prop( 'disabled', true ).addClass( 'is-busy' );
			$.post( cfg['import'], { _wpnonce: cfg.nonce, key: btn.data( 'key' ), mode: this.$( '.gocdn-mode select' ).val() || 'copy' } ).done( function( r ) {
				var model = media.attachment( r.id );
				model.fetch().done( function() {
					var state = frame.state(), lib = state.get( 'library' ), sel = state.get( 'selection' );
					if ( lib ) { lib.add( model, { at: 0 } ); }
					if ( sel ) { if ( ! sel.multiple ) { sel.reset(); } sel.add( model ); }
					frame.content.mode( 'browse' );
				} );
			} ).fail( function( x ) {
				var msg = ( x.responseJSON && x.responseJSON.error ) || 'The file could not be imported.';
				btn.prop( 'disabled', false ).removeClass( 'is-busy' );
				self.$( '.gocdn-bar' ).after( '<div class="notice notice-error inline"><p>' + esc( msg ) + '</p></div>' );
			} );
		}
	} );
	var browseRouter = Select.prototype.browseRouter, bindHandlers = Select.prototype.bindHandlers;
	Select.prototype.browseRouter = function( routerView ) {
		browseRouter.apply( this, arguments );
		routerView.set( { gocdn: { text: 'Remote storage', priority: 60 } } );
	};
	Select.prototype.bindHandlers = function() {
		bindHandlers.apply( this, arguments );
		this.on( 'content:render:gocdn', function() { this.content.set( new View( { controller: this } ) ); }, this );
	};
	$( function() {
		$( '<style>.gocdn-remote-browser{padding:12px 16px;overflow:auto;position:absolute;inset:0}.gocdn-bar{display:flex;gap:12px;align-items:center;flex-wrap:wrap;margin-bottom:10px}.gocdn-mode{margin-left:auto}' +
			'.gocdn-grid{display:grid;grid-template-columns:repeat(auto-fill,minmax(130px,1fr));gap:10px;margin:0}.gocdn-grid li{margin:0}' +
			'.gocdn-grid button{width:100%;height:150px;display:flex;flex-direction:column;align-items:center;justify-content:center;gap:6px;background:#f6f7f7;border:1px solid #dcdcde;border-radius:4px;cursor:pointer;padding:6px;overflow:hidden}' +
			'.gocdn-grid button:hover,.gocdn-grid button:focus{border-color:#2271b1;box-shadow:0 0 0 1px #2271b1}.gocdn-grid button[disabled]{opacity:.5;cursor:default}.gocdn-grid button.is-busy{opacity:.6;cursor:progress}' +
			'.gocdn-grid img{max-width:100%;max-height:100px;object-fit:contain}.gocdn-grid .dashicons{font-size:48px;width:48px;height:48px;color:#8c8f94}' +
			'.gocdn-name{font-size:12px;max-width:100%;overflow:hidden;text-overflow:ellipsis;white-space:nowrap}.gocdn-badge{font-size:10px;background:#00a32a;color:#fff;border-radius:9px;padding:0 6px}</style>' ).appendTo( 'head' );
	} );
} )( jQuery, window.wp && wp.media );`
