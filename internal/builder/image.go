package builder

// Attachment images: wp_get_attachment_image (with srcset/sizes) and upstream's
// Group_Control_Image_Size::get_attachment_image_html.

import (
	"math"
	"strconv"
	"strings"
)

// Attachment is an image attachment's metadata (post + _wp_attachment_metadata).
type Attachment struct {
	ID          int64
	URL         string // full size URL
	Width       int
	Height      int
	Alt         string // _wp_attachment_image_alt
	Title       string
	Caption     string // post_excerpt
	Description string // post_content
	MimeType    string
	Sizes       []AttachmentSize // intermediate sizes in metadata order
}

// AttachmentSize is one intermediate image size.
type AttachmentSize struct {
	Name     string
	URL      string
	Width    int
	Height   int
	MimeType string
}

// coreImageSizes are WordPress' registered sizes (name -> max width, max height, crop).
var coreImageSizes = map[string][3]int{
	"thumbnail":    {150, 150, 1},
	"medium":       {300, 300, 0},
	"medium_large": {768, 0, 0},
	"large":        {1024, 1024, 0},
	"1536x1536":    {1536, 1536, 0},
	"2048x2048":    {2048, 2048, 0},
}

func (r *renderer) attachment(id int64) *Attachment {
	if id <= 0 || r.rc.Attachment == nil {
		return nil
	}
	return r.rc.Attachment(id)
}

func (a *Attachment) isImage() bool {
	if a == nil {
		return false
	}
	if a.MimeType != "" {
		return strings.HasPrefix(a.MimeType, "image/")
	}
	return true
}

func (a *Attachment) size(name string) *AttachmentSize {
	for i := range a.Sizes {
		if a.Sizes[i].Name == name {
			return &a.Sizes[i]
		}
	}
	return nil
}

// imageSrc mirrors wp_get_attachment_image_src (url, width, height).
func (a *Attachment) imageSrc(size string) (string, int, int, bool) {
	if a == nil {
		return "", 0, 0, false
	}
	if size == "" || size == "full" {
		return a.URL, a.Width, a.Height, a.URL != ""
	}
	if s := a.size(size); s != nil {
		return s.URL, s.Width, s.Height, s.URL != ""
	}
	w, h := a.Width, a.Height
	if dims, ok := coreImageSizes[size]; ok {
		w, h = constrainDimensions(a.Width, a.Height, dims[0], dims[1])
	}
	return a.URL, w, h, a.URL != ""
}

// constrainDimensions mirrors wp_constrain_dimensions.
func constrainDimensions(cw, ch, maxW, maxH int) (int, int) {
	if maxW == 0 && maxH == 0 {
		return cw, ch
	}
	if cw == 0 || ch == 0 {
		return cw, ch
	}
	wRatio, hRatio := 1.0, 1.0
	didW, didH := false, false
	if maxW > 0 && cw > maxW {
		wRatio = float64(maxW) / float64(cw)
		didW = true
	}
	if maxH > 0 && ch > maxH {
		hRatio = float64(maxH) / float64(ch)
		didH = true
	}
	smaller := math.Min(wRatio, hRatio)
	larger := math.Max(wRatio, hRatio)
	ratio := smaller
	if float64(int(math.Round(float64(cw)*larger))) > float64(maxW) || float64(int(math.Round(float64(ch)*larger))) > float64(maxH) {
		ratio = smaller
	} else {
		ratio = larger
	}
	w := int(math.Max(1, math.Round(float64(cw)*ratio)))
	h := int(math.Max(1, math.Round(float64(ch)*ratio)))
	if didW && w == maxW-1 {
		w = maxW
	}
	if didH && h == maxH-1 {
		h = maxH
	}
	return w, h
}

func fuzzyMatch(a, b int) bool { return math.Abs(float64(a-b)) <= 1 }

func imageMatchesRatio(sw, sh, tw, th int) bool {
	var cw, ch, ew, eh int
	if sw > tw {
		cw, ch = constrainDimensions(sw, sh, tw, 0)
		ew, eh = tw, th
	} else {
		cw, ch = constrainDimensions(tw, th, sw, 0)
		ew, eh = sw, sh
	}
	return fuzzyMatch(cw, ew) && fuzzyMatch(ch, eh)
}

// srcset mirrors wp_calculate_image_srcset.
func (a *Attachment) srcset(src string, w, h int) string {
	if w < 1 || h < 1 {
		return ""
	}
	type source struct {
		url   string
		width int
	}
	var order []int
	sources := map[int]source{}
	all := append([]AttachmentSize(nil), a.Sizes...)
	all = append(all, AttachmentSize{Name: "full", URL: a.URL, Width: a.Width, Height: a.Height})
	srcMatched := false
	for _, s := range all {
		if s.URL == "" || s.Width == 0 {
			continue
		}
		isSrc := false
		if !srcMatched && s.URL == src {
			srcMatched = true
			isSrc = true
		}
		if !isSrc && s.Width > 2048 {
			continue
		}
		if !imageMatchesRatio(w, h, s.Width, s.Height) {
			continue
		}
		if _, dup := sources[s.Width]; dup && !isSrc {
			continue
		}
		if _, dup := sources[s.Width]; !dup {
			if isSrc {
				order = append([]int{s.Width}, order...)
			} else {
				order = append(order, s.Width)
			}
		}
		sources[s.Width] = source{s.URL, s.Width}
	}
	if len(order) < 2 {
		return ""
	}
	var parts []string
	for _, wd := range order {
		parts = append(parts, sources[wd].url+" "+strconv.Itoa(wd)+"w")
	}
	return strings.Join(parts, ", ")
}

// wpGetAttachmentImage mirrors wp_get_attachment_image( $id, $size, false, $attr ).
func (r *renderer) wpGetAttachmentImage(a *Attachment, size string, extra *attrs) string {
	src, w, h, ok := a.imageSrc(size)
	if !ok {
		return ""
	}
	sizeClass := size
	if sizeClass == "" {
		sizeClass = "full"
	}
	at := newAttrs()
	if w > 0 {
		at.add("width", strconv.Itoa(w))
	}
	if h > 0 {
		at.add("height", strconv.Itoa(h))
	}
	at.add("src", src)
	at.add("class", "attachment-"+sizeClass+" size-"+sizeClass)
	at.add("alt", strings.TrimSpace(stripTags(a.Alt)))
	if extra != nil {
		for _, k := range extra.keys {
			at.set(k, extra.vals[k]...)
		}
	}
	lazy := false
	// Inside the_content (front-end render) WordPress leaves decoding/loading to
	// wp_filter_content_tags (filterContentTags); the editor render adds them here.
	if r.rc.Editing {
		if !at.has("decoding") {
			at.add("decoding", "async")
		}
		if !at.has("loading") {
			at.add("loading", "lazy")
			lazy = true
		} else {
			lazy = at.get("loading") == "lazy"
		}
	}
	if ss := a.srcset(src, w, h); ss != "" && !at.has("srcset") {
		at.add("srcset", ss)
		sizes := "(max-width: " + strconv.Itoa(w) + "px) 100vw, " + strconv.Itoa(w) + "px"
		if lazy {
			sizes = "auto, " + sizes
		}
		at.add("sizes", sizes)
	}
	var b strings.Builder
	b.WriteString("<img")
	for _, k := range at.keys {
		v := at.get(k)
		if k == "src" {
			v = escURL(v)
		} else {
			v = escAttr(v)
		}
		b.WriteString(" " + k + `="` + v + `"`)
	}
	b.WriteString(" />")
	return b.String()
}

func stripTags(s string) string {
	var b strings.Builder
	in := false
	for _, r := range s {
		switch {
		case r == '<':
			in = true
		case r == '>' && in:
			in = false
		case !in:
			b.WriteRune(r)
		}
	}
	return b.String()
}

var intermediateSizes = []string{"thumbnail", "medium", "medium_large", "large", "1536x1536", "2048x2048", "post-thumbnail", "full"}

// attachmentImageHTML mirrors Group_Control_Image_Size::get_attachment_image_html.
// settings is the widget settings map, sizeKey the group control name (e.g. "image" for
// image_size / image_custom_dimension) and imageKey the media setting (defaults to sizeKey).
func (r *renderer) attachmentImageHTML(settings map[string]any, sizeKey, imageKey string) string {
	if imageKey == "" {
		imageKey = sizeKey
	}
	image := asMap(settings[imageKey])
	if image == nil {
		image = map[string]any{}
	}
	size := phpString(settings[sizeKey+"_size"])
	imageClass := ""
	if truthy(settings["hover_animation"]) {
		imageClass = "elementor-animation-" + phpString(settings["hover_animation"])
	}
	id := int64(toInt(image["id"]))
	att := r.attachment(id)
	if id > 0 && att != nil && !att.isImage() {
		id = 0
		att = nil
	}
	known := false
	for _, s := range intermediateSizes {
		if s == size {
			known = true
		}
	}
	if att != nil && att.size(size) != nil {
		known = true
	}
	if id > 0 && att != nil && known {
		imageClass += " attachment-" + size + " size-" + size + " wp-image-" + strconv.FormatInt(id, 10)
		extra := newAttrs().add("class", strings.TrimSpace(imageClass))
		return r.wpGetAttachmentImage(att, size, extra)
	}
	src := ""
	if id > 0 {
		src = r.attachmentImageSrc(id, sizeKey, settings)
	}
	if src == "" {
		if u, ok := image["url"]; ok {
			src = phpString(u)
		}
	}
	if src == "" {
		return ""
	}
	classHTML := ""
	if imageClass != "" {
		classHTML = ` class="` + escAttr(imageClass) + `"`
	}
	return `<img src="` + escURL(src) + `" title="` + escAttr(r.imageTitle(image)) + `" alt="` + escAttr(r.imageAlt(image)) + `"` + classHTML + ` loading="lazy" />`
}

// attachmentImageSrc mirrors Group_Control_Image_Size::get_attachment_image_src.
func (r *renderer) attachmentImageSrc(id int64, sizeKey string, settings map[string]any) string {
	att := r.attachment(id)
	size := phpString(settings[sizeKey+"_size"])
	if size == "custom" {
		size = "full" // custom crops (BFI_Thumb) are not generated; use the full image
	}
	if att != nil {
		if u, _, _, ok := att.imageSrc(size); ok {
			return u
		}
	}
	if r.rc.MediaURL != nil {
		return r.rc.MediaURL(id, size)
	}
	return ""
}

// imageTitle mirrors Control_Media::get_image_title.
func (r *renderer) imageTitle(image map[string]any) string {
	if !truthy(image["id"]) {
		return ""
	}
	id := int64(toInt(image["id"]))
	if att := r.attachment(id); att != nil {
		return att.Title
	}
	if r.rc.PostTitle != nil {
		return r.rc.PostTitle(id)
	}
	return ""
}

// imageAlt mirrors Control_Media::get_image_alt.
func (r *renderer) imageAlt(image map[string]any) string {
	if !truthy(image["id"]) {
		if alt, ok := image["alt"]; ok {
			return strings.TrimSpace(stripTags(phpString(alt)))
		}
		return ""
	}
	att := r.attachment(int64(toInt(image["id"])))
	if att == nil {
		return ""
	}
	alt := strings.TrimSpace(stripTags(att.Alt))
	if alt == "" {
		alt = strings.TrimSpace(stripTags(att.Caption))
	}
	if alt == "" {
		alt = strings.TrimSpace(stripTags(att.Title))
	}
	return alt
}
