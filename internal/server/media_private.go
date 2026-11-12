package server

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"wpgo/internal/store"
)

// Private files --------------------------------------------------------------------------------
//
// An attachment is private when its files live under uploads/private/ (the folder is the flag: no
// lookup is needed to protect a path). Nothing under that folder is ever served as a static file:
// /wp-content/uploads/private/… answers only a request that carries a valid, unexpired signature
// (mediaPrivateURL), the image service and the URL rewriting leave it alone, and in the bucket the
// objects are written without a public ACL under a key that cannot be guessed, and handed out only
// through short-lived presigned URLs. Pursae Commerce delivers downloadable files that are private
// after its own permission checks (gooDownload).

const (
	mediaPrivateDir = "private"
	// mediaPrivateLinkTTL is the life of a link handed to a customer (a redirect that is followed at once).
	mediaPrivateLinkTTL = 5 * time.Minute
)

// mediaIsPrivateRel reports whether an uploads path is inside the protected folder. The check is
// deliberately wide (any case, either slash): file systems that fold case must not open a side door.
func mediaIsPrivateRel(rel string) bool {
	clean := strings.TrimPrefix(path.Clean("/"+strings.ReplaceAll(rel, "\\", "/")), "/")
	first, _, _ := strings.Cut(clean, "/")
	return strings.EqualFold(strings.TrimRight(first, ". "), mediaPrivateDir)
}

func mediaIsPrivate(a *store.Attachment) bool { return a != nil && mediaIsPrivateRel(a.FilePath) }

// mediaPrivateKey is the HMAC key of the signed links, derived from the server secret.
func (s *Server) mediaPrivateKey() []byte {
	secret := s.cfg.Secret
	if secret == "" {
		secret = s.settings.String("auth_secret")
	}
	k := sha256.Sum256([]byte("wpgo-private-file|" + secret))
	return k[:]
}

func (s *Server) mediaPrivateSig(rel string, exp int64, download bool) string {
	m := hmac.New(sha256.New, s.mediaPrivateKey())
	m.Write([]byte(rel + "\n" + strconv.FormatInt(exp, 10) + "\n" + strconv.FormatBool(download)))
	return hex.EncodeToString(m.Sum(nil)[:16])
}

// mediaPrivateURL is a link to a private file that works until exp.
func (s *Server) mediaPrivateURL(ctx context.Context, rel string, exp time.Time, download bool) string {
	e := exp.Unix()
	u := s.links(ctx).HomeURL("/wp-content/uploads/"+rel) + "?e=" + strconv.FormatInt(e, 10) + "&s=" + s.mediaPrivateSig(rel, e, download)
	if download {
		u += "&dl=1"
	}
	return u
}

// mediaPrivateAdminURL is the link the admin screens show for a private file: valid for one to two
// hours, and the same for every request within an hour so browsers can cache thumbnails.
func (s *Server) mediaPrivateAdminURL(ctx context.Context, rel string) string {
	exp := time.Now().Truncate(time.Hour).Add(2 * time.Hour)
	return s.mediaPrivateURL(ctx, rel, exp, false)
}

// mediaPrivateViewer reports whether the request's user may be handed links to private files.
func (s *Server) mediaPrivateViewer(ctx context.Context) bool {
	c, ok := ctx.(*gin.Context)
	if !ok || c == nil {
		return false
	}
	u := currentUser(c)
	if u == nil {
		u = restCurrent(c)
	}
	return u != nil && u.Can("upload_files", nil)
}

// mediaPrivateServe answers /wp-content/uploads/private/…: the file for a valid signed link, 403 otherwise.
func (s *Server) mediaPrivateServe(c *gin.Context, rel string) {
	c.Header("Cache-Control", "private, no-store")
	c.Header("X-Robots-Tag", "noindex, nofollow")
	q := c.Request.URL.Query()
	exp, _ := strconv.ParseInt(q.Get("e"), 10, 64)
	download := q.Get("dl") == "1"
	if exp < time.Now().Unix() || !hmac.Equal([]byte(q.Get("s")), []byte(s.mediaPrivateSig(rel, exp, download))) {
		c.String(http.StatusForbidden, "This file is private, or the link has expired.\n")
		return
	}
	if !s.mediaPrivateDeliver(c, rel, download, false) {
		c.String(http.StatusNotFound, "File not found.\n")
	}
}

// mediaPrivateDeliver sends a private file to a request that was already authorised: the local
// file (or, with redirect, a short-lived signed link to it), or for a file that is only in the
// bucket a presigned URL that expires in minutes (the object itself has no public URL). It reports
// whether the file was found.
func (s *Server) mediaPrivateDeliver(c *gin.Context, rel string, download, redirect bool) bool {
	ctx := c.Request.Context()
	local := s.gocdnLocalPath(rel)
	if local == "" || !mediaIsPrivateRel(rel) {
		return false
	}
	c.Header("Cache-Control", "private, no-store")
	c.Header("X-Robots-Tag", "noindex, nofollow")
	if fi, err := os.Stat(local); err == nil && fi.Mode().IsRegular() {
		if redirect {
			c.Redirect(http.StatusFound, s.mediaPrivateURL(ctx, rel, time.Now().Add(mediaPrivateLinkTTL), download))
			return true
		}
		c.Header("Content-Security-Policy", "sandbox; default-src 'none'; img-src 'self' data:; style-src 'unsafe-inline'; media-src 'self'")
		c.Header("X-Content-Type-Options", "nosniff")
		if download {
			c.Header("Content-Disposition", `attachment; filename="`+strings.NewReplacer(`"`, "", "\r", "", "\n", "").Replace(path.Base(rel))+`"`)
		}
		c.File(local)
		return true
	}
	if !s.gocdnTracking() {
		return false
	}
	it := s.gocdnItemByPath(ctx, rel)
	if it == nil {
		return false
	}
	st := s.gocdnLoad()
	if !gocdnItemSSEC(st, it) {
		if cl, err := s.gocdnItemClient(st, it); err == nil {
			if u, err := cl.PresignGet(it.Key, mediaPrivateLinkTTL); err == nil {
				c.Redirect(http.StatusFound, u)
				return true
			}
		}
	}
	// SSE-C objects (and a storage that cannot presign) are streamed through the site.
	s.gocdnProxy(privateHeaders{c.Writer}, c.Request, st, it)
	return true
}

// privateHeaders keeps a proxied private object out of every cache, whatever the proxy sets.
type privateHeaders struct{ gin.ResponseWriter }

func (w privateHeaders) WriteHeader(code int) {
	w.Header().Set("Cache-Control", "private, no-store")
	w.ResponseWriter.WriteHeader(code)
}

func (w privateHeaders) Write(b []byte) (int, error) {
	if !w.ResponseWriter.Written() {
		w.Header().Set("Cache-Control", "private, no-store")
	}
	return w.ResponseWriter.Write(b)
}

// mediaPrivateRelOfURL maps a file URL to the private uploads path it stands for: a URL inside the
// protected folder, or the public URL a file had before it was made private (products keep the URL
// they were saved with). "" when the URL is not a private file of this site.
func (s *Server) mediaPrivateRelOfURL(ctx context.Context, file string) string {
	if i := strings.IndexAny(file, "?#"); i >= 0 {
		file = file[:i]
	}
	rel := ""
	home := strings.TrimRight(s.links(ctx).HomeURL("/"), "/")
	for _, prefix := range []string{home + "/wp-content/uploads/", "/wp-content/uploads/"} {
		if strings.HasPrefix(file, prefix) {
			rel = strings.TrimPrefix(file, prefix)
			break
		}
	}
	if rel == "" {
		return ""
	}
	if un, err := url.PathUnescape(rel); err == nil {
		rel = un
	}
	rel = strings.TrimPrefix(path.Clean("/"+rel), "/")
	if rel == "" || s.gocdnLocalPath(rel) == "" {
		return ""
	}
	if !mediaIsPrivateRel(rel) {
		if _, err := os.Stat(s.gocdnLocalPath(rel)); err == nil {
			return "" // still a public file
		}
		rel = mediaPrivateDir + "/" + rel
	}
	if _, err := os.Stat(s.gocdnLocalPath(rel)); err == nil {
		return rel
	}
	if s.gocdnTracking() && s.gocdnItemByPath(ctx, rel) != nil {
		return rel
	}
	return ""
}

// mediaPrivatePath is the path a file has in the other state.
func mediaPrivatePath(rel string, private bool) string {
	if private {
		if mediaIsPrivateRel(rel) {
			return rel
		}
		return mediaPrivateDir + "/" + rel
	}
	if !mediaIsPrivateRel(rel) {
		return rel
	}
	_, rest, _ := strings.Cut(rel, "/")
	return rest
}

// mediaSetPrivate moves an attachment's files into the protected folder (or back out of it) and
// updates everything that knows their paths. The old public URLs stop working at once: nothing is
// redirected, the bucket copies are deleted and uploaded again (privately, or publicly), and the
// CDN is told to forget them.
func (s *Server) mediaSetPrivate(ctx context.Context, a *store.Attachment, private bool) error {
	if a == nil || mediaIsPrivate(a) == private {
		return nil
	}
	mediaReplaceMu.Lock()
	defer mediaReplaceMu.Unlock()
	s.gocdnEnsureAttachmentLocal(ctx, a) // files that are only in the bucket come back first
	oldFiles := gocdnAttachmentFiles(a)
	if _, err := os.Stat(s.gocdnLocalPath(a.FilePath)); err != nil {
		return errors.New("The file is not on this server and could not be fetched from remote storage.")
	}
	to := func(p string) string { return mediaPrivatePath(p, private) }
	type move struct{ from, to string }
	var done []move
	undo := func() {
		for i := len(done) - 1; i >= 0; i-- {
			_ = os.Rename(done[i].to, done[i].from)
		}
	}
	for _, rel := range oldFiles {
		src, dst := s.gocdnLocalPath(rel), s.gocdnLocalPath(to(rel))
		if src == "" || dst == "" {
			undo()
			return errors.New("invalid attachment path")
		}
		if _, err := os.Stat(src); err != nil {
			continue // a size or an editor backup that is gone
		}
		if _, err := os.Stat(dst); err == nil {
			undo()
			return errors.New("A file with the same name already exists in the target folder.")
		}
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			undo()
			return err
		}
		if err := os.Rename(src, dst); err != nil {
			undo()
			return err
		}
		done = append(done, move{src, dst})
	}
	old := cloneAttachment(a)
	a.FilePath = to(a.FilePath)
	for i := range a.Variants {
		a.Variants[i].FilePath = to(a.Variants[i].FilePath)
	}
	if a.Meta == nil {
		a.Meta = map[string]any{}
	}
	if b, ok := a.Meta["backup_file"].(string); ok && b != "" {
		a.Meta["backup_file"] = to(b)
	}
	if raw, ok := a.Meta["backup_variants"].(string); ok && raw != "" {
		var vs []store.AttachmentVariant
		if json.Unmarshal([]byte(raw), &vs) == nil {
			for i := range vs {
				vs[i].FilePath = to(vs[i].FilePath)
			}
			out, _ := json.Marshal(vs)
			a.Meta["backup_variants"] = string(out)
		}
	}
	if list := metaStringList(a.Meta["edit_files"]); len(list) > 0 {
		for i := range list {
			list[i] = to(list[i])
		}
		a.Meta["edit_files"] = list
	}
	if err := s.store.ReplaceAttachmentFile(ctx, a); err != nil {
		undo()
		*a = *old
		return err
	}
	// Paths this attachment left earlier must not redirect to a file that is private now.
	_, _ = s.store.Pool.Exec(ctx, `DELETE FROM media_moved_files WHERE media_id = $1`, a.ID)
	s.gocdnDropVariants(oldFiles)
	s.afterMediaFilesChanged(ctx, a, append(oldFiles, gocdnAttachmentFiles(a)...))
	return nil
}

// mediaPrivateAction is the Media Library's "Make private" / "Make public" row action.
func (s *Server) mediaPrivateAction(c *gin.Context) {
	u := currentUser(c)
	id, _ := strconv.ParseInt(c.Query("media"), 10, 64)
	if !s.checkNonceValue(c, c.Query("_wpnonce"), "media-private_"+itoa64(id)) {
		s.denyNonce(c)
		return
	}
	back := "/wp-admin/upload?mode=list"
	a, err := s.store.GetAttachment(c, id)
	if err != nil || a == nil || (a.UploaderID != u.ID && !u.Can("edit_others_posts", nil)) {
		c.Redirect(http.StatusFound, back+"&gocdn-media-error="+url.QueryEscape("You are not allowed to change that file."))
		return
	}
	private := c.Query("action") == "make-private"
	if err := s.mediaSetPrivate(c.Request.Context(), a, private); err != nil {
		c.Redirect(http.StatusFound, back+"&gocdn-media-error="+url.QueryEscape(err.Error()))
		return
	}
	msg := "The file is public again."
	if private {
		msg = "The file is private now: its public URL no longer works."
	}
	c.Redirect(http.StatusFound, back+"&gocdn-media-msg="+url.QueryEscape(msg))
}
