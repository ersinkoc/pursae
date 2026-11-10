package wp

import (
	"regexp"
	"strconv"
	"strings"
	"time"
)

// ParseDate reads text written by FormatDate with the same format and English names (the date
// and time tokens d j D l S F M m n Y y and g G h H i s a A; other tokens make it fail). Fields the
// format does not carry stay zero (year 1 and so on), so only reformat with formats that use the
// same fields.
func ParseDate(text, format string) (time.Time, bool) {
	var re strings.Builder
	var kinds []byte
	re.WriteString(`^`)
	group := func(expr string, kind byte) {
		re.WriteString("(" + expr + ")")
		kinds = append(kinds, kind)
	}
	en := EnglishDateNames()
	alt := func(list []string) string {
		q := make([]string, len(list))
		for i, v := range list {
			q[i] = regexp.QuoteMeta(v)
		}
		return strings.Join(q, "|")
	}
	for i := 0; i < len(format); i++ {
		c := format[i]
		switch c {
		case '\\':
			if i+1 < len(format) {
				i++
				re.WriteString(regexp.QuoteMeta(string(format[i])))
			}
		case 'd':
			group(`\d{2}`, 'd')
		case 'j':
			group(`\d{1,2}`, 'd')
		case 'm':
			group(`\d{2}`, 'n')
		case 'n':
			group(`\d{1,2}`, 'n')
		case 'Y':
			group(`\d{4}`, 'Y')
		case 'y':
			group(`\d{2}`, 'y')
		case 'F':
			group(alt(en.Months[:]), 'F')
		case 'M':
			group(alt(en.MonthsShort[:]), 'M')
		case 'D':
			group(alt(en.WeekdaysShort[:]), '-')
		case 'l':
			group(alt(en.Weekdays[:]), '-')
		case 'S':
			group(`st|nd|rd|th`, '-')
		case 'g':
			group(`\d{1,2}`, 'h')
		case 'h':
			group(`\d{2}`, 'h')
		case 'G':
			group(`\d{1,2}`, 'H')
		case 'H':
			group(`\d{2}`, 'H')
		case 'i':
			group(`\d{2}`, 'i')
		case 's':
			group(`\d{2}`, 's')
		case 'a':
			group(`am|pm`, 'a')
		case 'A':
			group(`AM|PM`, 'a')
		default:
			if strings.ContainsRune("NwzWtLoeIOPpTZcrUuv", rune(c)) {
				return time.Time{}, false
			}
			re.WriteString(regexp.QuoteMeta(string(c)))
		}
	}
	re.WriteString(`$`)
	m := regexp.MustCompile(re.String()).FindStringSubmatch(strings.TrimSpace(text))
	if m == nil {
		return time.Time{}, false
	}
	year, month, day, hour, min, sec := 1, 1, 1, 0, 0, 0
	pm, has12 := false, false
	for i, k := range kinds {
		v := m[i+1]
		n, _ := strconv.Atoi(v)
		switch k {
		case 'd':
			day = n
		case 'n':
			month = n
		case 'Y':
			year = n
		case 'y':
			year = 2000 + n
		case 'F':
			month = indexOf(en.Months[:], v) + 1
		case 'M':
			month = indexOf(en.MonthsShort[:], v) + 1
		case 'h':
			hour, has12 = n%12, true
		case 'H':
			hour = n
		case 'i':
			min = n
		case 's':
			sec = n
		case 'a':
			pm = strings.EqualFold(v, "pm")
		}
	}
	if has12 && pm {
		hour += 12
	}
	if month < 1 || month > 12 || day < 1 || day > 31 || hour > 23 || min > 59 || sec > 59 {
		return time.Time{}, false
	}
	return time.Date(year, time.Month(month), day, hour, min, sec, 0, time.UTC), true
}

func indexOf(list []string, v string) int {
	for i, x := range list {
		if x == v {
			return i
		}
	}
	return -1
}
