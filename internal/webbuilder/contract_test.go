package webbuilder

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// A doc in the upstream shape decodes into the Go mirror and encodes back without losing the
// fields the mirror knows, including explicit zeros and false.
func TestDocRoundTrip(t *testing.T) {
	in := `{"sections":[{"id":"s1","kind":"hero","name":"Hero","hidden":false,
		"content":{"title":"Hi","variant":"split"},
		"items":[{"id":"i1","title":"A","text":"B","href":"#x","lay":{"gap":0,"wrap":false},
			"sub":{"id":"s2","kind":"layout","name":"L","hidden":true,"content":{},"items":[],
				"style":{"palette":"dark","align":"left","padding":0,"titleSize":32,"hideOn":["mobile"]}}}],
		"style":{"palette":"brand","align":"center","padding":96,"titleSize":56,"hideOn":[],
			"motion":{"entrance":"fade-up","delay":0,"repeat":false}},
		"els":{"title":{"mt":0,"color":"#fff","iconColors":{"#000000":"#ff0000"},"removed":true}},
		"grid":{"cols":3,"border":false},
		"bg":{"type":"gradient","decorX":0}}],
		"site":{"brand":"#2271b1","fontPair":"modern","corners":"soft","headingWeight":700}}`
	var d DocState
	if err := json.Unmarshal([]byte(in), &d); err != nil {
		t.Fatal(err)
	}
	out, err := json.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	var a, b any
	_ = json.Unmarshal([]byte(in), &a)
	_ = json.Unmarshal(out, &b)
	ja, _ := json.Marshal(a)
	jb, _ := json.Marshal(b)
	if string(ja) != string(jb) {
		t.Fatalf("round trip changed the doc\n in: %s\nout: %s", ja, jb)
	}
}

// The AI-off boot block has exactly the documented shape.
func TestBootAIOff(t *testing.T) {
	j, _ := json.Marshal(BootAI{Providers: []AIProviderInfo{}})
	if got, want := string(j), `{"enabled":false,"providers":[],"llmBase":"","imagesUrl":"","allowCode":false}`; got != want {
		t.Fatalf("got %s want %s", got, want)
	}
}

// Library ids are strings on the wire, like upstream SavedComponent.id.
func TestLibraryItemFlat(t *testing.T) {
	j, _ := json.Marshal(LibraryItem{SavedComponent: SavedComponent{ID: "42", Name: "x"}})
	if !strings.HasPrefix(string(j), `{"id":"42","name":"x","section":`) {
		t.Fatalf("got %s", j)
	}
}

// Constants shared with builder/host/contract.ts must agree.
func TestTSContractInSync(t *testing.T) {
	src, err := os.ReadFile(filepath.Join("..", "..", "builder", "host", "contract.ts"))
	if err != nil {
		t.Skip("builder/host/contract.ts not found:", err)
	}
	ts := string(src)
	want := map[string]string{
		`API_VERSION = (\d+)`:                 strconv.Itoa(APIVersion),
		`RENDERER_VERSION = (\d+)`:            strconv.Itoa(RendererVersion),
		`API_HEADER = '([^']+)'`:              APIHeader,
		`UPSTREAM_TYPES_SHA = '([^']+)'`:      UpstreamTypesSHA,
		`REST_NAMESPACE = '([^']+)'`:          RESTNamespace,
		`EDITOR_ASSETS_PATH = '([^']+)'`:      EditorAssetsPath,
		`EDITOR_NOAI_ASSETS_PATH = '([^']+)'`: EditorNoAIAssetsPath,
		`SITE_ASSETS_PATH = '([^']+)'`:        SiteAssetsPath,
		`FORM_HONEYPOT = '([^']+)'`:           FormHoneypot,
		`FORM_TIMING = '([^']+)'`:             FormTiming,
	}
	for re, v := range want {
		m := regexp.MustCompile(re).FindStringSubmatch(ts)
		if m == nil || m[1] != v {
			t.Errorf("contract.ts %s: got %v, Go has %q", re, m, v)
		}
	}
	for _, f := range []Feature{FeatureMenu, FeatureAcc, FeatureTabs, FeatureDismiss, FeatureForm, FeatureMotion, FeatureEmbed} {
		if !strings.Contains(ts, "'"+string(f)+"'") {
			t.Errorf("contract.ts lacks feature %q", f)
		}
	}
	for _, code := range []string{ErrAPIMismatch, ErrRevConflict, ErrDetached, ErrLocked, ErrTooLarge, ErrInvalidDoc, ErrFormInvalid, ErrRateLimited, ErrAIBudget, ErrAIPath, ErrAIModel, ErrAINoKey, ErrAIUpstream, ErrAITimeout} {
		if !strings.Contains(ts, "'"+code+"'") {
			t.Errorf("contract.ts lacks error code %q", code)
		}
	}
	for p, paths := range AIProviderPaths {
		for _, path := range paths {
			if !strings.Contains(ts, string(p)+": ['"+path+"']") {
				t.Errorf("contract.ts AI_PROVIDER_PATHS lacks %s: %s", p, path)
			}
		}
	}
}
