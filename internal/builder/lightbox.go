package builder

// Lightbox data attributes (Widget_Base::add_lightbox_data_attributes) and kit setting lookup.

import (
	"encoding/base64"
	"strconv"
	"strings"
)

// kitSetting returns a kit setting with the kit control default applied (Kit::get_settings).
func (r *renderer) kitSetting(name string) any {
	raw := kitRawSettings(r.rc)
	if v, ok := raw[name]; ok && v != nil {
		return v
	}
	if st := stackFor("kit", r.bps); st != nil {
		if c := st.byName[name]; c != nil {
			return c.Default
		}
	}
	return nil
}

// addLightboxData mirrors add_lightbox_data_attributes. lightboxSetting is the widget's
// open_lightbox value ("default", "yes", "no"); id the attachment id (0 = none); groupID the
// slideshow id ("" = none).
func (e *elemCtx) addLightboxData(key string, id int64, lightboxSetting any, groupID string, overwrite bool) {
	globalOn := phpString(e.r.kitSetting("global_image_lightbox")) == "yes"
	a := e.attr(key)
	put := func(k, v string) {
		if overwrite {
			a.set(k, v)
		} else {
			a.add(k, v)
		}
	}
	ls := phpString(lightboxSetting)
	if lightboxSetting != nil && ls == "no" {
		if globalOn {
			put("data-elementor-open-lightbox", "no")
		}
		return
	}
	if ls != "yes" && !globalOn {
		return
	}
	put("data-elementor-open-lightbox", "yes")
	params := newOmap()
	if id > 0 {
		params.Set("id", int(id))
		u := ""
		if att := e.r.attachment(id); att != nil {
			u = att.URL
		} else if e.r.rc.MediaURL != nil {
			u = e.r.rc.MediaURL(id, "")
		}
		if u == "" {
			params.Set("url", false)
		} else {
			params.Set("url", u)
		}
	}
	if groupID != "" {
		put("data-elementor-lightbox-slideshow", groupID)
		params.Set("slideshow", groupID)
	}
	if id > 0 {
		if att := e.r.attachment(id); att != nil {
			data := map[string]string{"alt": att.Alt, "caption": att.Caption, "description": att.Description, "title": att.Title}
			if src := phpString(e.r.kitSetting("lightbox_title_src")); src != "" && data[src] != "" {
				put("data-elementor-lightbox-title", data[src])
			}
			if src := phpString(e.r.kitSetting("lightbox_description_src")); src != "" && data[src] != "" {
				put("data-elementor-lightbox-description", data[src])
			}
		}
	}
	put("data-e-action-hash", createActionHash("lightbox", params))
}

// createActionHash mirrors Frontend::create_action_hash.
func createActionHash(action string, settings *omap) string {
	payload := "elementor-action:action=" + action + "&settings=" + base64.StdEncoding.EncodeToString([]byte(phpJSON(settings)))
	return "#" + rawURLEncode(payload)
}

// rawURLEncode mirrors PHP rawurlencode.
func rawURLEncode(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '-' || c == '_' || c == '.' || c == '~' {
			b.WriteByte(c)
			continue
		}
		b.WriteString("%" + strings.ToUpper(strconv.FormatInt(int64(c)>>4, 16)) + strings.ToUpper(strconv.FormatInt(int64(c)&15, 16)))
	}
	return b.String()
}
