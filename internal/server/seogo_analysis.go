package server

import (
	"fmt"
	"html"
	"math"
	"regexp"
	"strings"
	"unicode"
)

// Content analysis: Yoast's SEO and readability assessments (with their thresholds) plus Rank
// Math's extra tests, scored like Yoast: every assessment scores 9 (good), 6 (ok) or 3 (bad) and the
// overall score is the share of the maximum, 71+ good and 41–70 ok. The editors call it through
// /wp-json/seogo/v1/analyze, so one implementation serves the classic editor, Gotenberg and tests.

type seogoAnalysisInput struct {
	SEOTitle    string   `json:"seo_title"` // resolved, as shown in the snippet
	PostTitle   string   `json:"title"`
	Description string   `json:"description"`
	Slug        string   `json:"slug"`
	URL         string   `json:"url"`
	Content     string   `json:"content"` // HTML
	Keywords    []string `json:"keywords"`
	Cornerstone bool     `json:"cornerstone"`
	Taxonomy    bool     `json:"taxonomy"`
	UsedBy      []string `json:"-"` // titles of other posts with the same focus keyphrase
	SiteHost    string   `json:"-"`
}

type seogoResult struct {
	ID     string `json:"id"`
	Rating string `json:"rating"` // good, ok, bad
	Score  int    `json:"score"`
	Text   string `json:"text"`
	Group  string `json:"group,omitempty"` // "" scored; "extra" Rank Math tip; "insight"
}

type seogoAnalysis struct {
	Score   int           `json:"score"`
	Rating  string        `json:"rating"`
	Results []seogoResult `json:"results"`
	Points  int           `json:"points,omitempty"` // Rank Math's 100-point score (SEO only)
}

func ratingOf(score int) string {
	switch {
	case score >= 71:
		return "good"
	case score >= 41:
		return "ok"
	}
	return "bad"
}

// finish computes Yoast's SEO score: Σscores / (n × 9) × 100 over the scored results.
func finish(results []seogoResult) seogoAnalysis {
	sum, n := 0, 0
	for _, r := range results {
		if r.Group == "" {
			sum += r.Score
			n++
		}
	}
	if n == 0 {
		return seogoAnalysis{Rating: "na", Results: results}
	}
	score := int(math.Round(float64(sum) / float64(n*9) * 100))
	if score < 0 {
		score = 0
	}
	sortResults(results)
	return seogoAnalysis{Score: score, Rating: ratingOf(score), Results: results}
}

func sortResults(results []seogoResult) {
	// problems first, like the Yoast sidebar; unscored extras last
	order := map[string]int{"bad": 0, "ok": 1, "good": 2}
	rank := func(r seogoResult) int {
		if r.Group != "" {
			return 10 + order[r.Rating]
		}
		return order[r.Rating]
	}
	for i := 1; i < len(results); i++ {
		for j := i; j > 0 && rank(results[j]) < rank(results[j-1]); j-- {
			results[j], results[j-1] = results[j-1], results[j]
		}
	}
}

func res(id, rating, text string, a ...any) seogoResult {
	return seogoResult{ID: id, Rating: rating, Score: map[string]int{"good": 9, "ok": 6, "bad": 3}[rating], Text: fmt.Sprintf(text, a...)}
}

// scored is res with Yoast's own score for the outcome (they differ per assessment).
func scored(score int, id, text string, a ...any) seogoResult {
	r := seogoResult{ID: id, Score: score, Text: fmt.Sprintf(text, a...)}
	switch {
	case score > 7:
		r.Rating = "good"
	case score >= 5:
		r.Rating = "ok"
	default:
		r.Rating = "bad"
	}
	return r
}

// --- text helpers ------------------------------------------------------------------------------

var (
	paraSplitRe   = regexp.MustCompile(`(?is)</p>|<br\s*/?>\s*<br\s*/?>|\n\s*\n|</h[1-6]>|</li>|</blockquote>`)
	headingRe     = regexp.MustCompile(`(?is)<h([1-6])[^>]*>(.*?)</h[1-6]>`)
	anchorRe      = regexp.MustCompile(`(?is)<a\s[^>]*href=["']([^"']*)["'][^>]*>(.*?)</a>`)
	relRe         = regexp.MustCompile(`(?i)\srel=["']([^"']*)["']`)
	imgTagRe      = regexp.MustCompile(`(?is)<img\s[^>]*>`)
	altRe         = regexp.MustCompile(`(?i)\salt=["']([^"']*)["']`)
	sentenceEndRe = regexp.MustCompile(`([.!?]+)(\s+|$)`)
	wordRe        = regexp.MustCompile(`[\p{L}\p{N}][\p{L}\p{N}'’-]*`)
	numberRe      = regexp.MustCompile(`\d`)
)

func words(text string) []string {
	return wordRe.FindAllString(strings.ToLower(text), -1)
}

// sentences splits text into sentences (on . ! ? and paragraph ends).
func sentences(content string) []string {
	var out []string
	for _, para := range paragraphs(content) {
		for _, s := range sentenceEndRe.Split(para, -1) {
			if s = strings.TrimSpace(s); s != "" && len(words(s)) > 0 {
				out = append(out, s)
			}
		}
	}
	return out
}

// paragraphs are the text blocks of the content (paragraphs, list items, headings excluded).
func paragraphs(content string) []string {
	body := headingRe.ReplaceAllString(stripBlocks(content), "\n\n")
	var out []string
	for _, p := range paraSplitRe.Split(body, -1) {
		t := strings.Join(strings.Fields(html.UnescapeString(tagRe.ReplaceAllString(p, " "))), " ")
		if t != "" {
			out = append(out, t)
		}
	}
	return out
}

var functionWords = map[string]bool{}

func init() {
	for _, w := range strings.Fields(`a an the and or but nor of in on at to for from by with without about as into onto over under is are was were be been being am do does did
		have has had i you he she it we they me him her us them my your his its our their this that these those what which who whom whose how why when where
		not no so than too very can could will would shall should may might must just also then there here all any each few more most other some such only own same`) {
		functionWords[w] = true
	}
}

// contentWords are the words of a keyphrase that count (function words are ignored, like Yoast).
func contentWords(phrase string) []string {
	var out []string
	for _, w := range words(phrase) {
		if !functionWords[w] {
			out = append(out, w)
		}
	}
	if len(out) == 0 {
		return words(phrase)
	}
	return out
}

func stem(w string) string {
	for _, suf := range []string{"ing", "ies", "es", "s", "ed", "ly"} {
		if len(w) > len(suf)+3 && strings.HasSuffix(w, suf) {
			return strings.TrimSuffix(w, suf)
		}
	}
	return w
}

// containsPhrase reports an exact (word-bounded, case-insensitive) occurrence of the phrase.
func containsPhrase(text, phrase string) bool {
	return countPhrase(text, phrase) > 0
}

func countPhrase(text, phrase string) int {
	pw := words(phrase)
	tw := words(text)
	if len(pw) == 0 {
		return 0
	}
	n := 0
	for i := 0; i+len(pw) <= len(tw); i++ {
		ok := true
		for j, w := range pw {
			if stem(tw[i+j]) != stem(w) {
				ok = false
				break
			}
		}
		if ok {
			n++
		}
	}
	return n
}

// allWordsIn reports whether every content word of the phrase occurs in the text, in any order.
func allWordsIn(text, phrase string) bool {
	set := map[string]bool{}
	for _, w := range words(text) {
		set[stem(w)] = true
	}
	for _, w := range contentWords(phrase) {
		if !set[stem(w)] {
			return false
		}
	}
	return true
}

// titleWidth approximates the pixel width of a title in Google's desktop results (Arial 20px).
func titleWidth(t string) int {
	w := 0.0
	for _, r := range t {
		switch {
		case strings.ContainsRune("iljI.,;:!|'", r):
			w += 4.5
		case strings.ContainsRune("ftr()[]- ", r):
			w += 6
		case strings.ContainsRune("mwMW", r):
			w += 16
		case unicode.IsUpper(r) || unicode.IsDigit(r):
			w += 12.5
		default:
			w += 10.2
		}
	}
	return int(w)
}

func syllables(w string) int {
	w = strings.ToLower(w)
	n, prev := 0, false
	for _, r := range w {
		v := strings.ContainsRune("aeiouy", r)
		if v && !prev {
			n++
		}
		prev = v
	}
	if strings.HasSuffix(w, "e") && !strings.HasSuffix(w, "le") && n > 1 {
		n--
	}
	if n < 1 {
		n = 1
	}
	return n
}

var transitionWords = strings.Split(`accordingly|additionally|afterward|afterwards|albeit|also|although|altogether|another|basically|because|before|besides|but|certainly|chiefly|comparatively|concurrently|consequently|contrarily|conversely|correspondingly|despite|doubtedly|during|e.g.|earlier|emphatically|equally|especially|eventually|evidently|explicitly|finally|firstly|following|formerly|forthwith|fourthly|further|furthermore|generally|hence|henceforth|however|i.e.|identically|indeed|initially|instead|last|lastly|later|lest|likewise|markedly|meanwhile|moreover|nevertheless|nonetheless|nor|notwithstanding|obviously|occasionally|otherwise|overall|particularly|presently|previously|rather|regardless|secondly|shortly|significantly|similarly|simultaneously|since|so|soon|specifically|still|straightaway|subsequently|surely|surprisingly|than|then|thereafter|therefore|thereupon|thirdly|though|thus|till|undeniably|undoubtedly|unless|unlike|unquestionably|until|when|whenever|whereas|while|above all|after all|after that|all in all|all of a sudden|all things considered|another key point|as a matter of fact|as a result|as an illustration|as can be seen|as has been noted|as I have noted|as I have said|as I have shown|as long as|as much as|as shown above|as soon as|as well as|at any rate|at first|at last|at least|at length|at the present time|at the same time|at this instant|at this point|at this time|balanced against|being that|by all means|by and large|by comparison|by the same token|by the time|compared to|be that as it may|coupled with|different from|due to|equally important|even if|even more|even so|even though|first thing to remember|for example|for fear that|for instance|for one thing|for that reason|for the most part|for the purpose of|for the same reason|for this purpose|for this reason|from time to time|given that|given these points|important to realize|in a word|in addition|in another case|in any case|in any event|in brief|in case|in conclusion|in contrast|in detail|in due time|in effect|in either case|in essence|in fact|in general|in light of|in like fashion|in like manner|in order that|in order to|in other words|in particular|in reality|in short|in similar fashion|in spite of|in sum|in summary|in that case|in the event that|in the final analysis|in the first place|in the fourth place|in the hope that|in the light of|in the long run|in the meantime|in the same fashion|in the same way|in the second place|in the third place|in this case|in this situation|in time|in truth|in view of|inasmuch as|most compelling evidence|most important|must be remembered|not only|not to mention|now that|of course|on account of|on balance|on condition that|on the contrary|on the negative side|on the other hand|on the positive side|on the whole|on this occasion|once|only if|owing to|point often overlooked|prior to|provided that|seeing that|so as to|so far|so long as|so that|sooner or later|such as|summing up|take the case of|that is|that is to say|then again|this time|to be sure|to begin with|to clarify|to conclude|to demonstrate|to emphasize|to enumerate|to explain|to illustrate|to list|to point out|to put it another way|to put it differently|to repeat|to rephrase it|to say nothing of|to sum up|to summarize|to that end|to the end that|to this end|together with|under those circumstances|until now|up against|up to the present time|vis a vis|what's more|while it may be true|while this may be true|with attention to|with the result that|with this in mind|with this intention|with this purpose in mind|without a doubt|without delay|without doubt|without reservation`, "|")

var passiveRe = regexp.MustCompile(`(?i)\b(am|is|are|was|were|be|been|being|get|gets|got|gotten|getting)\s+(\w+ly\s+)?(\w+ed|built|done|made|seen|given|taken|written|known|shown|found|told|sold|held|kept|left|lost|paid|put|read|run|said|sent|set|spent|thought|understood|won|chosen|driven|eaten|fallen|forgotten|hidden|broken|spoken|stolen|worn|born|brought|bought|caught|taught|fought|meant|drawn|grown|thrown|begun|sung|sunk|swum|hung|struck|shut|cut|hit|hurt|let)\b`)

// --- SEO analysis (Yoast's assessments and scores) ----------------------------------------------

type linkStats struct {
	internal, intFollow, external, extFollow, competing int
}

func countLinks(content, host, kw string) linkStats {
	var st linkStats
	for _, m := range anchorRe.FindAllStringSubmatch(content, -1) {
		href := strings.TrimSpace(m[1])
		if href == "" || strings.HasPrefix(href, "#") || strings.HasPrefix(href, "mailto:") || strings.HasPrefix(href, "tel:") {
			continue
		}
		nofollow := false
		if r := relRe.FindStringSubmatch(m[0]); r != nil && strings.Contains(strings.ToLower(r[1]), "nofollow") {
			nofollow = true
		}
		if strings.Contains(href, "://") && (host == "" || !strings.Contains(href, "://"+host)) {
			st.external++
			if !nofollow {
				st.extFollow++
			}
		} else {
			st.internal++
			if !nofollow {
				st.intFollow++
			}
		}
		if kw != "" && sameWords(tagRe.ReplaceAllString(m[2], ""), kw) {
			st.competing++
		}
	}
	return st
}

// sameWords reports whether a text consists of exactly the keyphrase's content words.
func sameWords(text, kw string) bool {
	a, b := contentWords(text), contentWords(kw)
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if stem(a[i]) != stem(b[i]) {
			return false
		}
	}
	return true
}

// halfWordsIn reports whether at least half of the keyphrase's content words are in the text.
func halfWordsIn(text, kw string) bool {
	set := map[string]bool{}
	for _, w := range words(text) {
		set[stem(w)] = true
	}
	cw := contentWords(kw)
	n := 0
	for _, w := range cw {
		if set[stem(w)] {
			n++
		}
	}
	return len(cw) > 0 && n*2 >= len(cw)
}

func linkResults(id, label string, total, follow int) seogoResult {
	switch {
	case total == 0 && id == "externalLinks":
		return scored(3, id, "%s: No outbound links appear in this page. Add some!", label)
	case total == 0:
		return scored(3, id, "%s: No internal links appear in this page, make sure to add some!", label)
	case follow == 0:
		return scored(7, id, "%s: All %s links on this page are nofollowed. Add some normal links.", label, strings.ToLower(strings.Fields(label)[0]))
	case follow < total:
		return scored(8, id, "%s: There are both nofollowed and normal links on this page. Good job!", label)
	}
	return scored(9, id, "%s: Good job!", label)
}

func seogoAnalyzeSEO(in seogoAnalysisInput) seogoAnalysis {
	var out []seogoResult
	text := plainText(in.Content)
	tw := words(text)
	nWords := len(tw)
	kw := ""
	if len(in.Keywords) > 0 {
		kw = strings.TrimSpace(in.Keywords[0])
	}
	corner, tax := in.Cornerstone, in.Taxonomy

	// Text length
	switch {
	case tax:
		switch {
		case nWords >= 30:
			out = append(out, scored(9, "textLength", "Text length: The text contains %d words. Good job!", nWords))
		case nWords >= 10:
			out = append(out, scored(6, "textLength", "Text length: The text contains %d words. This is below the recommended minimum of 30 words. Add more content.", nWords))
		case nWords >= 1:
			out = append(out, scored(3, "textLength", "Text length: The text contains %d words. This is far below the recommended minimum of 30 words. Add more content.", nWords))
		default:
			out = append(out, scored(-20, "textLength", "Text length: The text contains 0 words. This is far below the recommended minimum of 30 words. Add more content."))
		}
	case corner:
		switch {
		case nWords >= 900:
			out = append(out, scored(9, "textLength", "Text length: The text contains %d words. Good job!", nWords))
		case nWords >= 400:
			out = append(out, scored(6, "textLength", "Text length: The text contains %d words. This is below the recommended minimum of 900 words. Add more content.", nWords))
		default:
			out = append(out, scored(-20, "textLength", "Text length: The text contains %d words. This is far below the recommended minimum of 900 words. Add more content.", nWords))
		}
	default:
		switch {
		case nWords >= 300:
			out = append(out, scored(9, "textLength", "Text length: The text contains %d words. Good job!", nWords))
		case nWords >= 250:
			out = append(out, scored(6, "textLength", "Text length: The text contains %d words. This is slightly below the recommended minimum of 300 words. Add a bit more copy.", nWords))
		case nWords >= 200:
			out = append(out, scored(3, "textLength", "Text length: The text contains %d words. This is below the recommended minimum of 300 words. Add more content.", nWords))
		case nWords >= 100:
			out = append(out, scored(-10, "textLength", "Text length: The text contains %d words. This is far below the recommended minimum of 300 words. Add more content.", nWords))
		default:
			out = append(out, scored(-20, "textLength", "Text length: The text contains %d words. This is far below the recommended minimum of 300 words. Add more content.", nWords))
		}
	}

	// SEO title width: short titles stay green, wider than 600 px is red
	width := titleWidth(in.SEOTitle)
	switch {
	case strings.TrimSpace(in.SEOTitle) == "":
		out = append(out, scored(1, "titleWidth", "SEO title width: Please create an SEO title."))
	case width > 600:
		out = append(out, scored(3, "titleWidth", "SEO title width: The SEO title is wider than the viewable limit (about %d of 600 pixels). Try to make it shorter.", width))
	default:
		out = append(out, scored(9, "titleWidth", "SEO title width: Good job!"))
	}

	// Meta description length: 121–156 characters
	dl := len([]rune(strings.TrimSpace(in.Description)))
	off := 6
	if corner {
		off = 3
	}
	switch {
	case dl == 0:
		out = append(out, scored(1, "metaDescriptionLength", "Meta description length: No meta description has been specified. Search engines will display copy from the page instead. Make sure to write one!"))
	case dl <= 120:
		out = append(out, scored(off, "metaDescriptionLength", "Meta description length: The meta description is too short (under 120 characters). Up to 156 characters are available. Use the space!"))
	case dl > 156:
		out = append(out, scored(off, "metaDescriptionLength", "Meta description length: The meta description is over 156 characters. To ensure the entire description will be visible, you should reduce the length!"))
	default:
		out = append(out, scored(9, "metaDescriptionLength", "Meta description length: Well done!"))
	}

	// Single title
	h1 := 0
	for _, m := range headingRe.FindAllStringSubmatch(in.Content, -1) {
		if m[1] == "1" {
			h1++
		}
	}
	if h1 >= 2 {
		out = append(out, scored(1, "singleH1", "Single title: H1s should only be used as your main title. Find all H1s in your text that aren't your main title and change them to a lower heading level!"))
	} else {
		out = append(out, scored(8, "singleH1", "Single title: You don't have multiple H1 headings, well done!"))
	}

	imgs := imgTagRe.FindAllString(in.Content, -1)
	st := countLinks(in.Content, in.SiteHost, kw)
	if !tax {
		out = append(out, linkResults("externalLinks", "Outbound links", st.external, st.extFollow))
		out = append(out, linkResults("internalLinks", "Internal links", st.internal, st.intFollow))
		if len(imgs) == 0 {
			out = append(out, scored(3, "images", "Images: No images appear on this page. Add some!"))
		} else {
			out = append(out, scored(9, "images", "Images: Good job!"))
		}
	}

	if kw == "" {
		out = append(out, scored(-999, "keyphraseLength", "Keyphrase length: No focus keyphrase was set for this page. Set a keyphrase in order to calculate your SEO score."))
		a := finish(out)
		a.Score, a.Rating = 0, "na"
		return a
	}
	cw := contentWords(kw)

	// Keyphrase length
	switch n := len(cw); {
	case n <= 4:
		out = append(out, scored(9, "keyphraseLength", "Keyphrase length: Good job!"))
	case n <= 8:
		out = append(out, scored(6, "keyphraseLength", "Keyphrase length: The keyphrase is %d words long. That's more than the recommended maximum of 4 words. Make it shorter!", n))
	default:
		out = append(out, scored(3, "keyphraseLength", "Keyphrase length: The keyphrase is %d words long. That's way more than the recommended maximum of 4 words. Make it shorter!", n))
	}

	// Keyphrase in introduction
	paras := paragraphs(in.Content)
	intro := ""
	if len(paras) > 0 {
		intro = paras[0]
	}
	introGood := false
	for _, sen := range sentenceEndRe.Split(intro, -1) {
		if allWordsIn(sen, kw) {
			introGood = true
		}
	}
	switch {
	case introGood:
		out = append(out, scored(9, "introductionKeyword", "Keyphrase in introduction: Well done!"))
	case allWordsIn(intro, kw):
		out = append(out, scored(6, "introductionKeyword", "Keyphrase in introduction: Your keyphrase or its synonyms appear in the first paragraph of the copy, but not within one sentence. Fix that!"))
	default:
		out = append(out, scored(3, "introductionKeyword", "Keyphrase in introduction: Your keyphrase or its synonyms do not appear in the first paragraph. Make sure the topic is clear immediately."))
	}

	// Keyphrase density, scaled by the keyphrase length factor 0.7 + words/3
	count := countPhrase(text, kw)
	switch {
	case nWords >= 100:
		density := float64(count) / float64(nWords) * 100 * (0.7 + float64(len(cw))/3)
		minCount := int(math.Ceil(0.5 / 100 * float64(nWords) / (0.7 + float64(len(cw))/3)))
		switch {
		case count == 0 || density < 0.5:
			out = append(out, scored(4, "keyphraseDensity", "Keyphrase density: The keyphrase was found %d times. That's less than the recommended minimum of %d times for a text of this length. Focus on your keyphrase!", count, minCount))
		case density > 4:
			out = append(out, scored(-50, "keyphraseDensity", "Keyphrase density: The keyphrase was found %d times. That's way more than the recommended maximum for a text of this length. Don't overoptimize!", count))
		case density > 3:
			out = append(out, scored(-10, "keyphraseDensity", "Keyphrase density: The keyphrase was found %d times. That's more than the recommended maximum for a text of this length. Don't overoptimize!", count))
		default:
			out = append(out, scored(9, "keyphraseDensity", "Keyphrase density: The keyphrase was found %d times. This is great!", count))
		}
	case nWords > 50:
		switch {
		case count >= 4:
			out = append(out, scored(-50, "keyphraseDensity", "Keyphrase density: The keyphrase was found %d times. That's way more than the recommended maximum of 2 times for a text of this length. Don't overoptimize!", count))
		case count == 3:
			out = append(out, scored(-10, "keyphraseDensity", "Keyphrase density: The keyphrase was found 3 times. That's more than the recommended maximum of 2 times for a text of this length. Don't overoptimize!"))
		case count >= 1:
			out = append(out, scored(9, "keyphraseDensity", "Keyphrase density: The keyphrase was found %d times. This is great!", count))
		default:
			out = append(out, scored(4, "keyphraseDensity", "Keyphrase density: The keyphrase was found 0 times. Focus on your keyphrase!"))
		}
	case nWords > 0:
		switch {
		case count >= 3:
			out = append(out, scored(-50, "keyphraseDensity", "Keyphrase density: The keyphrase was found %d times. That's way more than the recommended maximum of 1 time for a text of this length. Don't overoptimize!", count))
		case count == 2:
			out = append(out, scored(-10, "keyphraseDensity", "Keyphrase density: The keyphrase was found 2 times. That's more than the recommended maximum of 1 time for a text of this length. Don't overoptimize!"))
		case count == 1:
			out = append(out, scored(9, "keyphraseDensity", "Keyphrase density: The keyphrase was found 1 time. This is great!"))
		default:
			out = append(out, scored(4, "keyphraseDensity", "Keyphrase density: The keyphrase was found 0 times. Focus on your keyphrase!"))
		}
	}

	// Keyphrase in SEO title (leading function words ignored)
	titleWords := words(in.SEOTitle)
	for len(titleWords) > 0 && functionWords[titleWords[0]] {
		titleWords = titleWords[1:]
	}
	kwWords := words(kw)
	for len(kwWords) > 1 && functionWords[kwWords[0]] {
		kwWords = kwWords[1:]
	}
	atStart := len(titleWords) >= len(kwWords)
	for i := 0; atStart && i < len(kwWords); i++ {
		atStart = stem(titleWords[i]) == stem(kwWords[i])
	}
	switch {
	case atStart:
		out = append(out, scored(9, "keyphraseInTitle", "Keyphrase in SEO title: The exact match of the focus keyphrase appears at the beginning of the SEO title. Good job!"))
	case containsPhrase(in.SEOTitle, kw):
		out = append(out, scored(6, "keyphraseInTitle", "Keyphrase in SEO title: The exact match of the focus keyphrase appears in the SEO title, but not at the beginning. Move it to the beginning for the best results."))
	case allWordsIn(in.SEOTitle, kw):
		out = append(out, scored(6, "keyphraseInTitle", "Keyphrase in SEO title: Does not contain the exact match. Try to write the exact match of your keyphrase in the SEO title and put it at the beginning of the title."))
	default:
		out = append(out, scored(2, "keyphraseInTitle", "Keyphrase in SEO title: Not all the words from your keyphrase appear in the SEO title. For the best SEO results write the exact match of your keyphrase in the SEO title, and put the keyphrase at the beginning of the title."))
	}

	// Keyphrase in meta description: 1–2 sentences
	if strings.TrimSpace(in.Description) == "" {
		out = append(out, scored(3, "metaDescriptionKeyword", "Keyphrase in meta description: Please add both a keyphrase and a meta description containing the keyphrase."))
	} else {
		n := 0
		for _, sen := range sentenceEndRe.Split(in.Description, -1) {
			if strings.TrimSpace(sen) != "" && allWordsIn(sen, kw) {
				n++
			}
		}
		switch {
		case n == 0:
			out = append(out, scored(3, "metaDescriptionKeyword", "Keyphrase in meta description: The meta description has been specified, but it does not contain the keyphrase. Fix that!"))
		case n > 2:
			out = append(out, scored(3, "metaDescriptionKeyword", "Keyphrase in meta description: The meta description contains the keyphrase %d times, which is over the advised maximum of 2 times. Limit that!", n))
		default:
			out = append(out, scored(9, "metaDescriptionKeyword", "Keyphrase in meta description: Keyphrase or synonym appear in the meta description. Well done!"))
		}
	}

	// Keyphrase in slug: 1–2 word keyphrases need all words, longer ones more than half
	if in.Slug != "" {
		slugText := strings.NewReplacer("-", " ", "_", " ").Replace(in.Slug)
		set := map[string]bool{}
		for _, w := range words(slugText) {
			set[stem(w)] = true
		}
		found := 0
		for _, w := range cw {
			if set[stem(w)] {
				found++
			}
		}
		partial := 6
		if corner {
			partial = 3
		}
		switch {
		case found == 0:
			out = append(out, scored(3, "slugKeyword", "Keyphrase in slug: The slug does not contain the keyphrase. Change that!"))
		case (len(cw) <= 2 && found == len(cw)) || (len(cw) > 2 && found*2 > len(cw)):
			out = append(out, scored(9, "slugKeyword", "Keyphrase in slug: Great work!"))
		default:
			out = append(out, scored(partial, "slugKeyword", "Keyphrase in slug: (Part of) your keyphrase does not appear in the slug. Change that!"))
		}
	}

	if !tax {
		// Keyphrase in subheadings (H2/H3): 30–75%
		var subs []string
		for _, m := range headingRe.FindAllStringSubmatch(in.Content, -1) {
			if m[1] == "2" || m[1] == "3" {
				subs = append(subs, tagRe.ReplaceAllString(m[2], ""))
			}
		}
		limit := 300
		if corner {
			limit = 250
		}
		with := 0
		for _, h := range subs {
			if halfWordsIn(h, kw) {
				with++
			}
		}
		switch {
		case len(subs) == 0 && nWords <= limit:
			out = append(out, scored(9, "subheadingsKeyword", "Keyphrase in subheading: You are not using any higher-level subheadings containing the keyphrase or its synonyms, but your text is short enough."))
		case len(subs) == 0:
			out = append(out, scored(2, "subheadingsKeyword", "Keyphrase in subheading: You are not using any higher-level subheadings containing the keyphrase or its synonyms. Fix that!"))
		case (len(subs) == 1 && with == 1) || (float64(with)/float64(len(subs)) >= 0.3 && float64(with)/float64(len(subs)) <= 0.75):
			out = append(out, scored(9, "subheadingsKeyword", "Keyphrase in subheading: Your higher-level subheadings reflect the topic of your copy. Good job!"))
		case with == 0 || float64(with)/float64(len(subs)) < 0.3:
			out = append(out, scored(3, "subheadingsKeyword", "Keyphrase in subheading: Use more keyphrases or synonyms in your higher-level subheadings!"))
		default:
			out = append(out, scored(3, "subheadingsKeyword", "Keyphrase in subheading: More than 75%% of your higher-level subheadings reflect the topic of your copy. That's too much. Don't over-optimize!"))
		}

		// Image keyphrase: alt attributes with at least half of the content words
		withKw, withAlt := 0, 0
		for _, img := range imgs {
			if m := altRe.FindStringSubmatch(img); m != nil && strings.TrimSpace(m[1]) != "" {
				withAlt++
				if halfWordsIn(html.UnescapeString(m[1]), kw) {
					withKw++
				}
			}
		}
		none := 6
		if corner {
			none = 3
		}
		switch {
		case len(imgs) == 0:
			out = append(out, scored(3, "imageKeyphrase", "Image keyphrase: Add images and add your keyphrase or synonyms to the alt tags of relevant images!"))
		case withKw == 0:
			out = append(out, scored(none, "imageKeyphrase", "Image keyphrase: Images on this page do not have alt attributes with at least half of the words from your keyphrase. Fix that!"))
		case len(imgs) >= 5 && (float64(withKw)/float64(len(imgs)) < 0.3 || float64(withKw)/float64(len(imgs)) > 0.75):
			out = append(out, scored(6, "imageKeyphrase", "Image keyphrase: Out of %d images on this page, %d have alt attributes with words from your keyphrase or synonyms. Aim for 30%% to 75%%.", len(imgs), withKw))
		default:
			out = append(out, scored(9, "imageKeyphrase", "Image keyphrase: Good job!"))
		}

		// Competing links
		if st.competing > 0 {
			out = append(out, scored(2, "competingLinks", "Competing links: You're linking to another page with the words you want this page to rank for. Don't do that!"))
		} else {
			out = append(out, scored(8, "competingLinks", "Competing links: There are no links which use your keyphrase or synonym as their anchor text. Nice!"))
		}
	}

	// Previously used keyphrase
	switch n := len(in.UsedBy); {
	case n == 1:
		out = append(out, scored(6, "previouslyUsedKeywords", "Previously used keyphrase: You've used this keyphrase once before (in “%s”). Do not use your keyphrase more than once.", in.UsedBy[0]))
	case n > 1:
		out = append(out, scored(1, "previouslyUsedKeywords", "Previously used keyphrase: You've used this keyphrase %d times before. Do not use your keyphrase more than once.", n))
	default:
		out = append(out, scored(9, "previouslyUsedKeywords", "Previously used keyphrase: You've not used this keyphrase before, very good."))
	}

	// Related keyphrases (Yoast Premium) / secondary keywords (Rank Math): shown, not scored
	for _, extra := range in.Keywords[1:] {
		if extra = strings.TrimSpace(extra); extra == "" {
			continue
		}
		r := scored(3, "relatedKeyphrase", "Related keyphrase “%s”: does not appear in the text. Use it!", extra)
		if c := countPhrase(text, extra); c > 0 || allWordsIn(text, extra) {
			r = scored(9, "relatedKeyphrase", "Related keyphrase “%s”: appears in the text. Good job!", extra)
		}
		r.Group = "extra"
		out = append(out, r)
	}

	// Rank Math's tests that Yoast has no counterpart for: shown as tips, not scored
	for _, r := range seogoRankMathTips(in, kw, tw) {
		r.Group = "extra"
		out = append(out, r)
	}
	a := finish(out)
	a.Points = seogoRankMathPoints(in, kw, tw, st, imgs)
	return a
}

var powerWords = map[string]bool{}

func init() {
	for _, w := range strings.Fields(`amazing awesome best brilliant complete definitive easy effortless essential exclusive expert free guaranteed incredible instant
		powerful proven quick remarkable secret simple stunning surprising ultimate unique unbelievable vital wonderful new now how why mistakes tips tricks`) {
		powerWords[w] = true
	}
}

func seogoRankMathTips(in seogoAnalysisInput, kw string, tw []string) []seogoResult {
	var out []seogoResult
	if len(tw) > 0 {
		head := tw
		if len(tw) > 400 {
			head = tw[:len(tw)/10]
		}
		if containsPhrase(strings.Join(head, " "), kw) {
			out = append(out, scored(9, "keywordIn10Percent", "Focus Keyword appears in the first 10%% of the content."))
		} else {
			out = append(out, scored(3, "keywordIn10Percent", "Use the Focus Keyword near the beginning of your content."))
		}
	}
	if in.URL != "" {
		if l := len(in.URL); l > 75 {
			out = append(out, scored(3, "lengthPermalink", "URL is %d characters long. Consider shortening it to 75 characters or less.", l))
		} else {
			out = append(out, scored(9, "lengthPermalink", "URL is %d characters long. Kudos!", l))
		}
	}
	if numberRe.MatchString(in.SEOTitle) {
		out = append(out, scored(9, "titleHasNumber", "You are using a number in your SEO title."))
	} else {
		out = append(out, scored(6, "titleHasNumber", "Your SEO title doesn't contain a number."))
	}
	hasPower := false
	for _, w := range words(in.SEOTitle) {
		hasPower = hasPower || powerWords[w]
	}
	if hasPower {
		out = append(out, scored(9, "titleHasPowerWords", "Your title contains a power word. Good job!"))
	} else {
		out = append(out, scored(6, "titleHasPowerWords", "Your title doesn't contain a power word. Add at least one."))
	}
	short := true
	for _, p := range paragraphs(in.Content) {
		if len(words(p)) > 120 {
			short = false
		}
	}
	if short {
		out = append(out, scored(9, "contentHasShortParagraphs", "You are using short paragraphs."))
	} else {
		out = append(out, scored(3, "contentHasShortParagraphs", "At least one paragraph is long. Consider using short paragraphs."))
	}
	if strings.Contains(in.Content, "wp:seogo/toc") || strings.Contains(in.Content, "table-of-contents") || strings.Contains(in.Content, `id="toc"`) {
		out = append(out, scored(9, "contentHasTOC", "You seem to be using a Table of Contents to break down your text."))
	}
	return out
}

// seogoRankMathPoints is Rank Math's 100-point score of the primary keyword (test weights of
// the plugin's analyzer; English, Content AI not counted).
func seogoRankMathPoints(in seogoAnalysisInput, kw string, tw []string, st linkStats, imgs []string) int {
	if kw == "" {
		return 0
	}
	earned, total := 0, 0
	add := func(ok bool, pts int) {
		total += pts
		if ok {
			earned += pts
		}
	}
	text := strings.Join(tw, " ")
	add(containsPhrase(in.SEOTitle, kw), 36)
	add(containsPhrase(in.Description, kw), 2)
	if in.Slug != "" {
		add(allWordsIn(strings.ReplaceAll(in.Slug, "-", " "), kw), 5)
	}
	head := tw
	if len(tw) > 400 {
		head = tw[:len(tw)/10]
	}
	add(containsPhrase(strings.Join(head, " "), kw), 3)
	add(containsPhrase(text, kw), 3)
	n := len(tw)
	lp := 0
	switch {
	case n >= 2500:
		lp = 8
	case n >= 2000:
		lp = 5
	case n >= 1500:
		lp = 4
	case n >= 1000:
		lp = 3
	case n >= 600:
		lp = 2
	}
	earned += lp
	total += 8
	sub := false
	for _, m := range headingRe.FindAllStringSubmatch(in.Content, -1) {
		if m[1] != "1" && containsPhrase(tagRe.ReplaceAllString(m[2], ""), kw) {
			sub = true
		}
	}
	add(sub, 3)
	alt := false
	for _, img := range imgs {
		if m := altRe.FindStringSubmatch(img); m != nil && containsPhrase(html.UnescapeString(m[1]), kw) {
			alt = true
		}
	}
	add(alt, 2)
	if n > 0 {
		d := float64(countPhrase(text, kw)*len(words(kw))) / float64(n) * 100
		dp := 0
		switch {
		case d > 2.5:
		case d > 1.0:
			dp = 6
		case d >= 0.76:
			dp = 3
		case d >= 0.5:
			dp = 2
		}
		earned += dp
		total += 6
	}
	if in.URL != "" {
		add(len(in.URL) <= 75, 4)
	}
	add(st.external > 0, 4)
	add(st.extFollow > 0, 2)
	add(st.internal > 0, 5)
	tl := strings.ToLower(in.SEOTitle)
	add(strings.Index(tl, strings.ToLower(kw)) >= 0 && strings.Index(tl, strings.ToLower(kw)) < len(tl)/2, 3)
	power := false
	for _, w := range words(in.SEOTitle) {
		power = power || powerWords[w]
	}
	add(power, 1)
	add(numberRe.MatchString(in.SEOTitle), 1)
	short := true
	for _, p := range paragraphs(in.Content) {
		if len(words(p)) > 120 {
			short = false
		}
	}
	add(short, 3)
	assets := len(imgs)
	ap := map[int]int{0: 0, 1: 1, 2: 2, 3: 4}[min(assets, 3)]
	if assets >= 4 {
		ap = 6
	}
	vids := len(ytRe.FindAllString(in.Content, -1)) + len(vimeoRe.FindAllString(in.Content, -1))
	ap += min(vids, 2)
	earned += min(ap, 6)
	total += 6
	if total == 0 {
		return 0
	}
	return int(math.Round(float64(earned) / float64(total) * 100))
}

// --- Readability analysis (Yoast's content assessor) --------------------------------------------

func seogoAnalyzeReadability(in seogoAnalysisInput) seogoAnalysis {
	var out []seogoResult
	text := plainText(in.Content)
	tw := words(text)
	corner := in.Cornerstone
	if len([]rune(text)) < 50 {
		out = append(out, scored(3, "textPresence", "Not enough content: Please add some content to enable a good analysis."))
	}
	sens := sentences(in.Content)

	// Subheading distribution
	secGood, secBad := 300, 350
	if corner {
		secGood, secBad = 250, 300
	}
	if len(tw) > 0 {
		sections := headingRe.Split(stripBlocks(in.Content), -1)
		maxSec := 0
		for _, sec := range sections {
			if n := len(words(plainText(sec))); n > maxSec {
				maxSec = n
			}
		}
		switch {
		case len(sections) == 1 && len(tw) <= secGood:
			out = append(out, scored(9, "subheadingsTooLong", "Subheading distribution: You are not using any subheadings, but your text is short enough and probably doesn't need them."))
		case len(sections) == 1:
			out = append(out, scored(2, "subheadingsTooLong", "Subheading distribution: You are not using any subheadings, although your text is rather long. Try and add some subheadings."))
		case maxSec > secBad:
			out = append(out, scored(3, "subheadingsTooLong", "Subheading distribution: A section of your text is longer than %d words (%d) and is not separated by any subheadings. Add subheadings to improve readability.", secGood, maxSec))
		case maxSec > secGood:
			out = append(out, scored(6, "subheadingsTooLong", "Subheading distribution: A section of your text is longer than %d words (%d) and is not separated by any subheadings. Add subheadings to improve readability.", secGood, maxSec))
		default:
			out = append(out, scored(9, "subheadingsTooLong", "Subheading distribution: Great job!"))
		}
	}

	if len(sens) > 0 {
		// Paragraph length
		longest := 0
		for _, p := range paragraphs(in.Content) {
			if n := len(words(p)); n > longest {
				longest = n
			}
		}
		switch {
		case longest > 200:
			out = append(out, scored(3, "textParagraphTooLong", "Paragraph length: A paragraph contains %d words, more than the recommended maximum of 150 words. Shorten your paragraphs!", longest))
		case longest > 150:
			out = append(out, scored(6, "textParagraphTooLong", "Paragraph length: A paragraph contains %d words, more than the recommended maximum of 150 words. Shorten your paragraphs!", longest))
		default:
			out = append(out, scored(9, "textParagraphTooLong", "Paragraph length: None of the paragraphs are too long. Great job!"))
		}

		// Sentence length (more than 20 words)
		long := 0
		for _, s := range sens {
			if len(words(s)) > 20 {
				long++
			}
		}
		pct := float64(long) / float64(len(sens)) * 100
		good, ok := 25.0, 30.0
		if corner {
			good, ok = 20, 25
		}
		switch {
		case pct <= good:
			out = append(out, scored(9, "textSentenceLength", "Sentence length: Great!"))
		case pct <= ok && !corner:
			out = append(out, scored(6, "textSentenceLength", "Sentence length: %.1f%% of the sentences contain more than 20 words, which is more than the recommended maximum of %.0f%%. Try to shorten the sentences.", pct, good))
		default:
			out = append(out, scored(3, "textSentenceLength", "Sentence length: %.1f%% of the sentences contain more than 20 words, which is more than the recommended maximum of %.0f%%. Try to shorten the sentences.", pct, good))
		}

		// Transition words (texts over 200 words)
		if len(tw) > 200 {
			with := 0
			for _, s := range sens {
				ls := " " + strings.Join(words(s), " ") + " "
				for _, t := range transitionWords {
					if strings.Contains(ls, " "+strings.ToLower(t)+" ") {
						with++
						break
					}
				}
			}
			tp := float64(with) / float64(len(sens)) * 100
			switch {
			case tp >= 30:
				out = append(out, scored(9, "textTransitionWords", "Transition words: Well done!"))
			case tp >= 20:
				out = append(out, scored(6, "textTransitionWords", "Transition words: Only %.1f%% of the sentences contain transition words, which is not enough. Use more of them.", tp))
			default:
				out = append(out, scored(3, "textTransitionWords", "Transition words: Only %.1f%% of the sentences contain transition words, which is not enough. Use more of them.", tp))
			}
		} else {
			out = append(out, scored(9, "textTransitionWords", "Transition words: Well done!"))
		}

		// Passive voice
		passive := 0
		for _, s := range sens {
			if passiveRe.MatchString(s) {
				passive++
			}
		}
		pp := float64(passive) / float64(len(sens)) * 100
		switch {
		case pp <= 10:
			out = append(out, scored(9, "passiveVoice", "Passive voice: You are not using too much passive voice. That's great!"))
		case pp <= 15:
			out = append(out, scored(6, "passiveVoice", "Passive voice: %.1f%% of the sentences contain passive voice, which is more than the recommended maximum of 10%%. Try to use their active counterparts.", pp))
		default:
			out = append(out, scored(3, "passiveVoice", "Passive voice: %.1f%% of the sentences contain passive voice, which is more than the recommended maximum of 10%%. Try to use their active counterparts.", pp))
		}

		// Consecutive sentences
		run, maxRun, prev := 0, 0, ""
		for _, s := range sens {
			first := ""
			if w := words(s); len(w) > 0 {
				first = w[0]
			}
			if first != "" && first == prev {
				run++
			} else {
				run = 1
			}
			prev = first
			maxRun = max(maxRun, run)
		}
		if maxRun >= 3 {
			out = append(out, scored(3, "sentenceBeginnings", "Consecutive sentences: The text contains %d consecutive sentences starting with the same word. Try to mix things up!", maxRun))
		} else {
			out = append(out, scored(9, "sentenceBeginnings", "Consecutive sentences: There is enough variety in your sentences. That's great!"))
		}
	}

	// Flesch reading ease: an insight (Yoast shows it outside the score since 19.3; Rank Math's legacy test)
	if len(sens) > 0 && len(tw) > 0 {
		syl := 0
		for _, w := range tw {
			syl += syllables(w)
		}
		fre := 206.835 - 1.015*float64(len(tw))/float64(len(sens)) - 84.6*float64(syl)/float64(len(tw))
		fre = math.Max(0, math.Min(100, math.Round(fre*10)/10))
		label := "very confusing"
		for _, b := range []struct {
			min   float64
			label string
		}{{90, "very easy"}, {80, "easy"}, {70, "fairly easy"}, {60, "standard"}, {50, "fairly difficult"}, {30, "difficult"}} {
			if fre >= b.min {
				label = b.label
				break
			}
		}
		r := scored(9, "fleschReadingEase", "Flesch reading ease: The copy scores %.1f in the test, which is considered %s to read.", fre, label)
		if fre < 50 {
			r = scored(3, "fleschReadingEase", "Flesch reading ease: The copy scores %.1f in the test, which is considered %s to read. Try to make shorter sentences, using less difficult words to improve readability.", fre, label)
		} else if fre < 60 {
			r = scored(6, "fleschReadingEase", "Flesch reading ease: The copy scores %.1f in the test, which is considered %s to read. Try to make shorter sentences to improve readability.", fre, label)
		}
		r.Group = "insight"
		out = append(out, r)
		mins := readingTime(text)
		out = append(out, seogoResult{ID: "readingTime", Rating: "good", Group: "insight", Text: fmt.Sprintf("Reading time: %d minute%s for %d words.", mins, map[bool]string{true: "", false: "s"}[mins == 1], len(tw))})
	}

	// Yoast's readability score: penalty points (bad 3, ok 2) → 30, 60 or 90
	penalty, valid := 0, 0
	for _, r := range out {
		if r.Group != "" {
			continue
		}
		valid++
		switch r.Rating {
		case "bad":
			penalty += 3
		case "ok":
			penalty += 2
		}
	}
	score := 90
	switch {
	case valid == 0:
		score = 0
	case valid == 1 || penalty > 6:
		score = 30
	case penalty > 4:
		score = 60
	}
	sortResults(out)
	rating := ratingOf(score)
	if valid == 0 {
		rating = "na"
	}
	return seogoAnalysis{Score: score, Rating: rating, Results: out}
}
