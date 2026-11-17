package webbuilder

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestBindSiteCorpus runs BindSite over a design folder named by WB_BIND_CORPUS (a local corpus,
// e.g. enerjiosgb/html) and prints what it bound. Skipped without it.
func TestBindSiteCorpus(t *testing.T) {
	dir := os.Getenv("WB_BIND_CORPUS")
	if dir == "" {
		t.Skip("WB_BIND_CORPUS not set")
	}
	var pages []SitePage
	var files []string
	kit := map[string]bool{}
	_ = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		rel, _ := filepath.Rel(dir, p)
		rel = filepath.ToSlash(rel)
		if IsSitePage(rel) {
			b, _ := os.ReadFile(p)
			pages = append(pages, SitePage{Path: rel, HTML: string(b)})
			files = append(files, rel)
		} else {
			kit[rel] = true
		}
		return nil
	})
	bindDebug = func(what, got, want string) {
		i := 0
		for i < len(got) && i < len(want) && got[i] == want[i] {
			i++
		}
		lo := max(0, i-200)
		t.Logf("%s differs at %d:\n GOT  %s\n WANT %s", what, i, got[lo:min(len(got), i+200)], want[lo:min(len(want), i+200)])
	}
	defer func() { bindDebug = nil }()
	plan := PlanSite(files)
	res, err := ConvertSite(pages, SiteImportOptions{KitURL: "/kit/", KitFiles: kit, URLs: plan.URLs()})
	if err != nil {
		t.Fatal(err)
	}
	parents := map[string]string{}
	for f := range SiteFolders(res) {
		parents[f] = "7"
	}
	b := BindSite(res, SiteBindOptions{Menus: true, Groups: map[string]bool{"*": true}, FolderParents: parents})
	if b.Menu != nil {
		t.Logf("menu: lists %v subs %v", b.Menu.Lists, b.Menu.Subs)
		for _, it := range b.Menu.Items {
			t.Logf("  %s %s %s (%d children)", it.Label, it.URL, it.Target, len(it.Children))
		}
	} else {
		t.Logf("no menu")
	}
	for _, g := range b.Groups {
		t.Logf("group %s: %d pages, fields %v, rule %v", g.Folder, len(g.Posts), g.Fields, g.Rule != nil)
		for _, s := range g.Sections {
			t.Logf("  binds %s", s.Binds)
		}
	}
	for _, l := range b.Listings {
		t.Logf("listing %s §%d of %s: %v", res.Pages[l.Page].Path, l.Section, l.Folder, l.Members)
		t.Logf("  binds %s", res.Pages[l.Page].Code.Sections[l.Section].Binds)
	}
	t.Logf("notes:\n%s", strings.Join(b.Notes, "\n"))
}
