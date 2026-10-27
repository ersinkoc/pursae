package server

// Golementor v4: design-system sync (upstream modules/design-system-sync). Color variables and
// global classes marked "sync to v3" are exposed to classic widgets as v3 global custom
// properties in uploads/elementor/design-system-sync/design-system-sync.css:
//   - color variables:  --e-global-color-v4-<label>:var(--<label>);
//   - typography of synced classes (normal state, per breakpoint):
//     --e-global-typography-v4-<label>-<css prop>:<value>;
// POST elementor/v1/design-system-sync/stylesheet (re)generates it and answers 201 {url, version},
// or 204 when nothing is synced; front-end pages link it.

import (
	"context"
	"fmt"
	"html"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

const (
	gweV4SyncMapMeta = "_elementor_global_classes_sync_to_v3" // kit meta: class id → true
	gweV4DSSOption   = "_elementor_design_system_sync_css_meta"
)

// gweV4SyncTypographyProps are Sync_Typography_Props::get_css_props, in order.
var gweV4SyncTypographyProps = []string{"font-family", "font-size", "font-weight", "font-style", "text-decoration",
	"line-height", "letter-spacing", "word-spacing", "text-transform"}

func (s *Server) gweV4DSSPath() string {
	return filepath.Join(s.cfg.UploadsDir, "elementor", "design-system-sync", "design-system-sync.css")
}

// gweV4SyncMap reads the kit's synced class ids.
func gweV4SyncMap(meta map[string]any) map[string]bool {
	out := map[string]bool{}
	if m, ok := meta[gweV4SyncMapMeta].(map[string]any); ok {
		for id, v := range m {
			if truthyAny(v) {
				out[id] = true
			}
		}
	}
	return out
}

// gweV4ApplySyncMap mirrors Global_Classes_Sync_Map::apply_changes for a published classes PUT.
func (s *Server) gweV4ApplySyncMap(ctx context.Context, kitID int64, touched map[string]gweGlobalClass, deleted []string) error {
	kit, err := s.store.GetPost(ctx, kitID)
	if err != nil {
		return err
	}
	cur := gweV4SyncMap(kit.Meta)
	for _, id := range deleted {
		delete(cur, id)
	}
	for id, g := range touched {
		if g.SyncToV3 != nil && *g.SyncToV3 {
			cur[id] = true
		} else {
			delete(cur, id)
		}
	}
	out := map[string]any{}
	for id := range cur {
		out[id] = true
	}
	return s.gweV4SetKitMeta(ctx, kitID, gweV4SyncMapMeta, out)
}

// gweV4DSSContent mirrors Stylesheet_Manager::parse_content.
func (s *Server) gweV4DSSContent(c *gin.Context) string {
	kit, err := s.gweKit(c)
	if err != nil {
		return ""
	}
	raw := map[string][]string{} // device ("" = desktop) → raw CSS
	var deviceOrder []string
	addRaw := func(device, css string) {
		if _, ok := raw[device]; !ok {
			deviceOrder = append(deviceOrder, device)
		}
		raw[device] = append(raw[device], strings.TrimSpace(css))
	}

	// Synced color variables.
	data, _, _ := s.gweV4Variables(c, kit.ID)
	ids := make([]string, 0, len(data))
	for id := range data {
		ids = append(ids, id)
	}
	order := func(id string) float64 { f, _ := data[id]["order"].(float64); return f }
	sort.SliceStable(ids, func(i, j int) bool {
		if order(ids[i]) != order(ids[j]) {
			return order(ids[i]) < order(ids[j])
		}
		return ids[i] < ids[j]
	})
	var colors []string
	for _, id := range ids {
		v := data[id]
		if truthyAny(v["deleted"]) || gweStr(v["type"]) != "global-color-variable" || !truthyAny(v["sync_to_v3"]) {
			continue
		}
		label := strings.TrimSpace(gweStr(v["label"]))
		if label == "" {
			continue
		}
		colors = append(colors, "--e-global-color-v4-"+strings.ToLower(label)+":var(--"+label+");")
	}
	if len(colors) > 0 {
		addRaw("", ":root { "+strings.Join(colors, " ")+" }")
	}

	// Typography of synced classes (published classes, global order).
	synced := gweV4SyncMap(kit.Meta)
	if len(synced) > 0 {
		classes, _ := s.gweV4GlobalClasses(c, kit.ID, false)
		env := s.gweAtomicEnv(c, false)
		grouped := map[string][]string{}
		var groupOrder []string
		for _, g := range classes {
			label := strings.TrimSpace(g.Label)
			if !synced[g.ID] || label == "" {
				continue
			}
			byBP := map[string]any{}
			var bpOrder []string
			for _, vv := range g.Variants {
				vm, _ := vv.(map[string]any)
				meta, _ := vm["meta"].(map[string]any)
				if meta == nil {
					continue
				}
				if _, ok := meta["breakpoint"]; !ok {
					continue
				}
				st, ok := meta["state"]
				if !ok || (st != nil && st != "normal") {
					continue
				}
				bp := gweStr(meta["breakpoint"])
				if bp == "" {
					bp = "desktop"
				}
				if _, seen := byBP[bp]; !seen {
					bpOrder = append(bpOrder, bp)
				}
				byBP[bp] = vm["props"]
			}
			desktop, _ := byBP["desktop"].(map[string]any)
			hasTypo := false
			for _, p := range gweV4SyncTypographyProps {
				if _, ok := desktop[p]; ok {
					hasTypo = true
				}
			}
			if !hasTypo {
				continue
			}
			id := "v4-" + strings.ToLower(label)
			for _, bp := range bpOrder {
				props, _ := byBP[bp].(map[string]any)
				if len(props) == 0 {
					continue
				}
				vals := env.StylePropValues(props)
				if _, ok := grouped[bp]; !ok {
					groupOrder = append(groupOrder, bp)
					grouped[bp] = []string{}
				}
				for _, p := range gweV4SyncTypographyProps {
					if v := vals[p]; v != "" {
						grouped[bp] = append(grouped[bp], "--e-global-typography-"+id+"-"+p+":"+v+";")
					}
				}
			}
		}
		for _, bp := range groupOrder {
			device := bp
			if bp == "desktop" {
				device = ""
			}
			addRaw(device, ":root { "+strings.Join(grouped[bp], " ")+" }")
		}
	}

	// Stylesheet::__toString: raw CSS per device, wrapped in the device's max-width query.
	devices := map[string]int{}
	for _, b := range s.gweRenderContext(c, false).Breakpoints {
		devices[b.Name] = b.Value
	}
	var out strings.Builder
	for _, d := range deviceOrder {
		text := strings.Join(raw[d], "\n")
		if max, ok := devices[d]; ok && text != "" {
			text = "@media(max-width: " + strconv.Itoa(max) + "px){" + text + "}"
		}
		out.WriteString(text)
	}
	return out.String()
}

// gweV4DSSGenerate mirrors Stylesheet_Manager::generate: nil when there is nothing to sync.
func (s *Server) gweV4DSSGenerate(c *gin.Context) map[string]any {
	css := s.gweV4DSSContent(c)
	path := s.gweV4DSSPath()
	if css == "" {
		_ = os.Remove(path)
		return nil
	}
	_ = os.MkdirAll(filepath.Dir(path), 0o755)
	if old, err := os.ReadFile(path); err != nil || string(old) != css {
		tmp := path + ".tmp" + strconv.FormatInt(time.Now().UnixNano(), 36)
		if os.WriteFile(tmp, []byte(css), 0o644) != nil || os.Rename(tmp, path) != nil {
			return nil
		}
	}
	ver := time.Now().Unix()
	_ = s.settings.Set(c, map[string]any{gweV4DSSOption: map[string]any{"time": ver}})
	return map[string]any{"url": s.gweV4DSSURL(c, ver), "version": ver}
}

func (s *Server) gweV4DSSURL(c *gin.Context, ver int64) string {
	return s.links(c).HomeURL("/wp-content/uploads/elementor/design-system-sync/design-system-sync.css?ver=" + strconv.FormatInt(ver, 10))
}

// gweV4DSSStylesheet is POST elementor/v1/design-system-sync/stylesheet.
func (s *Server) gweV4DSSStylesheet(c *gin.Context, _ *restRequest) {
	res := s.gweV4DSSGenerate(c)
	if res == nil {
		c.Status(http.StatusNoContent)
		return
	}
	c.JSON(http.StatusCreated, res)
}

// gweV4DSSDelete drops the stylesheet (it is rebuilt on the next request that needs it).
func (s *Server) gweV4DSSDelete() { _ = os.Remove(s.gweV4DSSPath()) }

// gweV4DSSLink is the front end's <link> to the stylesheet (Stylesheet_Manager::enqueue).
func (s *Server) gweV4DSSLink(c *gin.Context) string {
	path := s.gweV4DSSPath()
	if _, err := os.Stat(path); err != nil {
		if s.gweV4DSSGenerate(c) == nil {
			return ""
		}
	}
	var ver int64
	if m, ok := s.settings.Get(gweV4DSSOption).(map[string]any); ok {
		ver = gweInt(m["time"])
	}
	if ver == 0 {
		if st, err := os.Stat(path); err == nil {
			ver = st.ModTime().Unix()
		}
	}
	return fmt.Sprintf("<link rel='stylesheet' id='elementor-design-system-sync-css' href='%s' media='all' />\n", html.EscapeString(s.gweV4DSSURL(c, ver)))
}
