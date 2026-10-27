package server

import (
	"html/template"
	"net/http"
	"net/mail"
	"sort"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"wpgo/internal/auth"
	"wpgo/internal/store"
)

// profile.php and user-edit.php share one screen: Target is the user being edited.
type profileData struct {
	Target         *store.User
	Self           bool
	Action         string
	Nonce          string
	DisplayOptions []string
	Avatar96       string
	ShowToolbar    bool
	SyntaxOff      bool // "Disable syntax highlighting when editing code"
	Colors         []adminColor
	Color          string // admin_color
	OtherSessions  int
	AvatarID       int64
	CanUpload      bool
	// Role editing (user-edit.php, for users who can promote_users)
	CanPromote bool
	Roles      []roleChoice
	CapRows    []capRow
}

type roleChoice struct {
	Slug, Name     string
	Primary, Extra bool
}

type capRow struct {
	Cap, Effect string
}

func (s *Server) profileView(c *gin.Context, target *store.User) profileData {
	u := currentUser(c)
	d := profileData{Target: target, Self: target.ID == u.ID, Nonce: s.nonce(c, "update-user_"+itoa64(target.ID)),
		Avatar96: s.avatarURL(c, target.Email, 96), ShowToolbar: true, Action: "/wp-admin/profile"}
	if !d.Self {
		d.Action = "/wp-admin/user-edit?user_id=" + itoa64(target.ID)
	}
	if v, ok := target.Meta["show_admin_bar_front"].(bool); ok {
		d.ShowToolbar = v
	}
	d.SyntaxOff, d.Colors, d.Color = !syntaxHighlighting(target), adminColors, adminColorFor(target).Slug
	seen := map[string]bool{}
	add := func(v string) {
		v = strings.TrimSpace(v)
		if v != "" && !seen[v] {
			seen[v] = true
			d.DisplayOptions = append(d.DisplayOptions, v)
		}
	}
	first, last := target.Profile["first_name"], target.Profile["last_name"]
	add(target.Profile["nickname"])
	add(target.Login)
	add(first)
	add(last)
	if first != "" && last != "" {
		add(first + " " + last)
		add(last + " " + first)
	}
	add(target.DisplayName)
	if d.Self {
		sess := currentSession(c)
		_ = s.store.Pool.QueryRow(c, `SELECT count(*) FROM sessions WHERE user_id = $1 AND id <> $2 AND expires_at > now()`, target.ID, sess.ID).Scan(&d.OtherSessions)
	}
	if !d.Self && u.Can("promote_users", nil) {
		d.CanPromote = true
		roles, _ := s.store.Roles(c)
		for _, r := range roles {
			rc := roleChoice{Slug: r.Slug, Name: r.Name}
			for i, have := range target.Roles {
				if have == r.Slug {
					rc.Primary, rc.Extra = i == 0, i > 0
				}
			}
			d.Roles = append(d.Roles, rc)
		}
		exc, _ := s.store.UserCapExceptions(c, target.ID)
		for k, v := range exc {
			d.CapRows = append(d.CapRows, capRow{k, v})
		}
		sort.Slice(d.CapRows, func(i, j int) bool { return d.CapRows[i].Cap < d.CapRows[j].Cap })
	}
	s.avatarPicker(c, target, &d)
	return d
}

func (s *Server) profile(c *gin.Context) {
	u := currentUser(c)
	s.adminPage(c, "profile", "Profile", "profile-php", "admin/profile", s.profileView(c, u))
}

func (s *Server) profileSave(c *gin.Context) {
	s.saveUser(c, currentUser(c))
}

// userEdit is user-edit.php.
func (s *Server) userEdit(c *gin.Context) {
	u := currentUser(c)
	id, _ := strconv.ParseInt(c.Query("user_id"), 10, 64)
	if id == u.ID {
		c.Redirect(http.StatusFound, "/wp-admin/profile")
		return
	}
	target, err := s.store.UserByID(c, id)
	if err != nil {
		c.String(http.StatusNotFound, "Invalid user ID.")
		return
	}
	if !u.Can("edit_users", nil) {
		c.String(http.StatusForbidden, "Sorry, you are not allowed to edit this user.")
		return
	}
	if c.Request.Method == http.MethodPost {
		s.saveUser(c, target)
		return
	}
	var notices []notice
	if c.Query("updated") == "1" {
		notices = append(notices, notice{Type: "success", Message: "<strong>User updated.</strong> <a href=\"/wp-admin/users\">&larr; Go to Users</a>", Dismissible: true})
	}
	s.adminPage(c, "users", "Edit User "+target.Login, "user-edit-php", "admin/profile", s.profileView(c, target), notices...)
}

// saveUser is edit_user(): profile fields, password, avatar and, for other users, roles and capabilities.
func (s *Server) saveUser(c *gin.Context, target *store.User) {
	u := currentUser(c)
	self := target.ID == u.ID
	if !s.checkNonce(c, "update-user_"+itoa64(target.ID)) {
		s.denyNonce(c)
		return
	}
	back := "/wp-admin/profile?updated=1"
	if !self {
		back = "/wp-admin/user-edit?user_id=" + itoa64(target.ID) + "&updated=1"
	}
	if self && c.PostForm("action") == "destroy-sessions" {
		_ = s.auth.DestroyOthers(c, u.ID, currentSession(c).ID)
		c.Redirect(http.StatusFound, back)
		return
	}
	var errs []string
	nickname := strings.TrimSpace(c.PostForm("nickname"))
	email := strings.TrimSpace(c.PostForm("email"))
	if nickname == "" {
		errs = append(errs, "<strong>Error:</strong> Please enter a nickname.")
	}
	if _, err := mail.ParseAddress(email); err != nil || email == "" {
		errs = append(errs, "<strong>Error:</strong> Please enter an email address.")
	}
	pass1, pass2 := c.PostForm("pass1"), c.PostForm("pass2")
	if pass1 != "" && pass2 != "" && pass1 != pass2 {
		errs = append(errs, "<strong>Error:</strong> Passwords do not match. Please enter the same password in both password fields.")
	}
	if len(errs) > 0 {
		var ns []notice
		for _, e := range errs {
			ns = append(ns, notice{Type: "error", Message: template.HTML(e)})
		}
		screen, title, body := "profile", "Profile", "profile-php"
		if !self {
			screen, title, body = "users", "Edit User "+target.Login, "user-edit-php"
		}
		s.adminPage(c, screen, title, body, "admin/profile", s.profileView(c, target), ns...)
		return
	}
	target.Profile["first_name"] = strings.TrimSpace(c.PostForm("first_name"))
	target.Profile["last_name"] = strings.TrimSpace(c.PostForm("last_name"))
	target.Profile["nickname"] = nickname
	target.Profile["description"] = strings.TrimSpace(c.PostForm("description"))
	if dn := strings.TrimSpace(c.PostForm("display_name")); dn != "" {
		target.DisplayName = dn
	}
	target.Email = email
	target.URL = strings.TrimSpace(c.PostForm("url"))
	if err := s.store.UpdateUserProfile(c, target); err != nil {
		c.String(http.StatusInternalServerError, err.Error())
		return
	}
	_ = s.setUserMeta(c, target, "show_admin_bar_front", c.PostForm("admin_bar_front") == "1")
	if c.PostForm("syntax_highlighting") == "false" {
		_ = s.setUserMeta(c, target, "syntax_highlighting", "false")
	} else {
		_ = s.setUserMeta(c, target, "syntax_highlighting", "true")
	}
	for _, sc := range adminColors {
		if sc.Slug == c.PostForm("admin_color") {
			_ = s.setUserMeta(c, target, "admin_color", sc.Slug)
		}
	}
	s.saveAvatar(c, target)
	if pass1 != "" {
		if hash, err := auth.HashPassword(pass1); err == nil {
			_ = s.store.SetUserPassword(c, target.ID, hash)
		}
	}
	if !self && u.Can("promote_users", nil) {
		if primary, ok := c.GetPostForm("role"); ok {
			_ = s.store.SetUserRoles(c, target.ID, primary, c.PostFormArray("extra_roles[]"))
		}
		exc := map[string]string{}
		caps := c.PostFormArray("cap_name[]")
		effects := c.PostFormArray("cap_effect[]")
		for i, cp := range caps {
			cp = strings.TrimSpace(cp)
			if cp != "" && i < len(effects) {
				exc[cp] = effects[i]
			}
		}
		_ = s.store.SetUserCapExceptions(c, target.ID, exc)
	}
	s.purgeAll(c) // names and avatars appear in bylines and comment lists across the site
	c.Redirect(http.StatusFound, back)
}

// avatarPicker wires the profile picture field to the media modal (wp.media), the same frame used for
// featured images: pick from the library or upload. WordPress Go never calls Gravatar.
func (s *Server) avatarPicker(c *gin.Context, u *store.User, d *profileData) {
	if id, ok := u.Meta["avatar_id"].(float64); ok {
		d.AvatarID = int64(id)
	}
	d.CanUpload = currentUser(c).Can("upload_files", nil)
	if !d.CanUpload {
		return
	}
	s.enqueueMedia(c, 0)
	s.pageAssets(c).AddInline("media-editor", `jQuery( function( $ ) {
	var frame, $img = $( '.user-profile-picture img.avatar' ), fallback = `+strconvQuote(defaultAvatar)+`;
	$( '#wpgo-avatar-choose' ).on( 'click', function( event ) {
		event.preventDefault();
		if ( ! frame ) {
			frame = wp.media( {
				title: 'Choose Profile Picture',
				library: { type: 'image' },
				button: { text: 'Use as profile picture' },
				multiple: false
			} );
			frame.on( 'open', function() {
				var id = parseInt( $( '#avatar_id' ).val(), 10 ), selection = frame.state().get( 'selection' );
				if ( id > 0 ) {
					var attachment = wp.media.attachment( id );
					attachment.fetch();
					selection.reset( [ attachment ] );
				}
			} );
			frame.on( 'select', function() {
				var a = frame.state().get( 'selection' ).first().toJSON(),
					size = a.sizes && ( a.sizes.thumbnail || a.sizes.medium || a.sizes.full ),
					src = size ? size.url : a.url;
				$( '#avatar_id' ).val( a.id );
				$img.attr( { src: src, srcset: src + ' 2x' } );
				$( '#wpgo-avatar-choose' ).text( 'Change Profile Picture' );
				$( '#wpgo-avatar-remove' ).prop( 'hidden', false );
				wp.a11y.speak( 'Profile picture selected. Save your profile to keep it.' );
			} );
		}
		frame.open();
	} );
	$( '#wpgo-avatar-remove' ).on( 'click', function( event ) {
		event.preventDefault();
		$( '#avatar_id' ).val( '0' );
		$img.attr( { src: fallback, srcset: fallback + ' 2x' } );
		$( '#wpgo-avatar-choose' ).text( 'Choose Profile Picture' );
		$( this ).prop( 'hidden', true );
	} );
} );`)
}

// saveAvatar stores the chosen attachment as the user's profile picture (users.meta.avatar_id).
func (s *Server) saveAvatar(c *gin.Context, u *store.User) {
	raw, ok := c.GetPostForm("avatar_id")
	if !ok || !currentUser(c).Can("upload_files", nil) {
		return
	}
	id, _ := strconv.ParseInt(raw, 10, 64)
	if id > 0 {
		a, err := s.store.GetAttachment(c, id)
		if err != nil || !strings.HasPrefix(a.Mime, "image/") {
			return
		}
	} else {
		id = 0
	}
	_ = s.setUserMeta(c, u, "avatar_id", id)
}
