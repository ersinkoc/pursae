package server

import (
	"context"
	"html"
	"log"
	"os"
	"path/filepath"

	"github.com/gin-gonic/gin"

	"wpgo/internal/store"
)

// The admin entry points of Pursae Web Builder, the way WordPress shows a page builder's:
//
//   - the editors' "Edit with Pursae Web Builder" button (block editor header toolbar, classic
//     editor under the title), which saves a new or changed post first;
//   - the Pages/Posts list row action and the "Pursae Web Builder" post state of a builder page;
//   - the toolbar node on the site ("Edit with Pursae Web Builder" on a builder page).
//
// Each one shows only while the plugin is on, for the builder's post types and for users who may
// edit the post. The post state and the toolbar node mark posts with a web_builder_docs row.

// wbCanBuild reports whether u may open p in the builder now.
func (s *Server) wbCanBuild(u *store.User, p *store.Post) bool {
	return p != nil && u != nil && s.wbOn() && wbEnabledType(p.Type) && p.Status != "trash" && u.Can("edit_post", p)
}

// wbDocIDs reports which of the posts have a builder document.
func (s *Server) wbDocIDs(ctx context.Context, ids []int64) map[int64]bool {
	out := map[int64]bool{}
	if len(ids) == 0 {
		return out
	}
	rows, err := s.store.Pool.Query(ctx, `SELECT post_id FROM web_builder_docs WHERE post_id = ANY($1)`, ids)
	if err != nil {
		log.Printf("web builder: docs of %d posts: %v", len(ids), err)
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		if rows.Scan(&id) == nil {
			out[id] = true
		}
	}
	return out
}

// wbRowAction is the list table's "Edit with Pursae Web Builder" link of p ("" when not offered).
func (s *Server) wbRowAction(c *gin.Context, p *store.Post) string {
	if !s.wbCanBuild(currentUser(c), p) {
		return ""
	}
	return wbEditURL(p.ID)
}

// wbClassicRedirectField is the hidden field the classic editor's button adds before it saves a
// new post: the save then continues to the builder.
const wbClassicRedirectField = "web_builder_redirect"

// wbClassicRedirect is the builder URL a classic editor save continues to, when its button asked.
func (s *Server) wbClassicRedirect(c *gin.Context, p *store.Post) (string, bool) {
	if c.PostForm(wbClassicRedirectField) == "" || !s.wbCanBuild(currentUser(c), p) {
		return "", false
	}
	return wbEditURL(p.ID), true
}

// wbEditorButton adds "Edit with Pursae Web Builder" to the block editor's header toolbar or below
// the classic editor's title. A new or changed post is saved first (an untitled one gets a
// placeholder title), then the builder opens.
func (s *Server) wbEditorButton(c *gin.Context, p *store.Post, block bool) {
	if !s.wbCanBuild(currentUser(c), p) {
		return
	}
	url := strconvQuote(wbEditURL(p.ID))
	label := strconvQuote(s.__(c, "Edit with Pursae Web Builder"))
	untitled := strconvQuote("Pursae Web Builder #" + itoa64(p.ID))
	a := s.pageAssets(c)
	if block {
		a.AddInline("wp-edit-post", `( function() {
	var url = `+url+`;
	function go() {
		var ed = wp.data.select( 'core/editor' );
		if ( ed.isEditedPostNew() || ed.isEditedPostDirty() ) {
			if ( ! ed.getEditedPostAttribute( 'title' ) ) { wp.data.dispatch( 'core/editor' ).editPost( { title: `+untitled+` } ); }
			wp.data.dispatch( 'core/editor' ).savePost().then( function() { location.href = url; } );
		} else {
			location.href = url;
		}
	}
	function mount() {
		var bar = document.querySelector( '.editor-header__toolbar, .edit-post-header-toolbar' );
		if ( ! bar ) { return false; }
		if ( document.getElementById( 'web-builder-switch-mode' ) ) { return true; }
		var b = document.createElement( 'button' );
		b.id = 'web-builder-switch-mode';
		b.type = 'button';
		b.className = 'components-button is-primary';
		b.style.marginLeft = '12px';
		b.textContent = `+label+`;
		b.addEventListener( 'click', go );
		bar.appendChild( b );
		return true;
	}
	wp.domReady( function() {
		var tries = 0;
		var t = setInterval( function() { if ( mount() || ++tries > 100 ) { clearInterval( t ); } }, 200 );
		wp.data.subscribe( mount );
	} );
} )();`)
		return
	}
	a.AddInline("common", `jQuery( function( $ ) {
	var $t = $( '#titlediv' );
	if ( ! $t.length || $( '#web-builder-switch-mode' ).length ) { return; }
	var $b = $( '<div id="web-builder-switch-mode" style="margin:12px 0"><a class="button button-primary button-hero"></a></div>' );
	$b.find( 'a' ).attr( 'href', `+url+` ).text( `+label+` );
	$t.after( $b );
	$b.find( 'a' ).on( 'click', function( e ) {
		if ( $( '#original_post_status' ).val() === 'auto-draft' ) {
			e.preventDefault();
			if ( ! $( '#title' ).val() ) { $( '#title' ).val( `+untitled+` ); }
			$( '#post' ).append( '<input type="hidden" name="`+wbClassicRedirectField+`" value="1">' );
			$( '#save-post' ).trigger( 'click' );
		}
	} );
} );`)
}

// wbAdminBarNode is the site toolbar's "Edit with Pursae Web Builder" on a builder page: the
// singular post of the view has a builder document and the user may edit it.
func (s *Server) wbAdminBarNode(c *gin.Context, q *siteQuery, u *store.User) string {
	if q == nil || q.Post == nil || !s.wbCanBuild(u, q.Post) || !s.wbDocIDs(c.Request.Context(), []int64{q.Post.ID})[q.Post.ID] {
		return ""
	}
	return `<li role='group' id='wp-admin-bar-web_builder_edit_page'><a class='ab-item' role="menuitem" href='` +
		html.EscapeString(wbEditURL(q.Post.ID)) + `'><span class="ab-icon dashicons-layout" aria-hidden="true"></span><span class="ab-label">` +
		html.EscapeString(s.__(c, "Edit with Pursae Web Builder")) + `</span></a></li>`
}

// wbMoveLegacyUploads renames, once, the builder's uploads folder of before the Pursae rename
// (uploads_legacy.go) to uploads/web-builder: the local Google Fonts the builder serves live there.
func wbMoveLegacyUploads(uploadsDir string) {
	if uploadsDir == "" {
		return
	}
	old, cur := filepath.Join(uploadsDir, gweLegacyUploadsDir), filepath.Join(uploadsDir, gweUploadsDir)
	if _, err := os.Stat(old); err != nil {
		return
	}
	if _, err := os.Stat(cur); err == nil {
		return
	}
	if err := os.Rename(old, cur); err != nil {
		log.Printf("web builder: moving %s: %v", old, err)
	}
}
