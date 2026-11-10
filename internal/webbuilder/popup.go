package webbuilder

import (
	"encoding/json"
	"regexp"
	"strings"
)

// Popups (template type "popup"): a template whose snapshot is shown in a modal dialog. The display
// conditions of the template decide where it can show (the same engine as every template; several
// popups can match one page); PopupSettings decide when and how often. The server prints each
// matching popup hidden before </body> and a small inline script opens it on its trigger and
// remembers the visitor's choice in localStorage (a functional preference, no cookie).

// Popup triggers.
const (
	PopupTriggerLoad   = "load"   // as soon as the page has loaded (after Delay seconds)
	PopupTriggerScroll = "scroll" // when the visitor has scrolled Scroll percent of the page
	PopupTriggerExit   = "exit"   // when the pointer leaves through the top of the window (touch: after Delay seconds)
	PopupTriggerClick  = "click"  // when an element matching Selector is clicked
)

// Popup frequencies: how often one visitor sees the popup.
const (
	PopupFreqAlways  = "always"  // on every page view
	PopupFreqSession = "session" // once per browser session
	PopupFreqOnce    = "once"    // once, until it is changed (see Version)
	PopupFreqDays    = "days"    // again after Days days
)

// Popup placement.
const (
	PopupPosCenter = "center"
	PopupPosTop    = "top"
	PopupPosBottom = "bottom"
)

// PopupSettings are the behaviour of a popup template (post meta MetaPopup, JSON).
type PopupSettings struct {
	Trigger   string `json:"trigger"`
	Delay     int    `json:"delay"`    // seconds, 0…PopupMaxDelay (load, exit on touch)
	Scroll    int    `json:"scroll"`   // percent 1…100 (scroll)
	Selector  string `json:"selector"` // click: "#id" or ".class" (PopupSelectorPattern)
	Frequency string `json:"frequency"`
	Days      int    `json:"days"`    // 1…PopupMaxDays (days)
	Version   int    `json:"version"` // bump to show a once/days popup again to everyone
	Position  string `json:"position"`
	Width     int    `json:"width"` // px, PopupMinWidth…PopupMaxWidth
	// Overlay closes the popup when the backdrop is clicked; Esc always closes it.
	Overlay bool `json:"overlay"`
	// AfterConsent holds the popup back until the visitor has answered the Pursae Cookie banner
	// (no effect while Pursae Cookie is off).
	AfterConsent bool `json:"afterConsent"`
}

const (
	PopupMaxDelay        = 300
	PopupMaxDays         = 365
	PopupMinWidth        = 200
	PopupMaxWidth        = 1600
	PopupSelectorPattern = `^[#.][A-Za-z_][A-Za-z0-9_-]{0,63}$`
)

var (
	popupSelectorRE = regexp.MustCompile(PopupSelectorPattern)
	popupTriggers   = []string{PopupTriggerLoad, PopupTriggerScroll, PopupTriggerExit, PopupTriggerClick}
	popupFreqs      = []string{PopupFreqAlways, PopupFreqSession, PopupFreqOnce, PopupFreqDays}
	popupPositions  = []string{PopupPosCenter, PopupPosTop, PopupPosBottom}
)

// DefaultPopupSettings: opens 2 seconds after load, once per session, centred.
func DefaultPopupSettings() PopupSettings {
	return PopupSettings{Trigger: PopupTriggerLoad, Delay: 2, Scroll: 50, Frequency: PopupFreqSession, Days: 7, Version: 1,
		Position: PopupPosCenter, Width: 640, Overlay: true, AfterConsent: true}
}

func inList(v string, list []string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

// ValidatePopup checks s as sent by a client; the message names the first problem ("" = valid).
func ValidatePopup(s PopupSettings) string {
	switch {
	case !inList(s.Trigger, popupTriggers):
		return "The trigger must be load, scroll, exit or click."
	case !inList(s.Frequency, popupFreqs):
		return "The frequency must be always, session, once or days."
	case !inList(s.Position, popupPositions):
		return "The position must be center, top or bottom."
	case s.Delay < 0 || s.Delay > PopupMaxDelay:
		return "The delay must be between 0 and 300 seconds."
	case s.Scroll < 1 || s.Scroll > 100:
		return "The scroll depth must be between 1 and 100 percent."
	case s.Days < 1 || s.Days > PopupMaxDays:
		return "The number of days must be between 1 and 365."
	case s.Width < PopupMinWidth || s.Width > PopupMaxWidth:
		return "The width must be between 200 and 1600 pixels."
	case s.Version < 0 || s.Version > 1<<20:
		return "The version is not valid."
	case s.Trigger == PopupTriggerClick && !popupSelectorRE.MatchString(s.Selector):
		return "A click trigger needs a selector such as #open-popup or .open-popup."
	}
	return ""
}

// ParsePopup reads stored settings: missing or invalid values fall back to the defaults, so a
// hand-edited meta value never breaks a page.
func ParsePopup(raw string) PopupSettings {
	def := DefaultPopupSettings()
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return def
	}
	s := def
	if json.Unmarshal([]byte(raw), &s) != nil || ValidatePopup(s) != "" {
		return def
	}
	return s
}
