// Package wp holds Go counterparts of WordPress formatting helpers that templates
// and handlers share: PHP date formats, sanitize_title, wpautop, excerpts.
package wp

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// DateNames are the month, weekday and meridiem names date_i18n() uses for a language; nil fields
// fall back to English.
type DateNames struct {
	Months, MonthsShort     [12]string // January.., Jan..
	Weekdays, WeekdaysShort [7]string  // Sunday.., Sun..
	AM, PM                  string
}

// EnglishDateNames are the names FormatDate produces.
func EnglishDateNames() *DateNames {
	d := &DateNames{AM: "am", PM: "pm"}
	for i := 0; i < 12; i++ {
		m := time.Month(i + 1).String()
		d.Months[i], d.MonthsShort[i] = m, m[:3]
	}
	for i := 0; i < 7; i++ {
		w := time.Weekday(i).String()
		d.Weekdays[i], d.WeekdaysShort[i] = w, w[:3]
	}
	return d
}

// FormatDate formats t with a PHP date() format string, as date_i18n() does for English.
func FormatDate(t time.Time, format string) string { return FormatDateNames(t, format, nil) }

// FormatDateNames is FormatDate with the month, weekday and meridiem names of a language (nil:
// English); empty names fall back to English too.
func FormatDateNames(t time.Time, format string, names *DateNames) string {
	en := EnglishDateNames()
	pick := func(v, def string) string {
		if v != "" {
			return v
		}
		return def
	}
	var sb strings.Builder
	for i := 0; i < len(format); i++ {
		c := format[i]
		switch c {
		case '\\':
			if i+1 < len(format) {
				i++
				sb.WriteByte(format[i])
			}
		case 'd':
			fmt.Fprintf(&sb, "%02d", t.Day())
		case 'D':
			sb.WriteString(pickName(names, func(n *DateNames) string { return n.WeekdaysShort[t.Weekday()] }, en.WeekdaysShort[t.Weekday()], pick))
		case 'j':
			sb.WriteString(strconv.Itoa(t.Day()))
		case 'l':
			sb.WriteString(pickName(names, func(n *DateNames) string { return n.Weekdays[t.Weekday()] }, en.Weekdays[t.Weekday()], pick))
		case 'N':
			wd := int(t.Weekday())
			if wd == 0 {
				wd = 7
			}
			sb.WriteString(strconv.Itoa(wd))
		case 'S':
			sb.WriteString(ordinalSuffix(t.Day()))
		case 'w':
			sb.WriteString(strconv.Itoa(int(t.Weekday())))
		case 'z':
			sb.WriteString(strconv.Itoa(t.YearDay() - 1))
		case 'W':
			_, w := t.ISOWeek()
			fmt.Fprintf(&sb, "%02d", w)
		case 'F':
			sb.WriteString(pickName(names, func(n *DateNames) string { return n.Months[t.Month()-1] }, en.Months[t.Month()-1], pick))
		case 'm':
			fmt.Fprintf(&sb, "%02d", int(t.Month()))
		case 'M':
			sb.WriteString(pickName(names, func(n *DateNames) string { return n.MonthsShort[t.Month()-1] }, en.MonthsShort[t.Month()-1], pick))
		case 'n':
			sb.WriteString(strconv.Itoa(int(t.Month())))
		case 't':
			sb.WriteString(strconv.Itoa(time.Date(t.Year(), t.Month()+1, 0, 0, 0, 0, 0, t.Location()).Day()))
		case 'L':
			y := t.Year()
			if (y%4 == 0 && y%100 != 0) || y%400 == 0 {
				sb.WriteByte('1')
			} else {
				sb.WriteByte('0')
			}
		case 'o':
			y, _ := t.ISOWeek()
			sb.WriteString(strconv.Itoa(y))
		case 'Y':
			fmt.Fprintf(&sb, "%04d", t.Year())
		case 'y':
			fmt.Fprintf(&sb, "%02d", t.Year()%100)
		case 'a':
			if t.Hour() < 12 {
				sb.WriteString(pickName(names, func(n *DateNames) string { return n.AM }, "am", pick))
			} else {
				sb.WriteString(pickName(names, func(n *DateNames) string { return n.PM }, "pm", pick))
			}
		case 'A':
			if t.Hour() < 12 {
				sb.WriteString(strings.ToUpper(pickName(names, func(n *DateNames) string { return n.AM }, "am", pick)))
			} else {
				sb.WriteString(strings.ToUpper(pickName(names, func(n *DateNames) string { return n.PM }, "pm", pick)))
			}
		case 'g':
			sb.WriteString(strconv.Itoa(hour12(t)))
		case 'G':
			sb.WriteString(strconv.Itoa(t.Hour()))
		case 'h':
			fmt.Fprintf(&sb, "%02d", hour12(t))
		case 'H':
			fmt.Fprintf(&sb, "%02d", t.Hour())
		case 'i':
			fmt.Fprintf(&sb, "%02d", t.Minute())
		case 's':
			fmt.Fprintf(&sb, "%02d", t.Second())
		case 'u':
			fmt.Fprintf(&sb, "%06d", t.Nanosecond()/1000)
		case 'v':
			fmt.Fprintf(&sb, "%03d", t.Nanosecond()/1_000_000)
		case 'e':
			sb.WriteString(t.Location().String())
		case 'I':
			if t.IsDST() {
				sb.WriteByte('1')
			} else {
				sb.WriteByte('0')
			}
		case 'O', 'P', 'p':
			_, off := t.Zone()
			if c == 'p' && off == 0 {
				sb.WriteByte('Z')
				break
			}
			sign := '+'
			if off < 0 {
				sign, off = '-', -off
			}
			if c == 'O' {
				fmt.Fprintf(&sb, "%c%02d%02d", sign, off/3600, off%3600/60)
			} else {
				fmt.Fprintf(&sb, "%c%02d:%02d", sign, off/3600, off%3600/60)
			}
		case 'T':
			name, _ := t.Zone()
			sb.WriteString(name)
		case 'Z':
			_, off := t.Zone()
			sb.WriteString(strconv.Itoa(off))
		case 'c':
			sb.WriteString(t.Format("2006-01-02T15:04:05-07:00"))
		case 'r':
			sb.WriteString(t.Format("Mon, 02 Jan 2006 15:04:05 -0700"))
		case 'U':
			sb.WriteString(strconv.FormatInt(t.Unix(), 10))
		default:
			sb.WriteByte(c)
		}
	}
	return sb.String()
}

func pickName(n *DateNames, get func(*DateNames) string, def string, pick func(v, def string) string) string {
	if n == nil {
		return def
	}
	return pick(get(n), def)
}

func hour12(t time.Time) int {
	h := t.Hour() % 12
	if h == 0 {
		return 12
	}
	return h
}

func ordinalSuffix(d int) string {
	if d%100 >= 11 && d%100 <= 13 {
		return "th"
	}
	switch d % 10 {
	case 1:
		return "st"
	case 2:
		return "nd"
	case 3:
		return "rd"
	}
	return "th"
}

// Location returns the site time zone from the timezone_string or gmt_offset settings.
func Location(tzString string, gmtOffset float64) *time.Location {
	if tzString != "" {
		if loc, err := time.LoadLocation(tzString); err == nil {
			return loc
		}
	}
	if gmtOffset == 0 {
		return time.UTC
	}
	secs := int(gmtOffset * 3600)
	sign := "+"
	if secs < 0 {
		sign = "-"
	}
	h := abs(secs) / 3600
	m := abs(secs) % 3600 / 60
	return time.FixedZone(fmt.Sprintf("UTC%s%d:%02d", sign, h, m), secs)
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

// HumanTimeDiff is human_time_diff(): "5 mins", "2 hours", "3 days".
func HumanTimeDiff(from, to time.Time) string {
	d := to.Sub(from)
	if d < 0 {
		d = -d
	}
	plural := func(n int, one, many string) string {
		if n <= 1 {
			return "1 " + one
		}
		return strconv.Itoa(n) + " " + many
	}
	switch {
	case d < time.Minute:
		return plural(int(d.Seconds()), "second", "seconds")
	case d < time.Hour:
		return plural(int(d.Round(time.Minute).Minutes()), "min", "mins")
	case d < 24*time.Hour:
		return plural(int(d.Round(time.Hour).Hours()), "hour", "hours")
	case d < 7*24*time.Hour:
		return plural(int(d.Round(24*time.Hour).Hours()/24), "day", "days")
	case d < 30*24*time.Hour:
		return plural(int(d.Round(7*24*time.Hour).Hours()/(24*7)), "week", "weeks")
	case d < 365*24*time.Hour:
		return plural(int(d.Round(30*24*time.Hour).Hours()/(24*30)), "month", "months")
	}
	return plural(int(d.Round(365*24*time.Hour).Hours()/(24*365)), "year", "years") // round(), like the other units
}
