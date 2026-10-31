package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"
)

func TestGoWebEditorV4REST(t *testing.T) {
	ts, c := testServer(t)
	base := ts.URL
	if code, body, _ := post(t, c, base+"/wp-admin/install?step=2", url.Values{
		"weblog_title": {"Atomic Site"}, "user_name": {"admin"}, "admin_password": {"correct horse"}, "admin_email": {"admin@example.test"},
	}); code != http.StatusOK || !strings.Contains(body, "Success!") {
		t.Fatalf("install: %d", code)
	}
	if code, _, _ := post(t, c, base+"/wp-login", url.Values{"log": {"admin"}, "pwd": {"correct horse"}}); code != http.StatusFound {
		t.Fatalf("login: %d", code)
	}
	var pageID string
	if err := getPool(t).QueryRow(context.Background(), `SELECT id::text FROM posts WHERE type='page' AND title='Sample Page'`).Scan(&pageID); err != nil {
		t.Fatal(err)
	}
	_, body, _ := get(t, c, base+"/wp-admin/post?post="+pageID+"&action=web-builder")
	m := regexp.MustCompile(`"X-WP-Nonce":"([^"]+)"`).FindStringSubmatch(body)
	if m == nil {
		m = regexp.MustCompile(`"nonce":"([^"]+)"`).FindStringSubmatch(body)
	}
	if m == nil {
		t.Fatal("no REST nonce on the editor page")
	}
	nonce := m[1]

	call := func(method, path string, payload any) (int, map[string]any, string) {
		t.Helper()
		var rd io.Reader
		if payload != nil {
			b, _ := json.Marshal(payload)
			rd = bytes.NewReader(b)
		}
		req, _ := http.NewRequest(method, base+"/wp-json/elementor/v1/"+path, rd)
		req.Header.Set("X-WP-Nonce", nonce)
		if payload != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		resp, err := c.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		raw, _ := io.ReadAll(resp.Body)
		var out map[string]any
		_ = json.Unmarshal(raw, &out)
		return resp.StatusCode, out, string(raw)
	}
	obj := func(v any) map[string]any { m, _ := v.(map[string]any); return m }

	// --- global classes ---
	variant := func(color string) []any {
		return []any{map[string]any{"meta": map[string]any{"breakpoint": "desktop", "state": nil},
			"props": map[string]any{"color": map[string]any{"$$type": "color", "value": color}}, "custom_css": nil}}
	}
	class := func(id, label, color string) map[string]any {
		return map[string]any{"id": id, "type": "class", "label": label, "variants": variant(color)}
	}
	code, _, raw := call(http.MethodPut, "global-classes?context=frontend", map[string]any{
		"changes": map[string]any{"added": []string{"g-aaa", "g-bbb"}, "deleted": []string{}, "modified": []string{}, "order": true},
		"items":   map[string]any{"g-aaa": class("g-aaa", "card", "#f00"), "g-bbb": class("g-bbb", "accent", "#0f0")},
		"order":   []string{"g-bbb", "g-aaa"},
	})
	if code != http.StatusNoContent {
		t.Fatalf("put classes: %d %s", code, raw)
	}
	_, _, raw = call(http.MethodGet, "global-classes", nil)
	if raw != `{"data":[{"id":"g-bbb","label":"accent"},{"id":"g-aaa","label":"card"}],"meta":[]}` {
		t.Errorf("classes list: %s", raw)
	}
	// A preview edit is visible in the preview only.
	code, _, raw = call(http.MethodPut, "global-classes?context=preview", map[string]any{
		"changes": map[string]any{"added": []string{"g-ccc"}, "deleted": []string{}, "modified": []string{"g-aaa"}},
		"items":   map[string]any{"g-ccc": class("g-ccc", "draft", "#00f"), "g-aaa": class("g-aaa", "card2", "#f00")},
		"order":   []string{"g-ccc", "g-bbb", "g-aaa"},
	})
	if code != http.StatusNoContent {
		t.Fatalf("put preview: %d %s", code, raw)
	}
	_, _, raw = call(http.MethodGet, "global-classes?context=preview", nil)
	if raw != `{"data":[{"id":"g-ccc","label":"draft"},{"id":"g-bbb","label":"accent"},{"id":"g-aaa","label":"card2"}],"meta":[]}` {
		t.Errorf("preview list: %s", raw)
	}
	_, _, raw = call(http.MethodGet, "global-classes", nil)
	if !strings.Contains(raw, `"card"`) || strings.Contains(raw, "g-ccc") {
		t.Errorf("frontend list after preview edit: %s", raw)
	}
	// Styles by id.
	_, out, raw := call(http.MethodGet, "global-classes/styles?ids=g-aaa,nope", nil)
	if obj(out["data"])["nope"] != nil || obj(obj(out["data"])["g-aaa"])["label"] != "card" || !strings.Contains(raw, `"order":["g-aaa"]`) {
		t.Errorf("styles: %s", raw)
	}
	// Duplicate label on add -> DUP_ rename.
	code, out, raw = call(http.MethodPut, "global-classes", map[string]any{
		"changes": map[string]any{"added": []string{"g-ddd"}, "deleted": []string{}, "modified": []string{}},
		"items":   map[string]any{"g-ddd": class("g-ddd", "card", "#123")},
		"order":   []string{"g-bbb", "g-aaa", "g-ddd"},
	})
	ml := obj(obj(obj(out["data"])["modifiedLabels"])["g-ddd"])
	if code != http.StatusOK || obj(out["data"])["code"] != "DUPLICATED_LABEL" || ml["original"] != "card" || ml["modified"] != "DUP_card" {
		t.Errorf("duplicate label: %d %s", code, raw)
	}
	// Invalid items.
	code, out, raw = call(http.MethodPut, "global-classes", map[string]any{
		"changes": map[string]any{"added": []string{"g-eee"}, "deleted": []string{}, "modified": []string{}},
		"items":   map[string]any{"g-eee": map[string]any{"id": "g-eee", "type": "class", "label": "x", "variants": []any{map[string]any{"props": map[string]any{}}}}},
		"order":   []string{},
	})
	if code != http.StatusBadRequest || out["code"] != "invalid_items" {
		t.Errorf("invalid items: %d %s", code, raw)
	}
	// Delete one; the published order is propagated to the preview.
	if code, _, raw = call(http.MethodPut, "global-classes", map[string]any{
		"changes": map[string]any{"added": []string{}, "deleted": []string{"g-bbb"}, "modified": []string{}},
		"items":   map[string]any{}, "order": []string{"g-aaa", "g-ddd"},
	}); code != http.StatusNoContent {
		t.Fatalf("delete class: %d %s", code, raw)
	}
	_, _, raw = call(http.MethodGet, "global-classes", nil)
	if raw != `{"data":[{"id":"g-aaa","label":"card"},{"id":"g-ddd","label":"DUP_card"}],"meta":[]}` {
		t.Errorf("after delete: %s", raw)
	}
	_, _, raw = call(http.MethodGet, "global-classes?context=preview", nil)
	if strings.Contains(raw, "g-bbb") || !strings.Contains(raw, "g-ccc") {
		t.Errorf("preview after delete: %s", raw)
	}
	// Classes used by a page, and usage.
	tree := `[{"id":"e1","elType":"e-div-block","settings":{"classes":{"$$type":"classes","value":["g-aaa","e1-local"]}},"elements":[` +
		`{"id":"e2","elType":"widget","widgetType":"e-heading","settings":{"classes":{"$$type":"classes","value":["g-aaa"]}},"elements":[]}]}]`
	if _, err := getPool(t).Exec(context.Background(), `UPDATE posts SET meta = meta || jsonb_build_object('_elementor_edit_mode','builder','_elementor_data',$2::text) WHERE id=$1::bigint`, pageID, tree); err != nil {
		t.Fatal(err)
	}
	_, out, raw = call(http.MethodGet, "global-classes/post?post_id="+pageID, nil)
	if d := obj(out["data"]); len(d) != 2 || d["e1-local"] != nil || obj(d["g-aaa"])["id"] != "g-aaa" || !strings.Contains(raw, `"order":["g-aaa"]`) {
		t.Errorf("classes for post: %s", raw)
	}
	_, out, raw = call(http.MethodGet, "global-classes/usage", nil)
	if u, _ := obj(out["data"])["g-aaa"].([]any); len(u) != 1 || obj(u[0])["total"] != float64(2) || obj(u[0])["type"] != "wp-page" {
		t.Errorf("usage: %s", raw)
	}

	// --- variables ---
	code, out, raw = call(http.MethodPost, "variables/create", map[string]any{"type": "global-color-variable", "label": "brand", "value": "#ff5500"})
	v := obj(obj(out["data"])["variable"])
	vid, _ := v["id"].(string)
	if code != http.StatusCreated || !strings.HasPrefix(vid, "e-gv-") || len(vid) != 12 || obj(out["data"])["watermark"] != float64(1) {
		t.Fatalf("create variable: %d %s", code, raw)
	}
	code, out, raw = call(http.MethodPost, "variables/create", map[string]any{"type": "global-size-variable", "label": "gap", "value": "12px"})
	sid, _ := obj(obj(out["data"])["variable"])["id"].(string)
	if code != http.StatusCreated || obj(out["data"])["watermark"] != float64(2) {
		t.Fatalf("create size: %d %s", code, raw)
	}
	code, _, raw = call(http.MethodPost, "variables/create", map[string]any{"type": "global-color-variable", "label": "BRAND", "value": "#000"})
	if code != http.StatusBadRequest || raw != `{"code":"duplicated_label","message":"Variable label already exists","data":{"status":400}}` {
		t.Errorf("duplicate variable: %d %s", code, raw)
	}
	if code, _, raw = call(http.MethodPost, "variables/create", map[string]any{"type": "global-color-variable", "label": "has space", "value": "#000"}); code != http.StatusBadRequest {
		t.Errorf("label with space: %d %s", code, raw)
	}
	var stored string
	_ = getPool(t).QueryRow(context.Background(), `SELECT data->$1->>'value' FROM gwe_variables`, sid).Scan(&stored)
	if !strings.Contains(stored, `"$$type": "size"`) || !strings.Contains(stored, `"unit": "px"`) {
		t.Errorf("stored size value: %s", stored)
	}
	code, out, raw = call(http.MethodPut, "variables/update", map[string]any{"id": vid, "label": "brand-main", "value": "#111111"})
	if code != http.StatusOK || obj(obj(out["data"])["variable"])["label"] != "brand-main" || obj(out["data"])["watermark"] != float64(3) {
		t.Errorf("update variable: %d %s", code, raw)
	}
	code, out, raw = call(http.MethodPut, "variables/delete", map[string]any{"id": vid})
	if code != http.StatusOK || obj(obj(out["data"])["variable"])["deleted"] != true || obj(out["data"])["watermark"] != float64(4) {
		t.Errorf("delete variable: %d %s", code, raw)
	}
	_, out, raw = call(http.MethodGet, "variables/list", nil)
	vars := obj(obj(out["data"])["variables"])
	if obj(vars[vid])["deleted"] != true || obj(vars[sid])["value"] != "12px" || obj(out["data"])["total"] != float64(2) || obj(out["data"])["watermark"] != float64(4) {
		t.Errorf("variables list: %s", raw)
	}
	code, out, raw = call(http.MethodPut, "variables/restore", map[string]any{"id": vid})
	if code != http.StatusOK || obj(obj(out["data"])["variable"])["deleted_at"] != nil || obj(out["data"])["watermark"] != float64(5) {
		t.Errorf("restore variable: %d %s", code, raw)
	}
	if code, out, raw = call(http.MethodPut, "variables/update", map[string]any{"id": "e-gv-nope000", "label": "x", "value": "y"}); code != http.StatusNotFound || out["code"] != "variable_not_found" {
		t.Errorf("update missing: %d %s", code, raw)
	}
	code, out, raw = call(http.MethodPost, "variables/batch", map[string]any{"watermark": 5, "operations": []any{
		map[string]any{"type": "create", "variable": map[string]any{"id": "tmp-1", "type": "global-font-variable", "label": "body-font", "value": "Roboto"}},
		map[string]any{"type": "update", "id": sid, "variable": map[string]any{"label": "gap-lg", "value": "24px"}},
	}})
	if res, _ := obj(out["data"])["results"].([]any); code != http.StatusOK || len(res) != 2 || obj(res[0])["temp_id"] != "tmp-1" || obj(out["data"])["watermark"] != float64(6) {
		t.Errorf("batch: %d %s", code, raw)
	}
	code, out, raw = call(http.MethodPost, "variables/batch", map[string]any{"watermark": 6, "operations": []any{
		map[string]any{"type": "create", "variable": map[string]any{"id": "tmp-2", "type": "global-font-variable", "label": "body-font", "value": "Arial"}},
	}})
	if code != http.StatusBadRequest || out["code"] != "batch_duplicated_label" || obj(out["data"])["tmp-2"] == nil {
		t.Errorf("batch duplicate: %d %s", code, raw)
	}

	// --- components ---
	atomic := []any{map[string]any{"id": "c1", "elType": "e-div-block", "settings": map[string]any{}, "styles": map[string]any{"s-1": map[string]any{"id": "s-1", "type": "class", "label": "local",
		"variants": []any{map[string]any{"meta": map[string]any{"breakpoint": "desktop", "state": nil}, "props": map[string]any{}, "custom_css": nil}}},
		"s-bad": map[string]any{"id": "s-bad"}}, // invalid styles are dropped like upstream
		"elements": []any{map[string]any{"id": "c2", "elType": "widget", "widgetType": "e-heading", "settings": map[string]any{}, "elements": []any{}}}}}
	code, out, raw = call(http.MethodPost, "components", map[string]any{"status": "draft", "items": []any{
		map[string]any{"uid": "uid-1", "title": "Hero Card", "elements": atomic}}})
	compID, _ := obj(out["data"])["uid-1"].(float64)
	if code != http.StatusCreated || compID == 0 {
		t.Fatalf("create component: %d %s", code, raw)
	}
	cid := jsonNum(compID)
	code, out, raw = call(http.MethodPost, "components/create-validate", map[string]any{"items": []any{
		map[string]any{"uid": "uid-2", "title": "Legacy", "elements": []any{map[string]any{"id": "x", "elType": "widget", "widgetType": "heading", "elements": []any{}}}}}})
	if code != http.StatusUnprocessableEntity || out["code"] != "non_atomic_element_in_component" {
		t.Errorf("non-atomic: %d %s", code, raw)
	}
	if code, out, raw = call(http.MethodPost, "components", map[string]any{"status": "draft", "items": []any{
		map[string]any{"uid": "uid-1", "title": "Other", "elements": atomic}}}); code != http.StatusUnprocessableEntity || out["code"] != "components_validation_failed" {
		t.Errorf("duplicate uid: %d %s", code, raw)
	}
	_, _, raw = call(http.MethodGet, "components", nil)
	if raw != `{"data":[{"id":`+cid+`,"isArchived":false,"name":"Hero Card","uid":"uid-1"}],"meta":[]}` {
		t.Errorf("components list: %s", raw)
	}
	_, out, raw = call(http.MethodGet, "components/styles", nil)
	if obj(obj(out["data"])[cid])["s-1"] == nil || obj(obj(out["data"])[cid])["s-bad"] != nil {
		t.Errorf("component styles: %s", raw)
	}
	_, out, raw = call(http.MethodGet, "components/overridable-props?componentIds[]="+cid+"&componentIds[]=999999", nil)
	if d := obj(out["data"]); len(d) != 1 || d[cid] != nil || obj(obj(out["meta"])["errors"])["999999"] != "component_not_found" {
		t.Errorf("overridable props: %s", raw)
	}
	code, out, raw = call(http.MethodPut, "components/status", map[string]any{"status": "publish", "ids": []any{compID}})
	if s, _ := obj(out["data"])["success"].([]any); code != http.StatusOK || len(s) != 1 {
		t.Errorf("publish component: %d %s", code, raw)
	}
	var status string
	_ = getPool(t).QueryRow(context.Background(), `SELECT status FROM posts WHERE id=$1::bigint`, cid).Scan(&status)
	if status != "publish" {
		t.Errorf("component status %q", status)
	}
	if code, out, raw = call(http.MethodPost, "components/lock", map[string]any{"componentId": compID}); code != http.StatusOK || obj(out["data"])["locked"] != true {
		t.Errorf("lock: %d %s", code, raw)
	}
	if _, out, raw = call(http.MethodGet, "components/lock-status?componentId="+cid, nil); obj(out["data"])["is_current_user_allow_to_edit"] != true || obj(out["data"])["locked_by"] == nil {
		t.Errorf("lock status: %s", raw)
	}
	if code, out, raw = call(http.MethodPost, "components/unlock", map[string]any{"componentId": compID}); code != http.StatusOK || obj(out["data"])["unlocked"] != true {
		t.Errorf("unlock: %d %s", code, raw)
	}
	if code, out, raw = call(http.MethodPost, "components/update-titles", map[string]any{"status": "publish", "components": []any{map[string]any{"componentId": compID, "title": "Hero"}}}); code != http.StatusOK || len(obj(out["data"])["successIds"].([]any)) != 1 {
		t.Errorf("update titles: %d %s", code, raw)
	}
	code, out, raw = call(http.MethodPost, "components/archive", map[string]any{"componentIds": []any{compID}, "status": "publish"})
	if s, _ := obj(out["data"])["successIds"].([]any); code != http.StatusOK || len(s) != 1 {
		t.Errorf("archive: %d %s", code, raw)
	}
	_, _, raw = call(http.MethodGet, "components", nil)
	if !strings.Contains(raw, `"isArchived":true`) || !strings.Contains(raw, `"name":"Hero"`) {
		t.Errorf("components after archive: %s", raw)
	}

	// --- default styles ---
	code, out, raw = call(http.MethodPut, "default-styles/h1", map[string]any{"variants": variant("#222")})
	if code != http.StatusOK || obj(out["data"])["id"] != "h1" || obj(out["data"])["type"] != "class" {
		t.Errorf("put default style: %d %s", code, raw)
	}
	if _, out, raw = call(http.MethodGet, "default-styles", nil); obj(obj(out["data"])["h1"])["label"] != "h1" {
		t.Errorf("default styles: %s", raw)
	}
	if code, _, _ = call(http.MethodGet, "default-styles/h1", nil); code != http.StatusOK {
		t.Errorf("get default style: %d", code)
	}
	if code, out, _ = call(http.MethodPut, "default-styles/blink", map[string]any{"variants": []any{}}); code != http.StatusBadRequest || out["code"] != "invalid_tag" {
		t.Errorf("invalid tag: %d", code)
	}
	if _, out, raw = call(http.MethodDelete, "default-styles/h1", nil); obj(out["data"])["deleted"] != true {
		t.Errorf("delete default style: %s", raw)
	}
	if code, _, _ = call(http.MethodGet, "default-styles/h1", nil); code != http.StatusNotFound {
		t.Errorf("deleted default style: %d", code)
	}

	// --- kit element defaults ---
	if code, _, raw = call(http.MethodPut, "kit-elements-defaults/heading", map[string]any{"settings": map[string]any{"title_color": "#f00"}}); code != http.StatusOK || raw != "{}" {
		t.Errorf("put defaults: %d %s", code, raw)
	}
	if _, out, raw = call(http.MethodGet, "kit-elements-defaults", nil); obj(out["heading"])["title_color"] != "#f00" {
		t.Errorf("defaults: %s", raw)
	}
	call(http.MethodDelete, "kit-elements-defaults/heading", nil)
	if _, _, raw = call(http.MethodGet, "kit-elements-defaults", nil); raw != "{}" {
		t.Errorf("defaults after delete: %s", raw)
	}

	// Design-system stylesheet and permissions.
	if code, _, _ = call(http.MethodPost, "design-system-sync/stylesheet", nil); code != http.StatusNoContent {
		t.Errorf("stylesheet: %d", code)
	}
	req, _ := http.NewRequest(http.MethodGet, base+"/wp-json/elementor/v1/global-classes", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("anonymous global-classes: %d", resp.StatusCode)
	}
}
