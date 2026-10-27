package builder

// Fixture context for the pro-d-* goldens (posts, portfolio, loop grid/carousel, taxonomy filter,
// sitemap): the lab's posts and terms at capture time (testdata/golden/pro-d-posts.json) and the
// loop item template 213 (testdata/golden/pro-d-loop-213.json).

import (
	"encoding/json"
	"os"
	"sort"
	"strings"
	"testing"
)

type proDLabTerm struct {
	ID       int64
	Name     string
	URL      string
	Slug     string
	Taxonomy string
	Count    int
	ParentID int64
}

type proDLabPost struct {
	PostData
	Excerpt    string
	TheExcerpt string
	RawContent string
	Content    string
	Categories []proDLabTerm
	Tags       []proDLabTerm
}

var proDActive bool

func proDLoadLab() ([]*proDLabPost, []proDLabTerm) {
	raw, err := os.ReadFile("testdata/golden/pro-d-posts.json")
	if err != nil {
		return nil, nil
	}
	var d struct {
		Posts []*proDLabPost
		Terms []proDLabTerm
	}
	_ = json.Unmarshal(raw, &d)
	return d.Posts, d.Terms
}

func init() {
	prevRC := proTestRC
	proTestRC = func(rc *RenderContext) {
		if prevRC != nil {
			prevRC(rc)
		}
		if inner := proFixtureCurrent; inner != nil {
			proFixtureCurrent = func(doc *Document) {
				inner(doc)
				proDActive = strings.HasPrefix(proFixtureName(doc), "pro-d-")
			}
		}
		posts, terms := proDLoadLab()
		if len(posts) == 0 || rc.Pro == nil {
			return
		}
		byID := map[int64]*proDLabPost{}
		data := map[int64]*PostData{}
		for _, p := range posts {
			byID[p.ID] = p
			pd := p.PostData
			pd.Excerpt = p.TheExcerpt
			pd.Categories, pd.Tags = nil, nil
			for _, t := range p.Categories {
				pd.Categories = append(pd.Categories, TermLink{Name: t.Name, URL: t.URL})
			}
			for _, t := range p.Tags {
				pd.Tags = append(pd.Tags, TermLink{Name: t.Name, URL: t.URL})
			}
			data[p.ID] = &pd
		}
		pro := rc.Pro
		baseQuery, baseMeta, baseTermIDs, baseTerms, baseTpl := pro.Query, pro.PostMeta, pro.PostTermIDs, pro.Terms, pro.Template
		pro.Query = func(q *PostQuery) *PostList {
			if !proDActive {
				if baseQuery != nil {
					return baseQuery(q)
				}
				return nil
			}
			list := append([]*proDLabPost(nil), posts...)
			switch q.OrderBy {
			case "post_title", "title":
				sort.SliceStable(list, func(i, j int) bool { return list[i].Title < list[j].Title })
				if !strings.EqualFold(q.Order, "asc") {
					for i, j := 0, len(list)-1; i < j; i, j = i+1, j-1 {
						list[i], list[j] = list[j], list[i]
					}
				}
			default:
				if strings.EqualFold(q.Order, "asc") {
					for i, j := 0, len(list)-1; i < j; i, j = i+1, j-1 {
						list[i], list[j] = list[j], list[i]
					}
				}
			}
			var filtered []*proDLabPost
			for _, p := range list {
				if containsID(q.ExcludeIDs, p.ID) || (len(q.IncludeIDs) > 0 && !containsID(q.IncludeIDs, p.ID)) {
					continue
				}
				filtered = append(filtered, p)
			}
			per, page := q.PerPage, q.Page
			if per <= 0 {
				per = 6
			}
			if page < 1 {
				page = 1
			}
			total := len(filtered) - q.Offset
			if total < 0 {
				total = 0
			}
			out := &PostList{Total: total, Pages: (total + per - 1) / per, CurrentPage: page}
			for i := q.Offset + (page-1)*per; i < len(filtered) && i < q.Offset+page*per; i++ {
				out.Posts = append(out.Posts, data[filtered[i].ID])
			}
			return out
		}
		pro.PostMeta = func(id int64, key string) any {
			p := byID[id]
			if !proDActive || p == nil {
				if baseMeta != nil {
					return baseMeta(id, key)
				}
				return nil
			}
			switch key {
			case "_gwe_raw_excerpt":
				return p.Excerpt
			case "_modified_iso":
				return p.DateISO
			case "_gwe_the_content":
				return p.Content
			case "_parent_id":
				return 0
			}
			return nil
		}
		pro.PostTermIDs = func(id int64, tax string) []int64 {
			p := byID[id]
			if !proDActive || p == nil {
				if baseTermIDs != nil {
					return baseTermIDs(id, tax)
				}
				return nil
			}
			src := p.Categories
			if tax == "post_tag" {
				src = p.Tags
			}
			var out []int64
			for _, t := range src {
				out = append(out, t.ID)
			}
			return out
		}
		pro.Terms = func(tax string, args map[string]any) []*TermItem {
			if !proDActive {
				if baseTerms != nil {
					return baseTerms(tax, args)
				}
				return nil
			}
			var out []*TermItem
			for _, t := range terms {
				if t.Taxonomy == tax {
					out = append(out, &TermItem{ID: t.ID, Taxonomy: t.Taxonomy, Name: t.Name, Slug: t.Slug, URL: t.URL, Count: t.Count, ParentID: t.ParentID})
				}
			}
			return out
		}
		pro.Template = func(id int64) *Document {
			if !proDActive || id != 213 {
				if baseTpl != nil {
					return baseTpl(id)
				}
				return nil
			}
			raw, err := os.ReadFile("testdata/golden/pro-d-loop-213.json")
			if err != nil {
				return nil
			}
			els, err := ParseElements(raw)
			if err != nil {
				return nil
			}
			return &Document{ID: 213, Type: "loop-item", Elements: els, Settings: map[string]any{}}
		}
		pro.PostTypeLabel = func(t string) string {
			if t == "page" {
				return "Pages"
			}
			return "Posts"
		}
	}
}

func TestProDWidgetsRegistered(t *testing.T) {
	for _, n := range []string{"posts", "portfolio", "loop-grid", "loop-carousel", "taxonomy-filter", "sitemap"} {
		if widgetDefs[n] == nil {
			t.Errorf("%s not registered", n)
		}
	}
}
