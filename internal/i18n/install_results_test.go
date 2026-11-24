package i18n

import (
	"archive/zip"
	"bytes"
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

type installTestTransport func(*http.Request) (*http.Response, error)

func (f installTestTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestInstallReportsArchiveProblems(t *testing.T) {
	archive := func(name string, data []byte) []byte {
		t.Helper()
		var b bytes.Buffer
		z := zip.NewWriter(&b)
		f, err := z.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.Write(data); err != nil {
			t.Fatal(err)
		}
		if err := z.Close(); err != nil {
			t.Fatal(err)
		}
		return b.Bytes()
	}
	core := archive("admin-de_DE.mo", writeMO(map[string]string{"Hello": "Hallo"}))
	noCatalogs := archive("README.txt", []byte("no catalogs"))
	for _, tc := range []struct {
		name       string
		core       []byte
		plugins    []string
		errorText  string
		translated string
	}{
		{"core without catalogs", noCatalogs, nil, "no catalog files", "Hello"},
		{"broken plugin archive", core, []string{"broken"}, "broken translation for de_DE", "Hallo"},
		{"no plugin archive offered", core, []string{"missing"}, "", "Hallo"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := NewManager(t.TempDir(), nil)
			_ = m.Catalog("de_DE")
			in := &Installer{API: "https://translations.example.test", Client: &http.Client{Transport: installTestTransport(func(r *http.Request) (*http.Response, error) {
				var body []byte
				switch r.URL.Path {
				case "/core/1.0/":
					body = []byte(`{"translations":[{"language":"de_DE","package":"https://translations.example.test/core.zip"}]}`)
				case "/plugins/1.0/":
					body = []byte(`{"translations":[]}`)
					if r.URL.Query().Get("slug") == "broken" {
						body = []byte(`{"translations":[{"language":"de_DE","package":"https://translations.example.test/plugin.zip"}]}`)
					}
				case "/core.zip":
					body = tc.core
				case "/plugin.zip":
					body = []byte("invalid zip")
				default:
					t.Fatalf("unexpected request: %s", r.URL)
				}
				return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(bytes.NewReader(body))}, nil
			})}}
			err := m.Install(context.Background(), in, "de_DE", tc.plugins)
			if tc.errorText == "" {
				if err != nil {
					t.Fatal(err)
				}
			} else if err == nil || !strings.Contains(err.Error(), tc.errorText) {
				t.Fatalf("error = %v, want %q", err, tc.errorText)
			}
			if got := m.Catalog("de_DE").T("Hello"); got != tc.translated {
				t.Errorf("translation = %q, want %q", got, tc.translated)
			}
		})
	}
}
