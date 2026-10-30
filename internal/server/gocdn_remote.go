package server

import (
	"bytes"
	"context"
	"errors"
	"image"
	"io"
	"net/http"
	"os"
	"path"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"wpgo/internal/gocdn"
	"wpgo/internal/media"
	"wpgo/internal/store"
	"wpgo/internal/wp"
)

// Remote Storage (Media › Remote Storage): the bucket's folders and files, with thumbnails, and
// "Import" in two modes: "copy" downloads the object into the Media Library like an upload (sizes
// are generated, and it is offloaded again under the library's own key), "link" registers the
// object as an attachment that lives only in the bucket (no local file, no generated sizes; its
// /wp-content/uploads URL is answered from the bucket and it is selectable in the media picker).

// gocdnRemotePreview is a small thumbnail of a bucket image for the browser.
func (s *Server) gocdnRemotePreview(c *gin.Context) {
	key := c.Query("key")
	st := s.gocdnLoad()
	cl, err := s.gocdnClient(st)
	if err != nil || key == "" || !gocdnImageExt[strings.ToLower(pathExt(key))] {
		c.Status(http.StatusNotFound)
		return
	}
	ctx, cancel := context.WithTimeout(c, 30*time.Second)
	defer cancel()
	rc, obj, err := cl.Get(ctx, key)
	if err != nil {
		c.Status(http.StatusNotFound)
		return
	}
	defer rc.Close()
	if obj.Size > 20<<20 {
		c.Status(http.StatusRequestEntityTooLarge)
		return
	}
	data, err := io.ReadAll(io.LimitReader(rc, 20<<20))
	if err != nil {
		c.Status(http.StatusBadGateway)
		return
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || (gocdn.Limits{}).CheckSource(cfg) != nil {
		c.Status(http.StatusUnsupportedMediaType)
		return
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		c.Status(http.StatusUnsupportedMediaType)
		return
	}
	img = gocdn.Transform(img, gocdn.ImageParams{W: 160, H: 160})
	format := "jpeg"
	if gocdn.HasAlpha(img) {
		format = "png"
	}
	var buf bytes.Buffer
	if err := gocdn.Encode(&buf, img, format, 75); err != nil {
		c.Status(http.StatusInternalServerError)
		return
	}
	c.Header("Cache-Control", "private, max-age=3600")
	c.Data(http.StatusOK, gocdn.ContentType(format), buf.Bytes())
}

// gocdnImport imports bucket objects into the Media Library.
func (s *Server) gocdnImport(c *gin.Context, st gocdnSettings, keys []string, mode string) (int, []string, error) {
	u := currentUser(c)
	if !u.Can("upload_files", nil) {
		return 0, nil, errors.New("Sorry, you are not allowed to upload files.")
	}
	if len(keys) == 0 {
		return 0, nil, errors.New("Select the files to import.")
	}
	cl, err := s.gocdnClient(st)
	if err != nil {
		return 0, nil, err
	}
	ctx, cancel := context.WithTimeout(c, 5*time.Minute)
	defer cancel()
	var skipped []string
	n := 0
	for _, key := range keys {
		name := path.Base(key)
		if media.MimeFor(name) == "" {
			skipped = append(skipped, name+" (file type not allowed)")
			continue
		}
		var err error
		if mode == "link" {
			_, err = s.gocdnImportLink(ctx, st, cl, key, u.ID)
		} else {
			_, err = s.gocdnImportCopy(ctx, cl, key, u.ID)
		}
		if err != nil {
			skipped = append(skipped, name+" ("+err.Error()+")")
			continue
		}
		n++
	}
	if n > 0 {
		s.purgeAll(ctx)
	}
	return n, skipped, nil
}

func (s *Server) gocdnImportCopy(ctx context.Context, cl *gocdn.Client, key string, uid int64) (int64, error) {
	rc, _, err := cl.Get(ctx, key)
	if err != nil {
		return 0, errors.New(gocdnExplain(err))
	}
	defer rc.Close()
	a, err := s.insertAttachmentFile(ctx, path.Base(key), rc, uid, 0)
	if err != nil {
		return 0, err
	}
	return a.ID, nil
}

// gocdnImportLink registers an object as a remote-only attachment. Its uploads path is the key
// without the object prefix (or gocdn-remote/<key> for keys outside it).
func (s *Server) gocdnImportLink(ctx context.Context, st gocdnSettings, cl *gocdn.Client, key string, uid int64) (int64, error) {
	rel, ok := strings.CutPrefix(key, st.Prefix)
	if !ok || st.Prefix == "" && strings.HasPrefix(key, "gocdn-cache/") {
		rel = "gocdn-remote/" + key
	}
	local := s.gocdnLocalPath(rel)
	if local == "" {
		return 0, errors.New("invalid key")
	}
	if it := s.gocdnItemByPath(ctx, rel); it != nil && it.MediaID != 0 {
		return it.MediaID, errAlreadyInLibrary
	}
	if _, err := os.Stat(local); err == nil {
		return 0, errors.New("a local file has the same path; use Copy")
	}
	obj, err := cl.Head(ctx, key)
	if err != nil {
		return 0, errors.New(gocdnExplain(err))
	}
	name := path.Base(key)
	mime := media.MimeFor(name)
	title := strings.TrimSuffix(name, path.Ext(name))
	a := &store.Attachment{UploaderID: uid, FilePath: rel, Mime: mime, FileSize: obj.Size, Title: title, Slug: wp.SanitizeTitle(title),
		Meta: map[string]any{"filesize": obj.Size, "gocdn_remote": true}}
	if strings.HasPrefix(mime, "image/") {
		if head, err := cl.GetRange(ctx, key, 0, 512<<10-1); err == nil {
			if cfg, _, err := image.DecodeConfig(bytes.NewReader(head)); err == nil {
				a.Width, a.Height = cfg.Width, cfg.Height
			}
		}
	}
	if err := s.store.InsertAttachment(ctx, a); err != nil {
		return 0, err
	}
	return a.ID, s.gocdnSaveItem(ctx, &gocdnItem{MediaID: a.ID, Path: rel, Provider: st.Provider, Region: gocdnRegion(st), Bucket: st.Bucket,
		Key: key, Size: obj.Size, Mime: mime, LocalRemoved: true, Endpoint: gocdnEndpoint(st)})
}

var errAlreadyInLibrary = errors.New("already in the library")
