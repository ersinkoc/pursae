package server

import (
	"bytes"
	"context"
	"errors"
	"html/template"
	"image"
	"image/color"
	"image/draw"
	_ "image/gif" // decoders for image.DecodeConfig / image.Decode
	_ "image/jpeg"
	_ "image/png"
	"io"
	"mime/multipart"
	"net/http"
	"strconv"
	"strings"
	"unicode"

	_ "golang.org/x/image/webp"

	"wpgo/internal/media"
	"wpgo/internal/store"
)

// Member profile photos (WP7). A member uploads a photo on the forum account page without Media
// Library access: the file is sniffed (JPEG, PNG, WebP, GIF; its first frame), size- and
// dimension-checked before decoding, re-encoded as a centre-cropped square of at most 512 px and
// stored as an attachment the member owns, so it sits in the Media Library and gets the usual sizes.
// users.meta avatar_id (the core avatar field: Profile screen, avatarFor) points at it;
// users.meta forum_avatar_id remembers which attachment the forum made, so a new upload or "Remove
// photo" deletes the old file but never an image an administrator picked from the library.
// Without a photo the forum shows an initials avatar (no Gravatar).

const (
	forumUMetaAvatar    = "forum_avatar_id" // users.meta: the attachment the forum uploaded
	forumAvatarMaxBytes = 2 << 20
	forumAvatarMaxDim   = 6000
	forumAvatarMinDim   = 16
	forumAvatarSize     = 512
)

var (
	errForumAvatarMissing = errors.New("Please choose a photo to upload.")
	errForumAvatarType    = errors.New("Please choose a JPEG, PNG, WebP or GIF image.")
	errForumAvatarBytes   = errors.New("The photo is larger than 2 MB.")
	errForumAvatarDims    = errors.New("The photo is too large: at most 6000 × 6000 pixels.")
	errForumAvatarSmall   = errors.New("The photo is too small: at least 16 × 16 pixels.")
	errForumAvatarBroken  = errors.New("The photo could not be read.")
)

// forumAvatarSem limits concurrent avatar decodes (a 6000×6000 image decodes to ~144 MB).
var forumAvatarSem = make(chan struct{}, 2)

// forumAvatarMimes are the accepted sniffed types and the image package's format name for each.
var forumAvatarMimes = map[string]string{"image/jpeg": "jpeg", "image/png": "png", "image/webp": "webp", "image/gif": "gif"}

// forumSaveAvatarUpload stores an uploaded photo (a multipart file) as u's avatar.
func (s *Server) forumSaveAvatarUpload(ctx context.Context, u *store.User, fh *multipart.FileHeader) (*store.Attachment, error) {
	if fh == nil {
		return nil, errForumAvatarMissing
	}
	if fh.Size > forumAvatarMaxBytes {
		return nil, errForumAvatarBytes
	}
	f, err := fh.Open()
	if err != nil {
		return nil, errForumAvatarBroken
	}
	defer f.Close()
	return s.forumSaveAvatar(ctx, u, f)
}

// forumSaveAvatar reads a photo from r and stores it as u's avatar (see the file comment); the
// previous forum-uploaded photo is deleted.
func (s *Server) forumSaveAvatar(ctx context.Context, u *store.User, r io.Reader) (*store.Attachment, error) {
	if u == nil || u.ID <= 0 {
		return nil, errForumNotFound
	}
	data, err := io.ReadAll(io.LimitReader(r, forumAvatarMaxBytes+1))
	if err != nil {
		return nil, errForumAvatarBroken
	}
	if len(data) == 0 {
		return nil, errForumAvatarMissing
	}
	if len(data) > forumAvatarMaxBytes {
		return nil, errForumAvatarBytes
	}
	out, ext, err := forumAvatarProcess(data)
	if err != nil {
		return nil, err
	}
	a, err := s.insertAttachmentFile(ctx, "avatar-"+strconv.FormatInt(u.ID, 10)+"."+ext, bytes.NewReader(out), u.ID, 0)
	if err != nil {
		return nil, err
	}
	fresh, err := s.store.UserByID(ctx, u.ID)
	if err != nil {
		return nil, err
	}
	old := forumMetaInt64(fresh.Meta, forumUMetaAvatar)
	if err := s.forumSetUserMeta(ctx, u.ID, map[string]any{"avatar_id": a.ID, forumUMetaAvatar: a.ID}); err != nil {
		return nil, err
	}
	if old > 0 && old != a.ID {
		s.forumDeleteOwnAttachment(ctx, old, u.ID)
	}
	s.purgeAll(ctx) // avatars appear in bylines and comment lists across the site
	return a, nil
}

// forumAvatarProcess validates and re-encodes a photo: sniffed type, dimensions checked from the
// header before decoding (decompression bomb guard), EXIF orientation applied, centre-cropped square,
// scaled to at most 512 px; JPEG unless the image has transparency (PNG).
func forumAvatarProcess(data []byte) ([]byte, string, error) {
	format, ok := forumAvatarMimes[http.DetectContentType(data)]
	if !ok {
		return nil, "", errForumAvatarType
	}
	cfg, cfgFormat, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || cfgFormat != format {
		return nil, "", errForumAvatarBroken
	}
	if cfg.Width > forumAvatarMaxDim || cfg.Height > forumAvatarMaxDim {
		return nil, "", errForumAvatarDims
	}
	if cfg.Width < forumAvatarMinDim || cfg.Height < forumAvatarMinDim {
		return nil, "", errForumAvatarSmall
	}
	forumAvatarSem <- struct{}{}
	defer func() { <-forumAvatarSem }()
	img, _, err := image.Decode(bytes.NewReader(data)) // a GIF decodes to its first frame
	if err != nil {
		return nil, "", errForumAvatarBroken
	}
	if format == "jpeg" {
		img = media.Orient(img, media.JPEGOrientation(bytes.NewReader(data)))
	}
	if format == "gif" && img.Bounds() != image.Rect(0, 0, cfg.Width, cfg.Height) {
		canvas := image.NewRGBA(image.Rect(0, 0, cfg.Width, cfg.Height))
		// The first GIF frame can cover only part of its logical screen.
		// Start with the global background unless that index is transparent.
		if palette, ok := cfg.ColorModel.(color.Palette); ok && len(data) > 11 && int(data[11]) < len(palette) {
			background := palette[data[11]]
			if frame, ok := img.(*image.Paletted); ok && int(data[11]) < len(frame.Palette) {
				if _, _, _, alpha := frame.Palette[data[11]].RGBA(); alpha == 0 {
					background = color.Transparent
				}
			}
			draw.Draw(canvas, canvas.Bounds(), image.NewUniform(background), image.Point{}, draw.Src)
		}
		draw.Draw(canvas, img.Bounds(), img, img.Bounds().Min, draw.Over)
		img = canvas
	}
	b := img.Bounds()
	side := min(b.Dx(), b.Dy())
	crop := image.Rect(0, 0, side, side).Add(image.Pt(b.Min.X+(b.Dx()-side)/2, b.Min.Y+(b.Dy()-side)/2))
	if sub, ok := img.(interface {
		SubImage(image.Rectangle) image.Image
	}); ok {
		img = sub.SubImage(crop)
	} else {
		return nil, "", errForumAvatarBroken
	}
	n := min(side, forumAvatarSize)
	sq := media.Resize(img, n, n)
	outFormat, ext := "jpeg", "jpg"
	if op, ok := sq.(interface{ Opaque() bool }); ok && !op.Opaque() {
		outFormat, ext = "png", "png"
	}
	var buf bytes.Buffer
	if err := media.EncodeTo(&buf, sq, outFormat); err != nil {
		return nil, "", err
	}
	return buf.Bytes(), ext, nil
}

// forumDeleteOwnAttachment deletes attachment id (files and row) when ownerID uploaded it.
func (s *Server) forumDeleteOwnAttachment(ctx context.Context, id, ownerID int64) {
	a, err := s.store.GetAttachment(ctx, id)
	if err != nil || a == nil || a.UploaderID != ownerID {
		return
	}
	s.deleteAttachmentFiles(a)
	if s.store.DeleteAttachment(ctx, id) == nil {
		s.purgeAttachment(a)
	}
}

// forumRemoveAvatar clears u's avatar (the initials avatar shows again); the photo the forum
// uploaded is deleted, an image picked from the Media Library stays in the library.
func (s *Server) forumRemoveAvatar(ctx context.Context, u *store.User) error {
	if u == nil || u.ID <= 0 {
		return errForumNotFound
	}
	fresh, err := s.store.UserByID(ctx, u.ID)
	if err != nil {
		return err
	}
	own := forumMetaInt64(fresh.Meta, forumUMetaAvatar)
	if err := s.forumSetUserMeta(ctx, u.ID, nil, "avatar_id", forumUMetaAvatar); err != nil {
		return err
	}
	if own > 0 {
		s.forumDeleteOwnAttachment(ctx, own, u.ID)
	}
	s.purgeAll(ctx)
	return nil
}

// Initials avatar ---------------------------------------------------------------------------------------

// forumInitials are up to two letters of name (first and last word), else of login; "?" when
// neither has a letter or digit.
func forumInitials(name, login string) string {
	pick := func(s string) string {
		var initials []string
		for _, w := range strings.FieldsFunc(s, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) && !unicode.IsMark(r) }) {
			var initial strings.Builder
			for _, r := range w {
				if initial.Len() == 0 {
					if unicode.IsLetter(r) || unicode.IsDigit(r) {
						initial.WriteRune(unicode.ToUpper(r))
					}
				} else if unicode.IsMark(r) {
					initial.WriteRune(r)
				} else {
					break
				}
			}
			if initial.Len() > 0 {
				initials = append(initials, initial.String())
			}
		}
		if len(initials) == 0 {
			return ""
		}
		out := initials[0]
		if len(initials) > 1 {
			out += initials[len(initials)-1]
		}
		return out
	}
	if s := pick(name); s != "" {
		return s
	}
	if s := pick(login); s != "" {
		return s
	}
	return "?"
}

// forumHue is a stable hue for a user id (golden-angle steps spread neighbours apart).
func forumHue(id int64) int {
	if id < 0 {
		id = -id
	}
	return int((id * 137) % 360)
}

// forumAvatarHTML is a member's avatar at size px: the photo (<img class="pf-avatar">) or the
// initials avatar (<span class="pf-avatar pf-avatar--initials" style="--pf-av:hsl(…)">AB</span>);
// a nil author (a deleted account) is an empty circle.
func forumAvatarHTML(a *forumAuthor, size int) template.HTML {
	if size <= 0 || size > 512 {
		size = 40
	}
	n := strconv.Itoa(size)
	sz := `--pf-av-size:` + n + `px`
	if a == nil {
		return template.HTML(`<span class="pf-avatar pf-avatar--empty" style="` + sz + `" aria-hidden="true"></span>`)
	}
	if a.HasPhoto && a.AvatarURL != "" {
		return template.HTML(`<img class="pf-avatar avatar" src="` + template.HTMLEscapeString(a.AvatarURL) + `" alt="" width="` + n +
			`" height="` + n + `" loading="lazy" decoding="async" />`)
	}
	initials := a.Initials
	if initials == "" {
		initials = forumInitials(a.DisplayName, a.Login)
	}
	return template.HTML(`<span class="pf-avatar pf-avatar--initials" style="--pf-av:hsl(` + strconv.Itoa(forumHue(a.ID)) +
		` 45% 42%);` + sz + `" aria-hidden="true">` + template.HTMLEscapeString(initials) + `</span>`)
}
