package server

import (
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"regexp"
	"strconv"
	"strings"
	"time"

	"wpgo/internal/blocks"
	"wpgo/internal/store"
)

// The schema graph: one JSON-LD script with an @graph of connected nodes, the way Yoast builds it.
// @ids: <home>#website, <home>#organization, <home>#/schema/person/<hash>, <home>#/schema/logo/image/,
// <url> for the WebPage, <url>#article, <url>#breadcrumb, <url>#primaryimage, <url>#faq-<id>,
// <url>#howto-<n>. FAQ and HowTo blocks, Product, LocalBusiness and custom JSON-LD join the graph.

type ldNode = map[string]any

func (s *Server) seogoOrgName() string {
	if n := strings.TrimSpace(s.seogoOpts().KGName); n != "" {
		return n
	}
	return s.settings.String("blogname")
}

func personID(home string, u *store.User) string {
	sum := md5.Sum([]byte(u.Login + "user"))
	return home + "#/schema/person/" + hex.EncodeToString(sum[:])
}

func (s *Server) seogoLanguage() string {
	return strings.ReplaceAll(s.seogoLocale(), "_", "-")
}

// seogoGraph builds the @graph of a context.
func (s *Server) seogoGraph(sc *seogoCtx, title, desc string) []ldNode {
	o := s.seogoOpts()
	home := sc.lb.HomeURL("/")
	lang := s.seogoLanguage()
	websiteID := home + "#website"
	var graph []ldNode

	// Publisher: Organization (LocalBusiness with Local SEO) or Person.
	var publisherID string
	if o.KGType == "person" && o.KGPersonID > 0 {
		if u, err := s.store.UserByID(sc.ctx, o.KGPersonID); err == nil {
			publisherID = personID(home, u)
			graph = append(graph, s.seogoPersonNode(sc, u, home, true))
		}
	}
	if publisherID == "" {
		publisherID = home + "#organization"
		org := ldNode{"@type": "Organization", "@id": publisherID, "name": s.seogoOrgName(), "url": home}
		if o.KGAltName != "" {
			org["alternateName"] = o.KGAltName
		}
		if o.KGLogo != "" {
			org["logo"] = ldNode{"@type": "ImageObject", "inLanguage": lang, "@id": home + "#/schema/logo/image/", "url": o.KGLogo, "contentUrl": o.KGLogo, "caption": s.seogoOrgName()}
			org["image"] = ldNode{"@id": home + "#/schema/logo/image/"}
		}
		var same []string
		if o.FacebookURL != "" {
			same = append(same, o.FacebookURL)
		}
		if u := strings.TrimPrefix(o.TwitterUser, "@"); u != "" {
			same = append(same, "https://x.com/"+u)
		}
		for _, p := range o.Profiles {
			if p = strings.TrimSpace(p); p != "" {
				same = append(same, p)
			}
		}
		if len(same) > 0 {
			org["sameAs"] = same
		}
		if o.Modules["local"] {
			s.seogoLocalBusiness(org)
		}
		graph = append(graph, org)
	}

	website := ldNode{"@type": "WebSite", "@id": websiteID, "url": home, "name": s.settings.String("blogname"),
		"publisher": ldNode{"@id": publisherID}, "inLanguage": lang,
		"potentialAction": []ldNode{{"@type": "SearchAction", "target": ldNode{"@type": "EntryPoint", "urlTemplate": home + "?s={search_term_string}"},
			"query-input": ldNode{"@type": "PropertyValueSpecification", "valueRequired": true, "valueName": "search_term_string"}}}}
	if d := s.settings.String("blogdescription"); d != "" {
		website["description"] = d
	}
	if o.KGAltName != "" {
		website["alternateName"] = o.KGAltName
	}
	graph = append(graph, website)

	url := sc.canonical()
	if url == "" {
		url = sc.url()
	}
	if url == "" { // search results
		url = sc.lb.SearchLink(sc.Search)
	}
	pageType := "WebPage"
	switch sc.Kind {
	case "category", "tag", "date":
		pageType = "CollectionPage"
	case "home":
		if sc.Post == nil {
			pageType = "CollectionPage"
		}
	case "author":
		pageType = "ProfilePage"
	case "search":
		pageType = "SearchResultsPage"
	}
	if sc.singular() && sc.Kind != "home" {
		if v := seogoMeta(sc.Post.Meta, "schema_page"); v != "" {
			pageType = v
		} else if v := o.Types[sc.Post.Type].SchemaPage; v != "" {
			pageType = v
		}
	}
	page := ldNode{"@type": pageType, "@id": url, "url": url, "name": title, "isPartOf": ldNode{"@id": websiteID}, "inLanguage": lang}
	if desc != "" {
		page["description"] = desc
	}
	if sc.Kind == "front-page" || (sc.Kind == "home" && sc.Post == nil) {
		page["about"] = ldNode{"@id": publisherID}
	}
	var primaryImg *seogoImage
	var imgNode ldNode
	if sc.singular() {
		p := sc.Post
		if p.PublishedAt != nil {
			page["datePublished"] = p.PublishedAt.UTC().Format(time.RFC3339)
		}
		page["dateModified"] = p.ModifiedAt.UTC().Format(time.RFC3339)
		primaryImg = s.seogoAttachmentImage(sc.ctx, p.FeaturedMediaID)
		if primaryImg == nil {
			if m := seogoImgSrcRe.FindStringSubmatch(p.Content); m != nil {
				primaryImg = &seogoImage{URL: m[1]}
			}
		}
		if primaryImg != nil {
			img := ldNode{"@type": "ImageObject", "inLanguage": lang, "@id": url + "#primaryimage", "url": primaryImg.URL, "contentUrl": primaryImg.URL}
			if primaryImg.Width > 0 {
				img["width"], img["height"] = primaryImg.Width, primaryImg.Height
			}
			if primaryImg.Alt != "" {
				img["caption"] = primaryImg.Alt
			}
			page["primaryImageOfPage"] = ldNode{"@id": url + "#primaryimage"}
			page["image"] = ldNode{"@id": url + "#primaryimage"}
			page["thumbnailUrl"] = primaryImg.URL
			imgNode = img
		}
		if p.Type == "post" && sc.Kind == "single" {
			if a := sc.author(); a != nil {
				page["author"] = ldNode{"@id": personID(home, a)}
			}
		}
		page["potentialAction"] = []ldNode{{"@type": "ReadAction", "target": []string{url}}}
	}
	if sc.Kind == "author" {
		if a := sc.author(); a != nil {
			page["mainEntity"] = ldNode{"@id": personID(home, a)}
		}
	}
	trail := []seogoCrumb{}
	if o.Modules["breadcrumbs"] && sc.Kind != "search" {
		trail = sc.breadcrumbs()
	}
	if len(trail) > 0 {
		page["breadcrumb"] = ldNode{"@id": url + "#breadcrumb"}
	}

	// Article
	artType := ""
	if sc.singular() && sc.Kind != "home" && sc.Kind != "front-page" {
		artType = o.Types[sc.Post.Type].SchemaArt
		if v := seogoMeta(sc.Post.Meta, "schema_article"); v != "" {
			artType = v
		}
	}
	var faqQuestions []ldNode
	var howtos []ldNode
	if sc.singular() {
		faqQuestions, howtos = seogoBlockSchema(sc.Post.Content, url)
	}
	if len(faqQuestions) > 0 {
		page["@type"] = []string{pageType, "FAQPage"}
		var ids []ldNode
		for _, q := range faqQuestions {
			ids = append(ids, ldNode{"@id": q["@id"]})
		}
		page["mainEntity"] = ids
	}
	graph = append(graph, page)
	if imgNode != nil {
		graph = append(graph, imgNode)
	}

	if artType == "Product" {
		graph = append(graph, s.seogoProductNode(sc, url, title, desc, primaryImg))
	} else if artType != "" && artType != "None" {
		p := sc.Post
		art := ldNode{"@type": artType, "@id": url + "#article", "isPartOf": ldNode{"@id": url}, "headline": cut(p.Title, 110),
			"mainEntityOfPage": ldNode{"@id": url}, "wordCount": len(strings.Fields(plainText(p.Content))),
			"publisher": ldNode{"@id": publisherID}, "inLanguage": lang}
		if p.PublishedAt != nil {
			art["datePublished"] = p.PublishedAt.UTC().Format(time.RFC3339)
		}
		art["dateModified"] = p.ModifiedAt.UTC().Format(time.RFC3339)
		if p.CommentCount > 0 {
			art["commentCount"] = p.CommentCount
		}
		if a := sc.author(); a != nil {
			art["author"] = ldNode{"name": a.DisplayName, "@id": personID(home, a)}
		}
		if primaryImg != nil {
			art["image"] = ldNode{"@id": url + "#primaryimage"}
			art["thumbnailUrl"] = primaryImg.URL
		}
		if p.Type == "post" {
			var sections []string
			for _, t := range sc.categories() {
				sections = append(sections, t.Name)
			}
			if len(sections) > 0 {
				art["articleSection"] = sections
			}
			if tags, _ := s.store.TermsForPosts(sc.ctx, []int64{p.ID}, "post_tag"); len(tags[p.ID]) > 0 {
				var kw []string
				for _, t := range tags[p.ID] {
					kw = append(kw, t.Name)
				}
				art["keywords"] = kw
			}
		}
		graph = append(graph, art)
	}

	if len(trail) > 0 {
		var items []ldNode
		for i, c := range trail {
			it := ldNode{"@type": "ListItem", "position": i + 1, "name": c.Name}
			if c.URL != "" && i < len(trail)-1 {
				it["item"] = c.URL
			}
			items = append(items, it)
		}
		graph = append(graph, ldNode{"@type": "BreadcrumbList", "@id": url + "#breadcrumb", "itemListElement": items})
	}

	// Authors (and the profile of an author archive)
	if a := sc.author(); a != nil && ((sc.singular() && sc.Post.Type == "post" && sc.Kind == "single") || sc.Kind == "author") {
		if personID(home, a) != publisherID {
			graph = append(graph, s.seogoPersonNode(sc, a, home, false))
		}
	}
	for _, q := range faqQuestions {
		graph = append(graph, q)
	}
	for _, h := range howtos {
		h["mainEntityOfPage"] = ldNode{"@id": url}
		graph = append(graph, h)
	}
	if sc.singular() {
		graph = append(graph, seogoCustomSchema(seogoMeta(sc.Post.Meta, "schema_custom"))...)
	}
	return graph
}

func (s *Server) seogoPersonNode(sc *seogoCtx, u *store.User, home string, publisher bool) ldNode {
	n := ldNode{"@type": "Person", "@id": personID(home, u), "name": u.DisplayName}
	if publisher {
		n["@type"] = []string{"Person", "Organization"}
		if logo := s.seogoOpts().KGLogo; logo != "" {
			n["logo"] = ldNode{"@id": home + "#/schema/person/image/"}
		}
	}
	img := s.avatarFor(sc.ctx, u.Email, 96)
	if strings.HasPrefix(img, "/") {
		img = sc.lb.HomeURL(img)
	}
	if img != "" {
		n["image"] = ldNode{"@type": "ImageObject", "inLanguage": s.seogoLanguage(), "@id": home + "#/schema/person/image/", "url": img, "contentUrl": img, "caption": u.DisplayName}
	}
	if d := u.Profile["description"]; d != "" {
		n["description"] = d
	}
	var same []string
	if u.URL != "" {
		same = append(same, u.URL)
	}
	if len(same) > 0 {
		n["sameAs"] = same
	}
	n["url"] = sc.lb.AuthorLink(u.ID, u.Nicename)
	return n
}

// seogoLocalBusiness turns the Organization node into a LocalBusiness (Local SEO).
func (s *Server) seogoLocalBusiness(org ldNode) {
	l := s.seogoOpts().Local
	typ := l.Type
	if typ == "" {
		typ = "LocalBusiness"
	}
	org["@type"] = []string{typ, "Organization"}
	if l.Name != "" {
		org["name"] = l.Name
	}
	addr := ldNode{"@type": "PostalAddress"}
	for k, v := range map[string]string{"streetAddress": l.Street, "addressLocality": l.City, "addressRegion": l.Region, "postalCode": l.Postal, "addressCountry": l.Country} {
		if v != "" {
			addr[k] = v
		}
	}
	if len(addr) > 1 {
		org["address"] = addr
	}
	if l.Phone != "" {
		org["telephone"] = l.Phone
	}
	if l.Email != "" {
		org["email"] = l.Email
	}
	if l.PriceRange != "" {
		org["priceRange"] = l.PriceRange
	}
	if l.Lat != "" && l.Lng != "" {
		org["geo"] = ldNode{"@type": "GeoCoordinates", "latitude": l.Lat, "longitude": l.Lng}
	}
	var hours []string
	for _, line := range strings.Split(l.Hours, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			hours = append(hours, line)
		}
	}
	if len(hours) > 0 {
		org["openingHours"] = hours
	}
}

// seogoProductNode is the Product schema of a post whose schema type is Product.
func (s *Server) seogoProductNode(sc *seogoCtx, url, title, desc string, img *seogoImage) ldNode {
	m := sc.Post.Meta
	get := func(k string) string { v, _ := seogoRaw(m, "_seogo_product_"+k); return v }
	n := ldNode{"@type": "Product", "@id": url + "#product", "name": sc.Post.Title, "mainEntityOfPage": ldNode{"@id": url}}
	if desc != "" {
		n["description"] = desc
	}
	if img != nil {
		n["image"] = ldNode{"@id": url + "#primaryimage"}
	}
	if v := get("sku"); v != "" {
		n["sku"] = v
	}
	if v := get("brand"); v != "" {
		n["brand"] = ldNode{"@type": "Brand", "name": v}
	}
	if price := get("price"); price != "" {
		avail := get("availability")
		if avail == "" {
			avail = "InStock"
		}
		cur := get("currency")
		if cur == "" {
			cur = "USD"
		}
		n["offers"] = ldNode{"@type": "Offer", "price": price, "priceCurrency": cur, "availability": "https://schema.org/" + avail, "url": url}
	}
	if r := get("rating"); r != "" {
		cnt := get("review_count")
		if cnt == "" {
			cnt = "1"
		}
		n["aggregateRating"] = ldNode{"@type": "AggregateRating", "ratingValue": r, "reviewCount": cnt}
	}
	return n
}

// seogoCustomSchema parses the custom JSON-LD of a post: one node, a list or a {"@graph": […]}.
func seogoCustomSchema(raw string) []ldNode {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	var v any
	if json.Unmarshal([]byte(raw), &v) != nil {
		return nil
	}
	var out []ldNode
	var add func(x any)
	add = func(x any) {
		switch t := x.(type) {
		case map[string]any:
			if g, ok := t["@graph"]; ok {
				add(g)
				return
			}
			delete(t, "@context")
			out = append(out, t)
		case []any:
			for _, y := range t {
				add(y)
			}
		}
	}
	add(v)
	return out
}

var tagRe = regexp.MustCompile(`<[^>]*>`)

func textOf(v any) string {
	switch t := v.(type) {
	case string:
		return strings.TrimSpace(tagRe.ReplaceAllString(t, ""))
	case []any: // rich-text arrays of the Yoast blocks
		var sb strings.Builder
		for _, x := range t {
			sb.WriteString(textOf(x))
		}
		return sb.String()
	case map[string]any:
		if p, ok := t["props"].(map[string]any); ok {
			return textOf(p["children"])
		}
	}
	return ""
}

// seogoBlockSchema reads the FAQ and HowTo blocks of the content (Pursae SEO's, Yoast's and Rank Math's).
func seogoBlockSchema(content, url string) (questions, howtos []ldNode) {
	if !strings.Contains(content, "<!-- wp:") {
		return nil, nil
	}
	var walk func([]*blocks.Block)
	walk = func(list []*blocks.Block) {
		for _, b := range list {
			switch b.Name() {
			case "seogo/faq", "yoast/faq-block", "rank-math/faq-block":
				items, _ := b.Attrs["questions"].([]any)
				for i, it := range items {
					q, _ := it.(map[string]any)
					if q == nil || q["visible"] == false {
						continue
					}
					name := textOf(q["question"])
					if name == "" {
						name = textOf(q["jsonQuestion"])
					}
					if name == "" {
						name = textOf(q["title"])
					}
					ans := ""
					for _, k := range []string{"jsonAnswer", "answer", "content"} {
						if a, ok := q[k].(string); ok && a != "" {
							ans = a
							break
						}
					}
					id, _ := q["id"].(string)
					if id == "" {
						id = "faq-" + strconv.Itoa(len(questions)+i+1)
					}
					if name == "" {
						continue
					}
					questions = append(questions, ldNode{"@type": "Question", "@id": url + "#" + id, "position": len(questions) + 1, "url": url + "#" + id,
						"name": name, "answerCount": 1, "acceptedAnswer": ldNode{"@type": "Answer", "text": strings.TrimSpace(ans), "inLanguage": "en-US"}, "inLanguage": "en-US"})
				}
			case "seogo/howto", "yoast/how-to-block", "rank-math/howto-block":
				h := ldNode{"@type": "HowTo", "@id": url + "#howto-" + strconv.Itoa(len(howtos)+1), "name": textOf(b.Attrs["title"])}
				if h["name"] == "" {
					h["name"] = textOf(b.Attrs["jsonTitle"])
				}
				if d := textOf(b.Attrs["description"]); d != "" {
					h["description"] = d
				} else if d := textOf(b.Attrs["jsonDescription"]); d != "" {
					h["description"] = d
				}
				if t, ok := b.Attrs["totalTime"].(string); ok && t != "" {
					h["totalTime"] = t
				} else if d, ok := b.Attrs["days"].(string); ok || b.Attrs["hours"] != nil || b.Attrs["minutes"] != nil {
					hr, _ := b.Attrs["hours"].(string)
					mn, _ := b.Attrs["minutes"].(string)
					h["totalTime"] = "P" + d + map[bool]string{true: "D", false: ""}[d != ""] + "T" + hr + map[bool]string{true: "H", false: ""}[hr != ""] + mn + map[bool]string{true: "M", false: ""}[mn != ""]
				}
				steps, _ := b.Attrs["steps"].([]any)
				var list []ldNode
				for i, it := range steps {
					st, _ := it.(map[string]any)
					if st == nil || st["visible"] == false {
						continue
					}
					id, _ := st["id"].(string)
					if id == "" {
						id = "howto-step-" + strconv.Itoa(i+1)
					}
					name := textOf(st["name"])
					if name == "" {
						name = textOf(st["jsonName"])
					}
					if name == "" {
						name = textOf(st["title"])
					}
					text := textOf(st["text"])
					if text == "" {
						text = textOf(st["jsonText"])
					}
					if text == "" {
						text = textOf(st["content"])
					}
					step := ldNode{"@type": "HowToStep", "url": url + "#" + id, "name": name, "itemListElement": []ldNode{{"@type": "HowToDirection", "text": text}}}
					list = append(list, step)
				}
				h["step"] = list
				h["inLanguage"] = "en-US"
				howtos = append(howtos, h)
			}
			walk(b.InnerBlocks)
		}
	}
	walk(blocks.Parse(content))
	return questions, howtos
}

// seogoSchemaScript is the <script type="application/ld+json"> of a context.
func (s *Server) seogoSchemaScript(sc *seogoCtx, title, desc string) string {
	graph := s.seogoGraph(sc, title, desc)
	b, err := json.Marshal(ldNode{"@context": "https://schema.org", "@graph": graph})
	if err != nil {
		return ""
	}
	// JSON-LD inside <script>: no "</" may end the element early.
	out := strings.ReplaceAll(string(b), "</", `<\/`)
	return `<script type="application/ld+json" class="seogo-schema-graph">` + out + "</script>\n"
}
