package server

import (
	"context"
	"io/fs"
	"regexp"
	"strconv"
	"sync"
	"time"

	"golang.org/x/text/language"
)

// The Pursae Analytics country map (Pursae Modern, admin_theme.go): the countries of
// web/admin/assets/maps/world-110m.svg, each shaded by its share of the period's visitors.

type worldCountry struct {
	M49, Name, D string
}

type worldMapPath struct {
	D, Name, Title string
	Level         int // 0 (no visitors) … 5 (the most)
}

type worldMap struct {
	Paths     []worldMapPath
	W, H      int
	Countries int   // countries with at least one visitor
	Known     int64 // visitors whose country is known
}

var (
	worldOnce      sync.Once
	worldCountries []worldCountry
	worldPathRe    = regexp.MustCompile(`<path data-m="(\d+)" d="([^"]+)"><title>([^<]*)</title></path>`)
)

func (s *Server) worldCountries() []worldCountry {
	worldOnce.Do(func() {
		b, err := fs.ReadFile(s.web, "admin/assets/maps/world-110m.svg")
		if err != nil {
			return
		}
		for _, m := range worldPathRe.FindAllStringSubmatch(string(b), -1) {
			worldCountries = append(worldCountries, worldCountry{M49: m[1], Name: m[3], D: m[2]})
		}
	})
	return worldCountries
}

// m49Of is the ISO 3166-1 numeric code ("792") of an alpha-2 code ("TR"), "" when unknown.
func m49Of(cc string) string {
	r, err := language.ParseRegion(cc)
	if err != nil || r.M49() == 0 || r.IsPrivateUse() {
		return ""
	}
	n := strconv.Itoa(r.M49())
	for len(n) < 3 {
		n = "0" + n
	}
	return n
}

// analyticsWorldMap shades the map with the unique visitors per country in [from, to).
func (s *Server) analyticsWorldMap(ctx context.Context, from, to time.Time) *worldMap {
	countries := s.worldCountries()
	if len(countries) == 0 {
		return nil
	}
	rows, err := s.store.Pool.Query(ctx, `SELECT country, count(DISTINCT visitor) FROM analytics_events
		WHERE name = 'pageview' AND ts >= $1 AND ts < $2 AND country <> '' GROUP BY 1`, from, to)
	if err != nil {
		return nil
	}
	defer rows.Close()
	byM49 := map[string]int64{}
	var top int64
	wm := &worldMap{W: 1000, H: 470}
	for rows.Next() {
		var cc string
		var n int64
		if rows.Scan(&cc, &n) != nil {
			continue
		}
		wm.Known += n
		if m := m49Of(cc); m != "" {
			byM49[m] += n
			if byM49[m] > top {
				top = byM49[m]
			}
		}
	}
	for _, c := range countries {
		p := worldMapPath{D: c.D, Name: c.Name, Title: c.Name}
		if n := byM49[c.M49]; n > 0 {
			wm.Countries++
			p.Title = c.Name + " · " + pmInt(n)
			// five steps on a square-root scale, so small countries still show
			switch f := float64(n) / float64(top); {
			case f > .64:
				p.Level = 5
			case f > .36:
				p.Level = 4
			case f > .16:
				p.Level = 3
			case f > .04:
				p.Level = 2
			default:
				p.Level = 1
			}
		}
		wm.Paths = append(wm.Paths, p)
	}
	return wm
}
