package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"wpgo/internal/builder/atomic"
	"wpgo/internal/store"
)

// REST routes of Golementor's v4 (atomic) features, namespace elementor/v1 like upstream:
// global classes, global variables, components, kit element defaults, default styles and the
// design-system stylesheet. Shapes follow upstream 4.3.5's controllers (Response_Builder wraps
// payloads as {"data":...,"meta":...}; Error_Builder errors carry data.status and data.meta).

func (s *Server) registerGoWebEditorV4REST() {
	api := s.rest
	const ns = "elementor/v1"
	need := func(capability string, h restHandler) restHandler {
		return func(c *gin.Context, r *restRequest) {
			u := restCurrent(c)
			if u == nil {
				restErr(c, http.StatusUnauthorized, "rest_forbidden", "Sorry, you are not allowed to do that.")
				return
			}
			if !u.Can(capability, nil) {
				restErr(c, http.StatusForbidden, "rest_forbidden", "Sorry, you are not allowed to do that.")
				return
			}
			h(c, r)
		}
	}
	read := func(h restHandler) restHandler { return need("edit_posts", h) }
	write := func(h restHandler) restHandler { return need("manage_options", h) }
	route := func(pattern string, methods map[string]restHandler) {
		api.register(ns, "/elementor/v1/"+pattern, methods, nil)
	}

	// Global classes.
	route("global-classes", map[string]restHandler{http.MethodGet: read(s.gweV4ClassesList), http.MethodPut: write(s.gweV4ClassesPut)})
	route("global-classes/post", map[string]restHandler{http.MethodGet: read(s.gweV4ClassesForPost)})
	route("global-classes/styles", map[string]restHandler{http.MethodGet: read(s.gweV4ClassesStyles)})
	route("global-classes/usage", map[string]restHandler{http.MethodGet: write(s.gweV4ClassesUsage)})

	// Variables.
	route("variables/list", map[string]restHandler{http.MethodGet: read(s.gweV4VarList)})
	route("variables/create", map[string]restHandler{http.MethodPost: write(s.gweV4VarCreate)})
	route("variables/update", map[string]restHandler{http.MethodPut: write(s.gweV4VarUpdate)})
	route("variables/delete", map[string]restHandler{http.MethodPut: write(s.gweV4VarDelete)})
	route("variables/restore", map[string]restHandler{http.MethodPut: write(s.gweV4VarRestore)})
	route("variables/batch", map[string]restHandler{http.MethodPost: write(s.gweV4VarBatch)})

	// Components.
	route("components", map[string]restHandler{http.MethodGet: read(s.gweV4ComponentsList), http.MethodPost: write(s.gweV4ComponentsCreate)})
	route("components/styles", map[string]restHandler{http.MethodGet: read(s.gweV4ComponentsStyles)})
	route("components/create-validate", map[string]restHandler{http.MethodPost: write(s.gweV4ComponentsValidate)})
	route("components/overridable-props", map[string]restHandler{http.MethodGet: read(s.gweV4ComponentsOverridable)})
	route("components/status", map[string]restHandler{http.MethodPut: write(s.gweV4ComponentsStatus)})
	route("components/archive", map[string]restHandler{http.MethodPost: write(s.gweV4ComponentsArchive)})
	route("components/update-titles", map[string]restHandler{http.MethodPost: write(s.gweV4ComponentsTitles)})
	route("components/lock", map[string]restHandler{http.MethodPost: write(s.gweV4ComponentLock)})
	route("components/unlock", map[string]restHandler{http.MethodPost: write(s.gweV4ComponentUnlock)})
	route("components/lock-status", map[string]restHandler{http.MethodGet: write(s.gweV4ComponentLockStatus)})

	// Kit element defaults.
	route("kit-elements-defaults", map[string]restHandler{http.MethodGet: read(s.gweV4ElementsDefaults)})
	route(`kit-elements-defaults/(?P<type>[\w\-\_]+)`, map[string]restHandler{http.MethodPut: write(s.gweV4ElementsDefaultsPut),
		http.MethodDelete: write(s.gweV4ElementsDefaultsDelete)})

	// Default styles.
	route("default-styles", map[string]restHandler{http.MethodGet: read(s.gweV4DefaultStylesList)})
	route("default-styles/(?P<tag>[a-z0-9]+)", map[string]restHandler{http.MethodGet: read(s.gweV4DefaultStyleGet),
		http.MethodPut: write(s.gweV4DefaultStylePut), http.MethodDelete: write(s.gweV4DefaultStyleDelete)})

	// Design-system stylesheet.
	route("design-system-sync/stylesheet", map[string]restHandler{http.MethodPost: read(s.gweV4DSSStylesheet)})
}

// --- helpers ---------------------------------------------------------------------------------------

// gweV4Data writes upstream's Response_Builder envelope.
func gweV4Data(c *gin.Context, status int, data any, meta any) {
	if meta == nil {
		meta = []any{}
	}
	c.JSON(status, map[string]any{"data": data, "meta": meta})
}

// gweV4Err writes upstream's Error_Builder error ({code,message,data:{status,meta}}).
func gweV4Err(c *gin.Context, status int, code, msg string, meta any) {
	if meta == nil {
		meta = []any{}
	}
	restErr(c, status, code, msg, map[string]any{"meta": meta})
}

// gweV4KitID resolves the active kit, answering 500 when it cannot.
func (s *Server) gweV4KitID(c *gin.Context) (int64, bool) {
	kit, err := s.gweKit(c)
	if err != nil {
		restErr(c, http.StatusInternalServerError, "unexpected_error", "Something went wrong")
		return 0, false
	}
	return kit.ID, true
}

func gweV4Preview(c *gin.Context, r *restRequest) bool {
	ctx, _ := r.Str(c, "context")
	return ctx == "preview"
}

func gweV4Strings(v any) ([]string, bool) {
	list, ok := v.([]any)
	if !ok {
		return nil, v == nil
	}
	out := make([]string, 0, len(list))
	for _, x := range list {
		str, ok := x.(string)
		if !ok {
			return nil, false
		}
		out = append(out, str)
	}
	return out, true
}

func gweV4Has(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

func gweV4Fail(c *gin.Context, err error) {
	restErr(c, http.StatusInternalServerError, "unexpected_error", "Something went wrong", map[string]any{"meta": []any{}})
	_ = err
}

// --- global classes --------------------------------------------------------------------------------

func (s *Server) gweV4ClassesList(c *gin.Context, r *restRequest) {
	kitID, ok := s.gweV4KitID(c)
	if !ok {
		return
	}
	all, err := s.gweV4GlobalClasses(c, kitID, gweV4Preview(c, r))
	if err != nil {
		gweV4Fail(c, err)
		return
	}
	list := make([]map[string]any, 0, len(all))
	for _, g := range all {
		list = append(list, map[string]any{"id": g.ID, "label": g.Label})
	}
	gweV4Data(c, http.StatusOK, list, nil)
}

// gweV4ClassesAnswer is the {data:{id:item|null}, meta:{order}} answer of /post and /styles.
func (s *Server) gweV4ClassesAnswer(c *gin.Context, r *restRequest, ids []string) {
	if len(ids) == 0 {
		gweV4Data(c, http.StatusOK, map[string]any{}, map[string]any{"order": []string{}})
		return
	}
	kitID, ok := s.gweV4KitID(c)
	if !ok {
		return
	}
	byID, order, err := s.gweV4GlobalClassesByID(c, kitID, gweV4Preview(c, r), ids)
	if err != nil {
		gweV4Fail(c, err)
		return
	}
	data := map[string]any{}
	for _, id := range ids {
		if g, ok := byID[id]; ok {
			data[id] = g.item()
		} else {
			data[id] = nil
		}
	}
	gweV4Data(c, http.StatusOK, data, map[string]any{"order": order})
}

func (s *Server) gweV4ClassesForPost(c *gin.Context, r *restRequest) {
	id, ok := r.Int(c, "post_id")
	if !ok || id <= 0 {
		restErr(c, http.StatusBadRequest, "rest_missing_callback_param", "Missing parameter(s): post_id", map[string]any{"params": []string{"post_id"}})
		return
	}
	var ids []string
	if p, err := s.store.GetPost(c, id); err == nil {
		ids = gweV4ClassIDsIn(gweElementsRaw(p))
	}
	s.gweV4ClassesAnswer(c, r, ids)
}

func (s *Server) gweV4ClassesStyles(c *gin.Context, r *restRequest) {
	raw, ok := r.Str(c, "ids")
	if !ok {
		restErr(c, http.StatusBadRequest, "rest_missing_callback_param", "Missing parameter(s): ids", map[string]any{"params": []string{"ids"}})
		return
	}
	var ids []string
	seen := map[string]bool{}
	for _, id := range strings.Split(raw, ",") {
		if id = strings.TrimSpace(id); id != "" && !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	s.gweV4ClassesAnswer(c, r, ids)
}

func (s *Server) gweV4ClassesUsage(c *gin.Context, _ *restRequest) {
	kitID, ok := s.gweV4KitID(c)
	if !ok {
		return
	}
	all, err := s.gweV4GlobalClasses(c, kitID, false)
	if err != nil {
		gweV4Fail(c, err)
		return
	}
	ids := make([]string, 0, len(all))
	for _, g := range all {
		ids = append(ids, g.ID)
	}
	usage, err := s.gweV4ClassUsages(c, ids)
	if err != nil {
		gweV4Fail(c, err)
		return
	}
	gweV4Data(c, http.StatusOK, usage, nil)
}

// gweV4ParseClass validates one global class item with upstream's Style_Parser (the label rules,
// meta.state / meta.breakpoint, every variant's props against the style schema: atomic.ValidateStyle);
// errors are "<key>.<path>: <reason>". The returned class keeps only the props the schema knows.
func gweV4ParseClass(key string, v any) (gweGlobalClass, []string) {
	m, ok := v.(map[string]any)
	if !ok {
		return gweGlobalClass{}, []string{key + ": invalid_type"}
	}
	// PHP's [] for an empty props object, and an empty custom CSS, as upstream stores them.
	if vs, ok := m["variants"].([]any); ok {
		for _, vv := range vs {
			vm, ok := vv.(map[string]any)
			if !ok {
				continue
			}
			if p, present := vm["props"]; !present || p == nil || isEmptyList(p) {
				vm["props"] = map[string]any{}
			}
			if cm, ok := vm["custom_css"].(map[string]any); ok {
				if raw, _ := cm["raw"].(string); raw == "" {
					vm["custom_css"] = nil
				}
			}
		}
	}
	parsed, verrs := atomic.ValidateStyle(m)
	var errs []string
	for _, e := range verrs {
		errs = append(errs, key+"."+e.String())
	}
	id, idOK := m["id"].(string)
	if idOK && id != key {
		errs = append(errs, key+".id: mismatching_value")
	}
	label, _ := m["label"].(string)
	var variants []any
	if b, err := json.Marshal(parsed); err == nil {
		var out map[string]any
		if json.Unmarshal(b, &out) == nil {
			variants, _ = out["variants"].([]any)
		}
	}
	if variants == nil {
		variants = []any{}
	}
	g := gweGlobalClass{ID: id, Label: strings.TrimSpace(label), Variants: variants}
	if sync, present := m["sync_to_v3"]; present {
		b := truthyAny(sync)
		g.SyncToV3 = &b
	}
	return g, errs
}

func isEmptyList(v any) bool {
	l, ok := v.([]any)
	return ok && len(l) == 0
}

// gweV4UniqueLabel is Global_Classes_Labels::generate_unique_label (DUP_ prefix, 50 chars max).
func gweV4UniqueLabel(label string, existing []string) string {
	const prefix, maxLen = "DUP_", 50
	if strings.HasPrefix(label, prefix) {
		base := label[len(prefix):]
		counter := 1
		cand := fmt.Sprintf("%s%s%d", prefix, base, counter)
		for gweV4Has(existing, cand) {
			counter++
			cand = fmt.Sprintf("%s%s%d", prefix, base, counter)
		}
		if len(cand) > maxLen {
			n := maxLen - len(fmt.Sprintf("%s%d", prefix, counter))
			base = base[:min(n, len(base))]
			cand = fmt.Sprintf("%s%s%d", prefix, base, counter)
		}
		return cand
	}
	avail := len(label)
	cand := prefix + label
	if len(cand) > maxLen {
		avail = maxLen - len(prefix)
		cand = prefix + label[:avail]
	}
	base := label[:avail]
	for counter := 1; gweV4Has(existing, cand); counter++ {
		cand = fmt.Sprintf("%s%s%d", prefix, base, counter)
		if len(cand) > maxLen {
			n := maxLen - len(fmt.Sprintf("%s%d", prefix, counter))
			base = label[:min(n, len(label))]
			cand = fmt.Sprintf("%s%s%d", prefix, base, counter)
		}
	}
	return cand
}

func (s *Server) gweV4ClassesPut(c *gin.Context, r *restRequest) {
	preview := gweV4Preview(c, r)
	changes, ok := r.Body["changes"].(map[string]any)
	if !ok {
		restErr(c, http.StatusBadRequest, "rest_invalid_param", "Invalid parameter(s): changes", map[string]any{"params": map[string]string{"changes": "changes is not of type object."}})
		return
	}
	added, ok1 := gweV4Strings(changes["added"])
	deleted, ok2 := gweV4Strings(changes["deleted"])
	modified, ok3 := gweV4Strings(changes["modified"])
	if !ok1 || !ok2 || !ok3 {
		restErr(c, http.StatusBadRequest, "rest_invalid_param", "Invalid parameter(s): changes", map[string]any{"params": map[string]string{"changes": "changes[added|deleted|modified] must be arrays of strings."}})
		return
	}
	rawItems, ok := r.Body["items"].(map[string]any)
	if !ok {
		if l, isList := r.Body["items"].([]any); isList && len(l) == 0 {
			rawItems = map[string]any{} // PHP serializes an empty object as []
		} else {
			restErr(c, http.StatusBadRequest, "rest_invalid_param", "Invalid parameter(s): items", map[string]any{"params": map[string]string{"items": "items is not of type object."}})
			return
		}
	}
	order, ok := gweV4Strings(r.Body["order"])
	if !ok || r.Body["order"] == nil {
		gweV4Err(c, http.StatusBadRequest, "invalid_order", "Invalid order: order: invalid", nil)
		return
	}

	kitID, ok := s.gweV4KitID(c)
	if !ok {
		return
	}
	current, err := s.gweV4GlobalClasses(c, kitID, preview)
	if err != nil {
		gweV4Fail(c, err)
		return
	}

	// Validate the items.
	keys := make([]string, 0, len(rawItems))
	for k := range rawItems {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	touched := map[string]gweGlobalClass{}
	var errs []string
	for _, k := range keys {
		g, e := gweV4ParseClass(k, rawItems[k])
		if len(e) > 0 {
			errs = append(errs, e...)
			continue
		}
		touched[k] = g
	}
	if len(errs) > 0 {
		gweV4Err(c, http.StatusBadRequest, "invalid_items", "Invalid items: "+strings.Join(errs, ", "), nil)
		return
	}
	if total := len(current) - len(deleted) + len(added); total > gweV4MaxClasses {
		gweV4Err(c, http.StatusBadRequest, "global_classes_limit_exceeded",
			fmt.Sprintf("Global classes limit exceeded. Maximum allowed: %d", gweV4MaxClasses),
			map[string]any{"current_count": total, "max_allowed": gweV4MaxClasses})
		return
	}

	// Added labels that clash with existing ones are renamed with the DUP_ scheme.
	var existingLabels []string
	for _, g := range current {
		if !gweV4Has(deleted, g.ID) {
			existingLabels = append(existingLabels, g.Label)
		}
	}
	modifiedLabels := map[string]any{}
	for _, id := range keys {
		g := touched[id]
		if !gweV4Has(added, id) {
			continue
		}
		for _, other := range current {
			if other.ID != id && !gweV4Has(deleted, other.ID) && other.Label == g.Label {
				nl := gweV4UniqueLabel(g.Label, existingLabels)
				modifiedLabels[id] = map[string]string{"original": g.Label, "modified": nl}
				g.Label = nl
				touched[id] = g
				break
			}
		}
	}

	// The final ids: existing (minus deleted), then the touched ones that are new.
	var final []string
	for _, g := range current {
		if !gweV4Has(deleted, g.ID) {
			final = append(final, g.ID)
		}
	}
	for _, id := range keys {
		if !gweV4Has(final, id) && !gweV4Has(deleted, id) {
			final = append(final, id)
		}
	}
	finalSet := map[string]bool{}
	for _, id := range final {
		finalSet[id] = true
	}
	var newOrder []string
	inOrder := map[string]bool{}
	for _, id := range order {
		if finalSet[id] && !inOrder[id] {
			inOrder[id] = true
			newOrder = append(newOrder, id)
		}
	}
	for _, id := range final {
		if !inOrder[id] {
			newOrder = append(newOrder, id)
		}
	}

	persist := map[string]gweGlobalClass{}
	for id, g := range touched {
		if gweV4Has(added, id) || gweV4Has(modified, id) {
			persist[id] = g
		}
	}
	if err := s.gweV4ApplyClasses(c, kitID, gweV4ClassChange{Preview: preview, Deleted: deleted, Items: persist, Order: newOrder}); err != nil {
		gweV4Fail(c, err)
		return
	}
	if !preview {
		if err := s.gweV4ApplySyncMap(c, kitID, persist, deleted); err != nil {
			gweV4Fail(c, err)
			return
		}
	}
	s.gweV4Invalidate()
	if len(modifiedLabels) > 0 {
		gweV4Data(c, http.StatusOK, map[string]any{"code": "DUPLICATED_LABEL", "modifiedLabels": modifiedLabels}, nil)
		return
	}
	c.Status(http.StatusNoContent)
}

// --- variables ---------------------------------------------------------------------------------------

type gweV4VarError struct {
	status      int
	code, msg   string
	description string
}

func (e *gweV4VarError) Error() string { return e.description }

var (
	gweV4ErrDupLabel = &gweV4VarError{http.StatusBadRequest, "duplicated_label", "Variable label already exists", "Variable label already exists."}
	gweV4ErrLimit    = &gweV4VarError{http.StatusBadRequest, "invalid_variable_limit_reached", "Reached the maximum number of variables", "Total variables count limit reached"}
	gweV4ErrNotFound = &gweV4VarError{http.StatusNotFound, "variable_not_found", "Variable not found", "Variable not found"}
	gweV4ErrType     = &gweV4VarError{http.StatusBadRequest, "type_mismatch", "Type change is forbidden", "Type change is forbidden"}
)

func gweV4VarFail(c *gin.Context, err error) {
	if e, ok := err.(*gweV4VarError); ok {
		c.JSON(e.status, restError{Code: e.code, Message: e.msg, Data: map[string]any{"status": e.status}})
		return
	}
	c.JSON(http.StatusInternalServerError, restError{Code: "unexpected_server_error", Message: "Unexpected server error", Data: map[string]any{"status": 500}})
}

// gweV4ParamError answers like WordPress' argument validation (rest_invalid_param).
func gweV4ParamError(c *gin.Context, param, msg string) {
	restErr(c, http.StatusBadRequest, "rest_invalid_param", "Invalid parameter(s): "+param,
		map[string]any{"params": map[string]string{param: msg}, "details": map[string]any{}})
}

func gweV4CheckLabel(label string) string {
	switch {
	case label == "":
		return "Label cannot be empty"
	case len(label) > 50:
		return "Label cannot exceed 50 characters"
	case strings.ContainsAny(label, " \t\n"):
		return "Label cannot contain spaces"
	}
	return ""
}

func gweV4CheckValue(value string) string {
	switch {
	case value == "":
		return "Value cannot be empty"
	case len(value) > 512:
		return "Value cannot exceed 512 characters"
	}
	return ""
}

// gweV4VarParams reads and validates the variable params present in the request.
func gweV4VarParams(c *gin.Context, r *restRequest, required ...string) (map[string]string, bool) {
	out := map[string]string{}
	for _, key := range []string{"id", "type", "label", "value"} {
		v, ok := r.Str(c, key)
		v = strings.TrimSpace(v)
		if !ok || v == "" && !gweV4Has(required, key) {
			if gweV4Has(required, key) {
				restErr(c, http.StatusBadRequest, "rest_missing_callback_param", "Missing parameter(s): "+key, map[string]any{"params": []string{key}})
				return nil, false
			}
			continue
		}
		msg := ""
		switch key {
		case "id":
			if v == "" {
				msg = "ID cannot be empty"
			} else if len(v) > 64 {
				msg = "ID cannot exceed 64 characters"
			}
		case "type":
			if !gweV4Has(gweV4VariableTypes, v) {
				msg = "Invalid variable type"
			}
		case "label":
			msg = gweV4CheckLabel(v)
		case "value":
			msg = gweV4CheckValue(v)
		}
		if msg != "" {
			gweV4ParamError(c, key, msg)
			return nil, false
		}
		out[key] = v
	}
	return out, true
}

func gweV4VarOK(c *gin.Context, status int, payload any) {
	c.JSON(status, map[string]any{"success": true, "data": payload})
}

func gweV4VarActive(set *gweV4VariableSet) int {
	n := 0
	for _, v := range set.Data {
		if _, del := v["deleted_at"]; !del {
			n++
		}
	}
	return n
}

func gweV4VarUniqueLabel(set *gweV4VariableSet, label, ignore string) error {
	for id, v := range set.Data {
		if _, del := v["deleted_at"]; del || id == ignore {
			continue
		}
		if l, _ := v["label"].(string); strings.EqualFold(l, label) {
			return gweV4ErrDupLabel
		}
	}
	return nil
}

func gweV4VarNextOrder(set *gweV4VariableSet) int {
	high := 0
	for _, v := range set.Data {
		if _, del := v["deleted_at"]; del {
			continue
		}
		if o, ok := v["order"].(float64); ok && int(o) > high {
			high = int(o)
		} else if o, ok := v["order"].(int); ok && o > high {
			high = o
		}
	}
	return high + 1
}

// gweV4VarWithID is the response shape of one variable: its fields plus id.
func gweV4VarWithID(id string, v map[string]any, extra map[string]any) map[string]any {
	out := map[string]any{"id": id}
	for k, x := range v {
		out[k] = x
	}
	for k, x := range extra {
		out[k] = x
	}
	return out
}

func gweV4VarCopy(v map[string]any) map[string]any {
	out := map[string]any{}
	for k, x := range v {
		out[k] = x
	}
	return out
}

// Variable operations on a loaded set (shared by the single routes and batch).

func gweV4VarCreate(set *gweV4VariableSet, data map[string]any) (string, error) {
	if gweV4VarActive(set) >= gweV4MaxVariables {
		return "", gweV4ErrLimit
	}
	label, _ := data["label"].(string)
	if err := gweV4VarUniqueLabel(set, label, ""); err != nil {
		return "", err
	}
	if msg := gweV4CheckLabel(label); msg != "" {
		return "", &gweV4VarError{http.StatusInternalServerError, "unexpected_server_error", "Unexpected server error", msg}
	}
	id := gweV4NewVariableID(set.Data)
	now := gweV4Now()
	v := map[string]any{"type": data["type"], "label": label, "value": data["value"], "created_at": now, "updated_at": now}
	if o, ok := data["order"]; ok && o != nil {
		v["order"] = o
	} else {
		v["order"] = gweV4VarNextOrder(set)
	}
	set.Data[id] = v
	return id, nil
}

func gweV4VarApply(v map[string]any, data map[string]any) error {
	if t, ok := data["type"].(string); ok && t != "" && t != v["type"] {
		sizes := []string{"global-size-variable", "global-custom-size-variable"}
		cur, _ := v["type"].(string)
		if !gweV4Has(sizes, cur) || !gweV4Has(sizes, t) {
			return gweV4ErrType
		}
		v["type"] = t
	}
	for _, f := range []string{"label", "value", "order", "sync_to_v3"} {
		if x, ok := data[f]; ok && x != nil && x != "" {
			v[f] = x
		}
	}
	if l, _ := v["label"].(string); gweV4CheckLabel(l) != "" {
		return &gweV4VarError{http.StatusInternalServerError, "unexpected_server_error", "Unexpected server error", gweV4CheckLabel(l)}
	}
	v["updated_at"] = gweV4Now()
	return nil
}

func gweV4VarUpdate(set *gweV4VariableSet, id string, data map[string]any) error {
	v, ok := set.Data[id]
	if !ok {
		return gweV4ErrNotFound
	}
	if l, ok := data["label"].(string); ok && l != "" {
		if err := gweV4VarUniqueLabel(set, l, id); err != nil {
			return err
		}
	}
	return gweV4VarApply(v, data)
}

func gweV4VarDelete(set *gweV4VariableSet, id string) error {
	v, ok := set.Data[id]
	if !ok {
		return gweV4ErrNotFound
	}
	v["deleted_at"] = gweV4Now()
	return nil
}

func gweV4VarRestore(set *gweV4VariableSet, id string, data map[string]any) error {
	v, ok := set.Data[id]
	if !ok {
		return gweV4ErrNotFound
	}
	if gweV4VarActive(set) >= gweV4MaxVariables {
		return gweV4ErrLimit
	}
	label, _ := v["label"].(string)
	if l, ok := data["label"].(string); ok && l != "" {
		label = l
	}
	if err := gweV4VarUniqueLabel(set, label, id); err != nil {
		return err
	}
	if err := gweV4VarApply(v, data); err != nil {
		return err
	}
	delete(v, "deleted_at")
	delete(v, "deleted")
	return nil
}

func (s *Server) gweV4VarLoad(c *gin.Context) (int64, *gweV4VariableSet, bool) {
	kitID, ok := s.gweV4KitID(c)
	if !ok {
		return 0, nil, false
	}
	set, err := s.gweV4LoadVariables(c, kitID)
	if err != nil {
		gweV4VarFail(c, err)
		return 0, nil, false
	}
	return kitID, set, true
}

func (s *Server) gweV4VarList(c *gin.Context, _ *restRequest) {
	kitID, ok := s.gweV4KitID(c)
	if !ok {
		return
	}
	data, watermark, err := s.gweV4Variables(c, kitID)
	if err != nil {
		gweV4VarFail(c, err)
		return
	}
	var vars any = data
	if len(data) == 0 {
		vars = []any{} // PHP's empty array
	}
	gweV4VarOK(c, http.StatusOK, map[string]any{"variables": vars, "total": len(data), "watermark": watermark})
}

func (s *Server) gweV4VarCreate(c *gin.Context, r *restRequest) {
	p, ok := gweV4VarParams(c, r, "type", "label", "value")
	if !ok {
		return
	}
	kitID, set, ok := s.gweV4VarLoad(c)
	if !ok {
		return
	}
	id, err := gweV4VarCreate(set, map[string]any{"type": p["type"], "label": p["label"], "value": p["value"]})
	if err != nil {
		gweV4VarFail(c, err)
		return
	}
	watermark, err := s.gweV4SaveVariables(c, kitID, set)
	if err != nil {
		gweV4VarFail(c, err)
		return
	}
	gweV4VarOK(c, http.StatusCreated, map[string]any{"variable": gweV4VarWithID(id, set.Data[id], nil), "watermark": watermark})
}

func (s *Server) gweV4VarUpdate(c *gin.Context, r *restRequest) {
	p, ok := gweV4VarParams(c, r, "id", "label", "value")
	if !ok {
		return
	}
	data := map[string]any{"label": p["label"], "value": p["value"]}
	if t := p["type"]; t != "" {
		data["type"] = t
	}
	if o, ok := r.Int(c, "order"); ok {
		if o < 0 {
			gweV4ParamError(c, "order", "Order must be a non-negative integer")
			return
		}
		data["order"] = o
	}
	kitID, set, ok := s.gweV4VarLoad(c)
	if !ok {
		return
	}
	if err := gweV4VarUpdate(set, p["id"], data); err != nil {
		gweV4VarFail(c, err)
		return
	}
	watermark, err := s.gweV4SaveVariables(c, kitID, set)
	if err != nil {
		gweV4VarFail(c, err)
		return
	}
	gweV4VarOK(c, http.StatusOK, map[string]any{"variable": gweV4VarWithID(p["id"], set.Data[p["id"]], nil), "watermark": watermark})
}

func (s *Server) gweV4VarDelete(c *gin.Context, r *restRequest) {
	p, ok := gweV4VarParams(c, r, "id")
	if !ok {
		return
	}
	kitID, set, ok := s.gweV4VarLoad(c)
	if !ok {
		return
	}
	if err := gweV4VarDelete(set, p["id"]); err != nil {
		gweV4VarFail(c, err)
		return
	}
	watermark, err := s.gweV4SaveVariables(c, kitID, set)
	if err != nil {
		gweV4VarFail(c, err)
		return
	}
	gweV4VarOK(c, http.StatusOK, map[string]any{"variable": gweV4VarWithID(p["id"], set.Data[p["id"]], map[string]any{"deleted": true}), "watermark": watermark})
}

func (s *Server) gweV4VarRestore(c *gin.Context, r *restRequest) {
	p, ok := gweV4VarParams(c, r, "id")
	if !ok {
		return
	}
	data := map[string]any{}
	for _, k := range []string{"label", "value", "type"} {
		if v := p[k]; v != "" {
			data[k] = v
		}
	}
	kitID, set, ok := s.gweV4VarLoad(c)
	if !ok {
		return
	}
	if err := gweV4VarRestore(set, p["id"], data); err != nil {
		gweV4VarFail(c, err)
		return
	}
	watermark, err := s.gweV4SaveVariables(c, kitID, set)
	if err != nil {
		gweV4VarFail(c, err)
		return
	}
	gweV4VarOK(c, http.StatusOK, map[string]any{"variable": gweV4VarWithID(p["id"], set.Data[p["id"]], nil), "watermark": watermark})
}

// gweV4VarBatchErrors maps an error code to the batch error code, message and per-item answer.
var gweV4VarBatchErrors = []struct{ code, batchCode, batchMsg string }{
	{"invalid_variable_limit_reached", "batch_variables_limit_reached", "Batch operation failed: Reached the maximum number of variables"},
	{"duplicated_label", "batch_duplicated_label", "Batch operation failed: Variable labels already exist"},
	{"variable_not_found", "batch_variables_not_found", "Batch operation failed: Variables not found"},
}

func (s *Server) gweV4VarBatch(c *gin.Context, r *restRequest) {
	if w, ok := r.Body["watermark"].(float64); !ok || w < 0 {
		restErr(c, http.StatusBadRequest, "rest_invalid_param", "Invalid parameter(s): watermark",
			map[string]any{"params": map[string]string{"watermark": "Watermark must be a non-negative integer"}, "details": map[string]any{}})
		return
	}
	ops, ok := r.Body["operations"].([]any)
	if !ok || len(ops) == 0 {
		gweV4ParamError(c, "operations", "Operations array cannot be empty")
		return
	}
	for i, o := range ops {
		m, ok := o.(map[string]any)
		t, _ := m["type"].(string)
		if !ok || t == "" {
			gweV4ParamError(c, "operations", fmt.Sprintf("Invalid operation structure at index %d", i))
			return
		}
		if !gweV4Has([]string{"create", "update", "delete", "restore", "reorder"}, t) {
			gweV4ParamError(c, "operations", fmt.Sprintf("Invalid operation type at index %d", i))
			return
		}
	}
	kitID, set, ok := s.gweV4VarLoad(c)
	if !ok {
		return
	}
	results := []any{}
	errs := map[string]*gweV4VarError{}
	var errOrder []string
	for i, o := range ops {
		op := o.(map[string]any)
		typ := op["type"].(string)
		id, _ := op["id"].(string)
		variable, _ := op["variable"].(map[string]any)
		opID := id
		if typ == "create" {
			if tmp, ok := variable["id"].(string); ok && tmp != "" {
				opID = tmp
			}
		}
		if opID == "" {
			opID = fmt.Sprintf("operation_%d", i)
		}
		var err error
		switch typ {
		case "create":
			if variable == nil {
				variable = map[string]any{}
			}
			var newID string
			newID, err = gweV4VarCreate(set, variable)
			if err == nil {
				results = append(results, map[string]any{"type": "create", "id": newID, "temp_id": variable["id"], "variable": gweV4VarCopy(set.Data[newID])})
			}
		case "update":
			if variable == nil {
				variable = map[string]any{}
			}
			if err = gweV4VarUpdate(set, id, variable); err == nil {
				results = append(results, map[string]any{"type": "update", "id": id, "variable": gweV4VarCopy(set.Data[id])})
			}
		case "delete":
			if err = gweV4VarDelete(set, id); err == nil {
				results = append(results, map[string]any{"type": "delete", "id": id, "deleted": true})
			}
		case "restore":
			if err = gweV4VarRestore(set, id, op); err == nil {
				results = append(results, map[string]any{"type": "restore", "id": id, "variable": gweV4VarCopy(set.Data[id])})
			}
		default: // reorder: listed by the validator, not implemented upstream either
			err = &gweV4VarError{http.StatusBadRequest, "batch_operation_failed", "Unexpected error", "Invalid operation type: " + typ}
		}
		if err != nil {
			e, ok := err.(*gweV4VarError)
			if !ok {
				e = &gweV4VarError{http.StatusInternalServerError, "unexpected_server_error", "Unexpected error", err.Error()}
			}
			if _, seen := errs[opID]; !seen {
				errOrder = append(errOrder, opID)
			}
			errs[opID] = e
		}
	}
	if len(errs) > 0 {
		for _, be := range gweV4VarBatchErrors {
			filtered := map[string]any{}
			for _, k := range errOrder {
				if errs[k].code == be.code {
					filtered[k] = map[string]any{"status": errs[k].status, "message": errs[k].msg}
				}
			}
			if len(filtered) > 0 {
				c.JSON(http.StatusBadRequest, map[string]any{"success": false, "code": be.batchCode, "message": be.batchMsg, "data": filtered})
				return
			}
		}
		unknown := map[string]any{}
		for _, k := range errOrder {
			unknown[k] = map[string]any{"status": http.StatusInternalServerError, "message": errs[k].description}
		}
		c.JSON(http.StatusBadRequest, map[string]any{"success": false, "code": "batch_operation_failed", "message": "Batch operation failed", "data": unknown})
		return
	}
	watermark, err := s.gweV4SaveVariables(c, kitID, set)
	if err != nil {
		gweV4VarFail(c, err)
		return
	}
	gweV4VarOK(c, http.StatusOK, map[string]any{"success": true, "results": results, "watermark": watermark})
}

// --- components ----------------------------------------------------------------------------------------

func (s *Server) gweV4ComponentsList(c *gin.Context, _ *restRequest) {
	posts, err := s.gweV4Components(c, true)
	if err != nil {
		gweV4Fail(c, err)
		return
	}
	list := make([]map[string]any, 0, len(posts))
	for _, p := range posts {
		list = append(list, map[string]any{"id": p.ID, "name": p.Title, "uid": gweV4ComponentUID(p), "isArchived": gweV4ComponentArchived(p)})
	}
	gweV4Data(c, http.StatusOK, list, nil)
}

func (s *Server) gweV4ComponentsStyles(c *gin.Context, _ *restRequest) {
	posts, err := s.gweV4Components(c, true)
	if err != nil {
		gweV4Fail(c, err)
		return
	}
	out := map[string]any{}
	for _, p := range posts {
		var styles any = gweV4ComponentStyles(p)
		if len(styles.(map[string]any)) == 0 {
			styles = []any{} // PHP's empty array
		}
		out[itoa64(p.ID)] = styles
	}
	var data any = out
	if len(out) == 0 {
		data = []any{}
	}
	gweV4Data(c, http.StatusOK, data, nil)
}

type gweV4ComponentItem struct {
	UID, Title string
	Elements   []any
	Settings   map[string]any
}

// gweV4ComponentItems validates the items param and the upstream save checks; it answers the
// error itself and returns false on failure.
func (s *Server) gweV4ComponentItems(c *gin.Context, r *restRequest) ([]gweV4ComponentItem, bool) {
	raw, ok := r.Body["items"].([]any)
	if !ok {
		restErr(c, http.StatusBadRequest, "rest_invalid_param", "Invalid parameter(s): items", map[string]any{"params": map[string]string{"items": "items is not of type array."}})
		return nil, false
	}
	var items []gweV4ComponentItem
	for i, it := range raw {
		m, _ := it.(map[string]any)
		uid, uidOK := m["uid"].(string)
		title, titleOK := m["title"].(string)
		els, elsOK := m["elements"].([]any)
		param := ""
		switch {
		case m == nil:
			param = fmt.Sprintf("items[%d] is not of type object.", i)
		case !uidOK:
			param = fmt.Sprintf("items[%d][uid] is a required property of items[%d].", i, i)
		case !titleOK:
			param = fmt.Sprintf("items[%d][title] is a required property of items[%d].", i, i)
		case len([]rune(title)) < 2:
			param = fmt.Sprintf("items[%d][title] must be at least 2 characters long.", i)
		case len([]rune(title)) > 200:
			param = fmt.Sprintf("items[%d][title] must be at most 200 characters long.", i)
		case !elsOK:
			param = fmt.Sprintf("items[%d][elements] is not of type array.", i)
		}
		if param != "" {
			restErr(c, http.StatusBadRequest, "rest_invalid_param", "Invalid parameter(s): items", map[string]any{"params": map[string]string{"items": param}})
			return nil, false
		}
		settings, _ := m["settings"].(map[string]any)
		items = append(items, gweV4ComponentItem{UID: uid, Title: strings.TrimSpace(title), Elements: els, Settings: settings})
	}

	existing, err := s.gweV4Components(c, true)
	if err != nil {
		gweV4Fail(c, err)
		return nil, false
	}
	active := 0
	for _, p := range existing {
		if !gweV4ComponentArchived(p) {
			active++
		}
	}
	var msgs []string
	addMsg := func(m string) {
		if !gweV4Has(msgs, m) {
			msgs = append(msgs, m)
		}
	}
	if active+len(items) > gweV4MaxComponent {
		addMsg("Maximum number of components exceeded.")
	}
	for _, it := range items {
		for _, p := range existing {
			if !gweV4ComponentArchived(p) && p.Title == it.Title {
				addMsg(fmt.Sprintf("Component title '%s' is duplicated.", it.Title))
			}
		}
		dupUID := false
		for _, p := range existing {
			if gweV4ComponentUID(p) == it.UID {
				dupUID = true
			}
		}
		n := 0
		for _, other := range items {
			if other.UID == it.UID {
				n++
			}
		}
		if dupUID || n > 1 {
			addMsg(fmt.Sprintf("Component uid '%s' is duplicated.", it.UID))
		}
	}
	if len(msgs) > 0 {
		gweV4Err(c, http.StatusUnprocessableEntity, "components_validation_failed", "Validation failed: "+strings.Join(msgs, ", "), nil)
		return nil, false
	}
	for _, it := range items {
		if bad := gweV4NonAtomic(it.Elements); len(bad) > 0 {
			gweV4Err(c, http.StatusUnprocessableEntity, "non_atomic_element_in_component",
				"Components require atomic elements only. Remove widgets to create this component.", map[string]any{"non_atomic_elements": bad})
			return nil, false
		}
	}
	for _, it := range items {
		if op, ok := it.Settings["overridable_props"]; ok && op != nil {
			if _, isObj := op.(map[string]any); !isObj {
				gweV4Err(c, http.StatusUnprocessableEntity, "settings_validation_failed",
					`Settings validation failed: {"`+it.UID+`":"Validation failed for overridable_props: invalid_type"}`, nil)
				return nil, false
			}
		}
	}
	return items, true
}

func gweV4ComponentStatus(c *gin.Context, r *restRequest) (string, bool) {
	st, _ := r.Str(c, "status")
	if st != "publish" && st != "draft" && st != "autosave" {
		restErr(c, http.StatusBadRequest, "rest_invalid_param", "Invalid parameter(s): status",
			map[string]any{"params": map[string]string{"status": "status is not one of publish, draft, and autosave."}})
		return "", false
	}
	return st, true
}

func (s *Server) gweV4ComponentsCreate(c *gin.Context, r *restRequest) {
	status, ok := gweV4ComponentStatus(c, r)
	if !ok {
		return
	}
	items, ok := s.gweV4ComponentItems(c, r)
	if !ok {
		return
	}
	if err := s.gweV4EnsureComponentType(c); err != nil {
		gweV4Fail(c, err)
		return
	}
	if status == "autosave" {
		status = "draft"
	}
	// Saving each component document validates its elements (Components_REST_API::create_components
	// collects the per-uid exceptions into one settings_validation_failed error).
	failed := map[string]string{}
	for i, it := range items {
		parsed, msg := gweV4ParseComponentElements(it.Elements)
		if msg != "" {
			failed[it.UID] = msg
			continue
		}
		items[i].Elements = parsed
	}
	if len(failed) > 0 {
		b, _ := json.Marshal(failed)
		gweV4Err(c, http.StatusUnprocessableEntity, "settings_validation_failed", "Settings validation failed: "+string(b), nil)
		return
	}
	u := restCurrent(c)
	created := map[string]any{}
	for _, it := range items {
		els, _ := json.Marshal(it.Elements)
		meta := map[string]any{
			"_elementor_edit_mode": "builder", "_elementor_template_type": gweComponentType, "_elementor_version": gweVersion,
			"_elementor_data": string(els), "_elementor_component_uid": it.UID,
		}
		if op, ok := it.Settings["overridable_props"].(map[string]any); ok {
			meta["_elementor_component_overridable_props"] = op
		}
		p := &store.Post{Type: gweComponentType, Title: it.Title, Status: status, AuthorID: u.ID, CommentStatus: "closed", PingStatus: "closed", Meta: meta}
		if err := s.store.InsertPost(c, p); err != nil {
			gweV4Fail(c, err)
			return
		}
		created[it.UID] = p.ID
	}
	gweV4Data(c, http.StatusCreated, created, nil)
}

func (s *Server) gweV4ComponentsValidate(c *gin.Context, r *restRequest) {
	if _, ok := s.gweV4ComponentItems(c, r); !ok {
		return
	}
	gweV4Data(c, http.StatusOK, nil, nil)
}

func (s *Server) gweV4ComponentsOverridable(c *gin.Context, r *restRequest) {
	ids, ok := r.IDs(c, "componentIds")
	if !ok {
		restErr(c, http.StatusBadRequest, "rest_missing_callback_param", "Missing parameter(s): componentIds", map[string]any{"params": []string{"componentIds"}})
		return
	}
	data := map[string]any{}
	errs := map[string]any{}
	for _, id := range ids {
		p := s.gweV4Component(c, id)
		if p == nil {
			errs[itoa64(id)] = "component_not_found"
			continue
		}
		data[itoa64(id)] = gweV4ComponentOverridableProps(p)
	}
	gweV4Data(c, http.StatusOK, data, map[string]any{"errors": errs})
}

func gweV4BodyIDs(v any) []int64 {
	list, _ := v.([]any)
	var out []int64
	for _, x := range list {
		switch t := x.(type) {
		case float64:
			out = append(out, int64(t))
		case string:
			var n int64
			if _, err := fmt.Sscan(t, &n); err == nil {
				out = append(out, n)
			}
		}
	}
	return out
}

func (s *Server) gweV4ComponentsStatus(c *gin.Context, r *restRequest) {
	if st, _ := r.Str(c, "status"); st != "publish" {
		restErr(c, http.StatusBadRequest, "rest_invalid_param", "Invalid parameter(s): status",
			map[string]any{"params": map[string]string{"status": "status is not one of publish."}})
		return
	}
	res := map[string][]int64{"success": {}, "failed": {}}
	for _, id := range gweV4BodyIDs(r.Body["ids"]) {
		p := s.gweV4Component(c, id)
		if p == nil {
			res["failed"] = append(res["failed"], id)
			continue
		}
		p.Status = "publish"
		if err := s.store.UpdatePost(c, p, restCurrent(c).ID); err != nil {
			res["failed"] = append(res["failed"], id)
			continue
		}
		res["success"] = append(res["success"], id)
	}
	gweV4Data(c, http.StatusOK, res, nil)
}

func (s *Server) gweV4ComponentsArchive(c *gin.Context, r *restRequest) {
	if _, ok := gweV4ComponentStatus(c, r); !ok {
		return
	}
	res := map[string][]int64{"failedIds": {}, "successIds": {}}
	for _, id := range gweV4BodyIDs(r.Body["componentIds"]) {
		if s.gweV4Component(c, id) == nil ||
			s.gweV4SetPostMeta(c, id, "_elementor_component_is_archived", map[string]any{"is_archived": true, "archived_at": time.Now().Unix()}) != nil {
			res["failedIds"] = append(res["failedIds"], id)
			continue
		}
		res["successIds"] = append(res["successIds"], id)
	}
	gweV4Data(c, http.StatusOK, res, nil)
}

func (s *Server) gweV4ComponentsTitles(c *gin.Context, r *restRequest) {
	if _, ok := gweV4ComponentStatus(c, r); !ok {
		return
	}
	list, _ := r.Body["components"].([]any)
	res := map[string][]int64{"failedIds": {}, "successIds": {}}
	for _, it := range list {
		m, _ := it.(map[string]any)
		ids := gweV4BodyIDs([]any{m["componentId"]})
		if len(ids) == 0 {
			continue
		}
		id := ids[0]
		title, _ := m["title"].(string)
		title = strings.TrimSpace(title)
		if title == "" || s.gweV4Component(c, id) == nil {
			res["failedIds"] = append(res["failedIds"], id)
			continue
		}
		if _, err := s.store.Pool.Exec(c, `UPDATE posts SET title = $2, modified_at = now() WHERE id = $1`, id, title); err != nil {
			res["failedIds"] = append(res["failedIds"], id)
			continue
		}
		res["successIds"] = append(res["successIds"], id)
	}
	gweV4Data(c, http.StatusOK, res, nil)
}

func gweV4ComponentIDParam(c *gin.Context, r *restRequest) int64 {
	id, _ := r.Int(c, "componentId")
	return id
}

func (s *Server) gweV4ComponentLock(c *gin.Context, r *restRequest) {
	p := s.gweV4Component(c, gweV4ComponentIDParam(c, r))
	uid := restCurrent(c).ID
	if p != nil {
		if l := gweV4LockOf(p); l.LockedBy == 0 || l.LockedBy == uid {
			if s.gweV4SetPostMeta(c, p.ID, "_elementor_component_lock", map[string]any{"locked_by": uid, "locked_at": time.Now().Unix()}) == nil {
				gweV4Data(c, http.StatusOK, map[string]any{"locked": true}, nil)
				return
			}
		}
	}
	gweV4Err(c, http.StatusInternalServerError, "lock_failed", "Failed to lock component", nil)
}

func (s *Server) gweV4ComponentUnlock(c *gin.Context, r *restRequest) {
	p := s.gweV4Component(c, gweV4ComponentIDParam(c, r))
	if p != nil {
		if l := gweV4LockOf(p); l.LockedBy == 0 || l.LockedBy == restCurrent(c).ID {
			if s.gweV4SetPostMeta(c, p.ID, "_elementor_component_lock", nil) == nil {
				gweV4Data(c, http.StatusOK, map[string]any{"unlocked": true}, nil)
				return
			}
		}
	}
	gweV4Err(c, http.StatusInternalServerError, "unlock_failed", "Failed to unlock component", nil)
}

func (s *Server) gweV4ComponentLockStatus(c *gin.Context, r *restRequest) {
	p := s.gweV4Component(c, gweV4ComponentIDParam(c, r))
	if p == nil {
		gweV4Err(c, http.StatusInternalServerError, "get_lock_status_failed", "Failed to get lock status", nil)
		return
	}
	l := gweV4LockOf(p)
	if l.LockedBy == 0 {
		gweV4Data(c, http.StatusOK, map[string]any{"is_current_user_allow_to_edit": true, "locked_by": nil}, nil)
		return
	}
	name := ""
	if u, err := s.store.UserByID(c, l.LockedBy); err == nil {
		name = u.DisplayName
	}
	gweV4Data(c, http.StatusOK, map[string]any{"is_current_user_allow_to_edit": l.LockedBy == restCurrent(c).ID, "locked_by": name}, nil)
}

// --- kit element defaults ----------------------------------------------------------------------------

func (s *Server) gweV4ElementsDefaults(c *gin.Context, _ *restRequest) {
	kit, err := s.gweKit(c)
	if err != nil {
		gweV4Fail(c, err)
		return
	}
	c.JSON(http.StatusOK, gweV4KitElementsDefaults(kit))
}

func (s *Server) gweV4ElementsDefaultsWrite(c *gin.Context, typ string, settings map[string]any) {
	kit, err := s.gweKit(c)
	if err != nil {
		restErr(c, http.StatusNotFound, "rest_not_found", "Kit doesn't exist.")
		return
	}
	data := gweV4KitElementsDefaults(kit)
	if settings == nil {
		delete(data, typ)
	} else {
		data[typ] = settings
	}
	if err := s.gweV4SetKitMeta(c, kit.ID, "_elementor_elements_default_values", data); err != nil {
		gweV4Fail(c, err)
		return
	}
	c.JSON(http.StatusOK, map[string]any{})
}

func (s *Server) gweV4ElementsDefaultsPut(c *gin.Context, r *restRequest) {
	settings, ok := r.Body["settings"].(map[string]any)
	if !ok {
		if l, isList := r.Body["settings"].([]any); isList && len(l) == 0 {
			settings = map[string]any{}
		} else {
			restErr(c, http.StatusBadRequest, "rest_invalid_param", "Invalid parameter(s): settings",
				map[string]any{"params": map[string]string{"settings": "settings is not of type object."}})
			return
		}
	}
	s.gweV4ElementsDefaultsWrite(c, r.Params["type"], settings)
}

func (s *Server) gweV4ElementsDefaultsDelete(c *gin.Context, r *restRequest) {
	s.gweV4ElementsDefaultsWrite(c, r.Params["type"], nil)
}

// --- default styles ----------------------------------------------------------------------------------

func (s *Server) gweV4DefaultStylesList(c *gin.Context, _ *restRequest) {
	kitID, ok := s.gweV4KitID(c)
	if !ok {
		return
	}
	items, err := s.gweV4DefaultStyles(c, kitID)
	if err != nil {
		gweV4Fail(c, err)
		return
	}
	gweV4Data(c, http.StatusOK, items, nil)
}

func gweV4TagOK(c *gin.Context, tag string) bool {
	if !gweV4DefaultStyleTag(tag) {
		gweV4Err(c, http.StatusBadRequest, "invalid_tag", "Invalid HTML tag.", nil)
		return false
	}
	return true
}

func (s *Server) gweV4DefaultStyleGet(c *gin.Context, r *restRequest) {
	tag := r.Params["tag"]
	if !gweV4TagOK(c, tag) {
		return
	}
	kitID, ok := s.gweV4KitID(c)
	if !ok {
		return
	}
	items, err := s.gweV4DefaultStyles(c, kitID)
	if err != nil {
		gweV4Fail(c, err)
		return
	}
	item, ok := items[tag]
	if !ok {
		gweV4Err(c, http.StatusNotFound, "not_found", "Default style not found.", nil)
		return
	}
	gweV4Data(c, http.StatusOK, item, nil)
}

func (s *Server) gweV4DefaultStylePut(c *gin.Context, r *restRequest) {
	tag := r.Params["tag"]
	variants, ok := r.Body["variants"].([]any)
	if !ok {
		restErr(c, http.StatusBadRequest, "rest_invalid_param", "Invalid parameter(s): variants",
			map[string]any{"params": map[string]string{"variants": "variants is not of type array."}})
		return
	}
	if t, present := r.Body["type"]; present && t != "class" {
		restErr(c, http.StatusBadRequest, "rest_invalid_param", "Invalid parameter(s): type",
			map[string]any{"params": map[string]string{"type": "type is not one of class."}})
		return
	}
	if !gweV4TagOK(c, tag) {
		return
	}
	kitID, ok := s.gweV4KitID(c)
	if !ok {
		return
	}
	if err := s.gweV4PutDefaultStyle(c, kitID, tag, variants); err != nil {
		gweV4Err(c, http.StatusInternalServerError, "persist_failed", "Failed to save default style.", nil)
		return
	}
	gweV4Data(c, http.StatusOK, gweV4DefaultStyleItem(tag, variants), nil)
}

func (s *Server) gweV4DefaultStyleDelete(c *gin.Context, r *restRequest) {
	tag := r.Params["tag"]
	if !gweV4TagOK(c, tag) {
		return
	}
	kitID, ok := s.gweV4KitID(c)
	if !ok {
		return
	}
	if err := s.gweV4DeleteDefaultStyle(c, kitID, tag); err != nil {
		gweV4Fail(c, err)
		return
	}
	gweV4Data(c, http.StatusOK, map[string]any{"deleted": true}, nil)
}
