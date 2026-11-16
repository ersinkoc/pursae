package server

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"wpgo/internal/cli"
	"wpgo/internal/store"
)

// registerMediaCLI registers the media, menu and import commands. See cli.go for the environment (cliEnv) and conventions.
//
// Nouns: media, menu (+ menu item, menu location), import (WXR).
func registerMediaCLI(r *cli.Registry, e *cliEnv) {
	registerMediaLibraryCLI(r, e)
	registerMenuCLI(r, e)
	registerImportCLI(r, e)
}

// mediaListFields are the default columns of `media list`.
var mediaListFields = []string{"ID", "title", "file", "mime_type", "post_id", "date"}

// mediaGetFields are the default fields of `media get`.
var mediaGetFields = []string{"ID", "title", "slug", "file", "url", "mime_type", "file_size", "width", "height", "alt", "caption",
	"description", "post_id", "author", "date", "modified", "sizes"}

const mediaUploadsNote = `Files are read and written in WPGO_UPLOADS_DIR: run the command where the server's uploads
volume is mounted (in Docker: docker exec <container> pursae ...).`

func registerMediaLibraryCLI(r *cli.Registry, e *cliEnv) {
	r.Noun("media", "Import, list, update, delete and regenerate media library items.")

	r.Add(cli.Command{
		Path:  "media list",
		Short: "List media library items.",
		Long: `Lists attachments of the media library, newest first.

Media items live in their own table with their own ID sequence: a media ID and a post ID can be
the same number and still name different things (unlike WordPress, where attachments are posts).

Fields: ID, title, slug, file, url, mime_type, file_size, width, height, alt, caption,
description, post_id, author, date, modified.`,
		Flags: []cli.Flag{
			{Name: "mime_type", Usage: "Only these MIME types or type prefixes, comma-separated (image, video, application/pdf …)."},
			{Name: "s", Usage: "Search title, caption, description, alt text and file name."},
			{Name: "post_id", Usage: "Only items attached to this post; 0 lists unattached items."},
			{Name: "author", Usage: "Only items uploaded by this user (ID, login or e-mail)."},
			{Name: "year", Usage: "Only items uploaded in this year."},
			{Name: "monthnum", Usage: "Only items uploaded in this month (1-12)."},
			{Name: "orderby", Usage: "Sort field.", Default: "date", Values: []string{"date", "title", "menu_order", "id", "modified"}},
			{Name: "order", Usage: "Sort direction.", Default: "desc", Values: []string{"asc", "desc", "ASC", "DESC"}},
			{Name: "per_page", Usage: "Items per page; 0 lists all.", Default: "0"},
			{Name: "paged", Usage: "Page number (with --per_page).", Default: "1"},
		},
		Examples: []string{
			"media list",
			"media list --mime_type=image --fields=ID,title,url",
			"media list --post_id=0 --format=ids",
		},
		Run: func(c *cli.Ctx) error {
			ctx := c.Context
			s, err := e.Server(ctx)
			if err != nil {
				return err
			}
			q := store.AttachmentQuery{Search: c.Flag("s"), MimePrefix: c.List("mime_type"), OrderBy: c.Flag("orderby"), Order: c.Flag("order")}
			if q.Year, err = c.Int("year", 0); err != nil {
				return err
			}
			if q.Month, err = c.Int("monthnum", 0); err != nil {
				return err
			}
			if q.PerPage, err = c.Int("per_page", 0); err != nil {
				return err
			}
			if q.Page, err = c.Int("paged", 1); err != nil {
				return err
			}
			if c.Has("post_id") {
				pid, err := c.Int64("post_id", 0)
				if err != nil {
					return err
				}
				q.PostID = &pid
			}
			if ref := c.Flag("author"); ref != "" {
				u, err := e.User(ctx, ref)
				if err != nil {
					return err
				}
				q.AuthorID = u.ID
			}
			list, total, err := s.store.QueryAttachments(ctx, q)
			if err != nil {
				return err
			}
			if c.Format == "count" && q.PerPage > 0 {
				c.Println(total)
				return nil
			}
			rows := make([]cli.Row, 0, len(list))
			for _, a := range list {
				rows = append(rows, s.cliMediaRow(ctx, a, false))
			}
			return c.Print(rows, cli.View{Fields: mediaListFields, ID: "ID"})
		},
	})

	r.Add(cli.Command{
		Path:     "media get",
		Short:    "Show one media library item.",
		Long:     "Prints the attachment's fields, its URL and its generated sizes (sizes: name, file, width, height, mime_type, file_size).",
		Args:     []cli.Arg{{Name: "id", Usage: "Media ID."}},
		Examples: []string{"media get 12", "media get 12 --field=url", "media get 12 --format=json"},
		Run: func(c *cli.Ctx) error {
			ctx := c.Context
			id, err := c.ArgInt64(0)
			if err != nil {
				return err
			}
			s, err := e.Server(ctx)
			if err != nil {
				return err
			}
			a, err := cliGetAttachment(ctx, s, id)
			if err != nil {
				return err
			}
			return c.PrintItem(s.cliMediaRow(ctx, a, true), mediaGetFields)
		},
	})

	r.Add(cli.Command{
		Path:  "media import",
		Short: "Import files or URLs into the media library.",
		Long: `Stores each file (a local path or an http(s) URL) in the uploads directory, generates the image
sizes and inserts the attachment, the same path the WordPress importer and the upload screen use.
URLs are fetched with the site's SSRF guard (public addresses only).

` + mediaUploadsNote + `

--title, --caption, --alt and --desc apply to every imported file. --featured_image sets the
(last) imported item as the featured image of --post_id.`,
		Args: []cli.Arg{{Name: "file", Usage: "Local file path or http(s) URL.", Repeat: true}},
		Flags: []cli.Flag{
			{Name: "post_id", Usage: "Attach the files to this post."},
			{Name: "title", Usage: "Attachment title (default: the file name)."},
			{Name: "caption", Usage: "Attachment caption."},
			{Name: "alt", Usage: "Alternative text."},
			{Name: "desc", Usage: "Attachment description."},
			{Name: "featured_image", Bool: true, Usage: "Make the imported item the featured image of --post_id."},
			{Name: "porcelain", Bool: true, Usage: "Print only the new attachment IDs."},
		},
		Examples: []string{
			"media import ~/photos/beach.jpg --title='Beach' --alt='Sunset at the beach'",
			"media import https://example.com/logo.png --post_id=12 --featured_image",
			"media import *.jpg --porcelain",
		},
		Run: func(c *cli.Ctx) error { return cliMediaImport(c, e) },
	})

	r.Add(cli.Command{
		Path:  "media update",
		Short: "Update the texts, parent or meta of a media item.",
		Long: `Changes only the given fields, like the Edit Media screen, then purges the pages that show the
item. --meta merges a JSON object into the item's meta (a null value removes a key).`,
		Args: []cli.Arg{{Name: "id", Usage: "Media ID.", Repeat: true}},
		Flags: []cli.Flag{
			{Name: "title", Usage: "Attachment title."},
			{Name: "caption", Usage: "Attachment caption."},
			{Name: "alt", Usage: "Alternative text."},
			{Name: "desc", Usage: "Attachment description."},
			{Name: "slug", Usage: "Attachment slug."},
			{Name: "post_id", Usage: "Attach to this post; 0 detaches."},
			{Name: "menu_order", Usage: "Menu order."},
			{Name: "meta", Usage: "JSON object merged into the item's meta."},
		},
		Examples: []string{
			"media update 12 --alt='Company logo'",
			"media update 12 13 --post_id=0",
			`media update 12 --meta='{"credit":"Jane Doe"}'`,
		},
		Run: func(c *cli.Ctx) error { return cliMediaUpdate(c, e) },
	})

	r.Add(cli.Command{
		Path:  "media delete",
		Short: "Delete media items and their files permanently.",
		Long: `Removes the attachment, its file, generated sizes, original and editor backups (and Pursae CDN
copies). Media has no trash, so this cannot be undone: it asks for confirmation (--yes skips it).

` + mediaUploadsNote,
		Args:     []cli.Arg{{Name: "id", Usage: "Media ID.", Repeat: true}},
		Examples: []string{"media delete 12", "media delete 12 13 14 --yes"},
		Run: func(c *cli.Ctx) error {
			ctx := c.Context
			ids, err := c.ArgIDs(0)
			if err != nil {
				return err
			}
			s, err := e.Server(ctx)
			if err != nil {
				return err
			}
			list := make([]*store.Attachment, 0, len(ids))
			for _, id := range ids {
				a, err := cliGetAttachment(ctx, s, id)
				if err != nil {
					return err
				}
				list = append(list, a)
			}
			if err := c.Confirm("Delete %d media item(s) and their files permanently?", len(list)); err != nil {
				return err
			}
			failed := 0
			for _, a := range list {
				s.deleteAttachmentFiles(a)
				if err := s.store.DeleteAttachment(ctx, a.ID); err != nil {
					c.Warning("Failed deleting media %d: %v", a.ID, err)
					failed++
					continue
				}
				s.purgeAttachment(a)
				c.Log("Deleted media %d (%s).", a.ID, a.FilePath)
			}
			if failed > 0 {
				return cli.Errorf("failed deleting %d of %d media item(s)", failed, len(list))
			}
			c.Success("Deleted %d media item(s).", len(list))
			return nil
		},
	})

	r.Add(cli.Command{
		Path:  "media regenerate",
		Short: "Regenerate the image sizes of media items.",
		Long: `Rebuilds the generated sizes (thumbnail, medium, large …) of image attachments from their
original files, e.g. after changing the sizes or the output format in Settings › Media. Without
IDs every image in the library is processed (asks for confirmation; --yes skips it).

The run is synchronous with per-item progress. It refuses to start while the server is running
a "Regenerate all" job (pass --force when that state is stale). Non-local and non-image files
are skipped.

` + mediaUploadsNote,
		Args: []cli.Arg{{Name: "id", Usage: "Media IDs (default: all images).", Optional: true, Repeat: true}},
		Flags: []cli.Flag{
			{Name: "force", Bool: true, Usage: "Run even though a server-side regenerate job is marked as running."},
		},
		Examples: []string{"media regenerate 12 13", "media regenerate --yes"},
		Run:      func(c *cli.Ctx) error { return cliMediaRegenerate(c, e) },
	})
}

// cliGetAttachment loads an attachment, with a WP-CLI style error when it does not exist.
func cliGetAttachment(ctx context.Context, s *Server, id int64) (*store.Attachment, error) {
	a, err := s.store.GetAttachment(ctx, id)
	if errors.Is(err, store.ErrNotFound) {
		return nil, cli.Errorf("invalid media ID: %d", id)
	}
	return a, err
}

// cliMediaRow is one attachment as WP-CLI style fields; sizes adds the generated sub-sizes.
func (s *Server) cliMediaRow(ctx context.Context, a *store.Attachment, sizes bool) cli.Row {
	row := cli.Row{
		"ID":          a.ID,
		"title":       a.Title,
		"slug":        a.Slug,
		"file":        a.FilePath,
		"url":         s.uploadURL(ctx, a.FilePath),
		"mime_type":   a.Mime,
		"file_size":   a.FileSize,
		"width":       a.Width,
		"height":      a.Height,
		"alt":         a.Alt,
		"caption":     a.Caption,
		"description": a.Description,
		"post_id":     a.PostID,
		"author":      a.UploaderID,
		"menu_order":  a.MenuOrder,
		"date":        a.CreatedAt.In(s.loc()), // the site time zone, like post_date
		"modified":    a.UpdatedAt.In(s.loc()),
	}
	if sizes {
		list := make([]any, 0, len(a.Variants))
		for _, v := range a.Variants {
			list = append(list, map[string]any{"name": v.Name, "file": v.FilePath, "width": v.Width, "height": v.Height,
				"mime_type": v.Mime, "file_size": v.FileSize})
		}
		row["sizes"] = list
		row["meta"] = a.Meta
	}
	return row
}

// isRemoteMedia reports whether an import source is an http(s) URL rather than a local path.
func isRemoteMedia(src string) bool {
	l := strings.ToLower(src)
	return strings.HasPrefix(l, "http://") || strings.HasPrefix(l, "https://")
}

func cliMediaImport(c *cli.Ctx, e *cliEnv) error {
	ctx := c.Context
	if c.Bool("featured_image") && !c.Has("post_id") {
		return cli.Usagef("--featured_image needs --post_id")
	}
	postID, err := c.Int64("post_id", 0)
	if err != nil {
		return err
	}
	if postID < 0 {
		return cli.Usagef("--post_id must be a post ID")
	}
	s, err := e.Server(ctx)
	if err != nil {
		return err
	}
	actor, err := e.Actor(c)
	if err != nil {
		return err
	}
	var post *store.Post
	if postID > 0 {
		if post, err = s.store.GetPost(ctx, postID); err != nil {
			if errors.Is(err, store.ErrNotFound) {
				return cli.Errorf("invalid post ID: %d", postID)
			}
			return err
		}
	}
	if !c.Bool("porcelain") {
		c.Log("Importing into %s.", s.cfg.UploadsDir)
	}
	var imported []int64
	failed := 0
	for _, src := range c.Args {
		a, err := s.cliImportOne(ctx, src, actor.ID, postID)
		if err != nil {
			c.Warning("Unable to import %s: %v", src, err)
			failed++
			continue
		}
		changed := false
		for flag, dst := range map[string]*string{"title": &a.Title, "caption": &a.Caption, "alt": &a.Alt, "desc": &a.Description} {
			if v, ok := c.Lookup(flag); ok {
				*dst, changed = v, true
			}
		}
		if changed {
			if err := s.store.UpdateAttachment(ctx, a); err != nil {
				c.Warning("Imported %s as %d but could not save its texts: %v", src, a.ID, err)
			}
			s.purgeAttachment(a)
		}
		imported = append(imported, a.ID)
		if c.Bool("porcelain") {
			c.Println(a.ID)
		} else {
			c.Log("Imported %s as attachment ID %d.", src, a.ID)
		}
	}
	if post != nil && c.Bool("featured_image") && len(imported) > 0 {
		post.FeaturedMediaID = imported[len(imported)-1]
		if err := s.store.UpdatePost(ctx, post, actor.ID); err != nil {
			return cli.Errorf("could not set the featured image of post %d: %v", post.ID, err)
		}
		s.afterPostSave(ctx, post)
		if !c.Bool("porcelain") {
			c.Log("Set attachment %d as the featured image of post %d.", post.FeaturedMediaID, post.ID)
		}
	}
	if failed > 0 {
		return cli.Errorf("imported %d of %d item(s)", len(imported), len(c.Args))
	}
	if !c.Bool("porcelain") {
		c.Success("Imported %d item(s).", len(imported))
	}
	return nil
}

// cliImportOne stores one local file or URL as an attachment.
func (s *Server) cliImportOne(ctx context.Context, src string, uploaderID, postID int64) (*store.Attachment, error) {
	if isRemoteMedia(src) {
		return s.sideloadURL(ctx, src, uploaderID, postID)
	}
	st, err := os.Stat(src)
	if err != nil {
		return nil, err
	}
	if st.IsDir() {
		return nil, errors.New("is a directory")
	}
	f, err := os.Open(src)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return s.insertAttachmentFile(ctx, filepath.Base(src), f, uploaderID, postID)
}

func cliMediaUpdate(c *cli.Ctx, e *cliEnv) error {
	ctx := c.Context
	ids, err := c.ArgIDs(0)
	if err != nil {
		return err
	}
	var meta map[string]any
	if raw, ok := c.Lookup("meta"); ok {
		if err := json.Unmarshal([]byte(raw), &meta); err != nil || meta == nil {
			return cli.Usagef("--meta expects a JSON object")
		}
	}
	var postID, order int64
	if postID, err = c.Int64("post_id", 0); err != nil {
		return err
	}
	if order, err = c.Int64("menu_order", 0); err != nil {
		return err
	}
	s, err := e.Server(ctx)
	if err != nil {
		return err
	}
	if c.Has("post_id") && postID > 0 {
		if _, err := s.store.GetPost(ctx, postID); err != nil {
			return cli.Errorf("invalid post ID: %d", postID)
		}
	}
	for _, id := range ids {
		a, err := cliGetAttachment(ctx, s, id)
		if err != nil {
			return err
		}
		for flag, dst := range map[string]*string{"title": &a.Title, "caption": &a.Caption, "alt": &a.Alt, "desc": &a.Description} {
			if v, ok := c.Lookup(flag); ok {
				*dst = v
			}
		}
		if v, ok := c.Lookup("slug"); ok {
			a.Slug = v
		}
		if c.Has("post_id") {
			a.PostID = postID
		}
		if c.Has("menu_order") {
			a.MenuOrder = int(order)
		}
		if err := s.store.UpdateAttachment(ctx, a); err != nil {
			return err
		}
		if meta != nil {
			if err := s.store.SetAttachmentMeta(ctx, a.ID, meta); err != nil {
				return err
			}
		}
		s.purgeAttachment(a)
		c.Success("Updated media %d.", a.ID)
	}
	return nil
}

func cliMediaRegenerate(c *cli.Ctx, e *cliEnv) error {
	ctx := c.Context
	ids, err := c.ArgIDs(0)
	if err != nil {
		return err
	}
	s, err := e.Server(ctx)
	if err != nil {
		return err
	}
	if j := s.regenerateJobState(); j.Running && !c.Bool("force") {
		return cli.Errorf("a \"Regenerate all\" job is running on the server (%d of %d done); wait for it or stop it in the Media Library (--force runs anyway)", j.Done+j.Skipped+j.Failed, j.Total)
	}
	if len(ids) == 0 {
		if ids, err = s.store.ImageAttachmentIDs(ctx); err != nil {
			return err
		}
		if len(ids) == 0 {
			c.Success("No images found.")
			return nil
		}
		if err := c.Confirm("Regenerate the sizes of all %d images?", len(ids)); err != nil {
			return err
		}
	}
	c.Log("Found %d image(s) to regenerate.", len(ids))
	done, skipped, failed := 0, 0, 0
	for i, id := range ids {
		prefix := strconv.Itoa(i+1) + "/" + strconv.Itoa(len(ids))
		a, err := s.store.GetAttachment(ctx, id)
		if err != nil {
			c.Warning("%s No media item with ID %d.", prefix, id)
			failed++
			continue
		}
		switch err := s.regenerateAttachment(ctx, a); {
		case err == nil:
			done++
			c.Log("%s Regenerated sizes for %q (ID %d).", prefix, a.Title, a.ID)
		case errors.Is(err, errRegenerateSkipped):
			skipped++
			c.Log("%s Skipped %q (ID %d): not a local image.", prefix, a.Title, a.ID)
		default:
			failed++
			c.Warning("%s Failed %q (ID %d): %v", prefix, a.Title, a.ID, err)
		}
	}
	if failed > 0 {
		return cli.Errorf("regenerated %d of %d image(s) (%d skipped, %d failed)", done, len(ids), skipped, failed)
	}
	c.Success("Regenerated %d of %d image(s) (%d skipped).", done, len(ids), skipped)
	return nil
}
