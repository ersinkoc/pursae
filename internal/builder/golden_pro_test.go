package builder

// Golementor Pro golden fixtures (testdata/golden/pro-*.data.json, made in the lab with upstream
// Pro active) render against the lab's sample content: testdata/golden/pro-posts.json is the
// lab's post list (scratchpad elementor-lab/wp/wpgo-dump/make-content.php), the fixture page is
// the "current post" and the site is the lab site.

import (
	"encoding/json"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"
)

func init() {
	proTestRC = proFixtureRC
}

type proFixturePost struct {
	PostData
	Content string
}

var proFixturePosts []*proFixturePost

func loadProFixturePosts() []*proFixturePost {
	if proFixturePosts != nil {
		return proFixturePosts
	}
	raw, err := os.ReadFile("testdata/golden/pro-posts.json")
	if err != nil {
		proFixturePosts = []*proFixturePost{}
		return proFixturePosts
	}
	_ = json.Unmarshal(raw, &proFixturePosts)
	return proFixturePosts
}

// proFixtureRC adds the lab site data to a golden render context.
func proFixtureRC(rc *RenderContext) {
	posts := loadProFixturePosts()
	byID := map[int64]*proFixturePost{}
	for _, p := range posts {
		byID[p.ID] = p
	}
	home := "http://localhost:8095/"
	now := time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)
	var current *PostData
	theme := &ThemeData{SiteName: "Lab", Tagline: "", HomeURL: home, SearchURL: home, Now: now,
		Post: func() *PostData { return current }, PostContent: func() string { return "" },
		Archive: func() *ArchiveData { return nil },
		Menu:    func(string, int64) []*MenuItem { return nil }}
	rc.Theme = theme
	author := &UserInfo{ID: 1, Login: "labadmin", Email: "labadmin@example.test", Nicename: "labadmin", DisplayName: "labadmin",
		PostsURL: home + "author/labadmin/", AvatarURL: "http://localhost:8095/avatar.png", Roles: []string{"administrator"}, Meta: map[string]any{}}
	rc.Pro = &ProData{
		DateFormat: "F j, Y", TimeFormat: "g:i a", Now: now, AdminAjaxURL: home + "wp-admin/admin-ajax.php", RestURL: home + "wp-json/",
		AuthorOf: func(int64) *UserInfo { return author },
		UserByID: func(id int64) *UserInfo { return author },
		PostByID: func(id int64) *PostData {
			if p := byID[id]; p != nil {
				return &p.PostData
			}
			return nil
		},
		PostTerms: func(id int64, tax string) []TermLink {
			if p := byID[id]; p != nil {
				if tax == "category" {
					return p.Categories
				}
				return p.Tags
			}
			return nil
		},
		Query: func(q *PostQuery) *PostList {
			list := append([]*proFixturePost(nil), posts...)
			if q.OrderBy == "post_title" || q.OrderBy == "title" {
				sort.SliceStable(list, func(i, j int) bool { return list[i].Title < list[j].Title })
			}
			if strings.EqualFold(q.Order, "asc") {
				for i, j := 0, len(list)-1; i < j; i, j = i+1, j-1 {
					list[i], list[j] = list[j], list[i]
				}
			}
			var filtered []*proFixturePost
			for _, p := range list {
				if q.ExcludeCurrent && current != nil && p.ID == current.ID {
					continue
				}
				if len(q.ExcludeIDs) > 0 && containsID(q.ExcludeIDs, p.ID) {
					continue
				}
				if len(q.IncludeIDs) > 0 && !containsID(q.IncludeIDs, p.ID) {
					continue
				}
				filtered = append(filtered, p)
			}
			per := q.PerPage
			if per <= 0 {
				per = 6
			}
			page := q.Page
			if page < 1 {
				page = 1
			}
			total := len(filtered) - q.Offset
			if total < 0 {
				total = 0
			}
			out := &PostList{Total: total, Pages: (total + per - 1) / per, CurrentPage: page}
			start := q.Offset + (page-1)*per
			for i := start; i < len(filtered) && i < start+per; i++ {
				out.Posts = append(out.Posts, &filtered[i].PostData)
			}
			return out
		},
		Terms: func(tax string, args map[string]any) []*TermItem {
			if tax == "category" {
				return []*TermItem{{ID: 1, Taxonomy: "category", Name: "Uncategorized", Slug: "uncategorized", URL: home + "category/uncategorized/", Count: 1},
					{ID: 2, Taxonomy: "category", Name: "News", Slug: "news", URL: home + "category/news/", Count: 2},
					{ID: 3, Taxonomy: "category", Name: "Guides", Slug: "guides", URL: home + "category/guides/", Count: 3}}
			}
			return nil
		},
		Breadcrumbs: func() []TermLink {
			if current == nil {
				return []TermLink{{Name: "Home", URL: home}}
			}
			return []TermLink{{Name: "Home", URL: home}, {Name: current.Title}}
		},
		PostTypeLabel: func(t string) string { return strings.ToUpper(t[:1]) + t[1:] + "s" },
		Request:       func(string, string) string { return "" },
		CommentsHTML:  func(int64) string { return "" },
	}
	// The fixture page is the current post (set per fixture by the golden test through the document).
	rc.Pro.PostMeta = func(id int64, key string) any { return nil }
	proFixtureCurrent = func(doc *Document) {
		current = &PostData{ID: doc.ID, Type: "page", Title: "WPGO fixture " + proFixtureName(doc), URL: home + "wpgo-fx-" + proFixtureName(doc) + "/",
			Date: "October 27, 2026", DateISO: "2026-10-27T12:00:00+00:00", AuthorName: "labadmin", AuthorURL: home + "author/labadmin/",
			Class: "post-" + strconv.FormatInt(doc.ID, 10) + " page type-page status-publish hentry"}
		rc.Pro.CurrentURL = home + "wpgo-fx-" + proFixtureName(doc) + "/"
	}
}

var proFixtureCurrent func(doc *Document)

func proFixtureName(doc *Document) string {
	if n, ok := doc.Settings["__fixture_name"].(string); ok {
		return n
	}
	return ""
}

func containsID(list []int64, id int64) bool {
	for _, x := range list {
		if x == id {
			return true
		}
	}
	return false
}
