package i18n

// jsonPluralExpr is the gettext Plural-Forms expression for locale, used when a JSON catalog
// (web/languages/wpgo-<locale>.json) is loaded without a core .mo that already carries the rule.
// The empty string means the binary default (n == 1 is singular, everything else is plural), which
// is the rule for German, Spanish, Turkish and the other two-form languages not listed here.
// Expressions match the forms shipped in those files (WordPress's plural formulas).
func jsonPluralExpr(locale string) string {
	switch locale {
	case "bo", "dzo", "id_ID", "ja", "jv_ID", "km", "ko_KR", "lo", "ms_MY", "my_MM",
		"sah", "th", "tt_RU", "vi", "yor", "zh_CN", "zh_HK", "zh_TW":
		return "0" // one form for every number
	case "fr_FR", "fr_BE", "fr_CA", "pt_BR", "fa_IR", "fa_AF":
		return "n > 1" // 0 and 1 are singular
	case "pl_PL", "szl":
		return "n==1 ? 0 : n%10>=2 && n%10<=4 && (n%100<10 || n%100>=20) ? 1 : 2"
	case "ru_RU", "uk", "bel", "bs_BA", "hr", "sr_RS":
		return "n%10==1 && n%100!=11 ? 0 : n%10>=2 && n%10<=4 && (n%100<10 || n%100>=20) ? 1 : 2"
	case "cs_CZ", "sk_SK":
		return "n==1 ? 0 : n>=2 && n<=4 ? 1 : 2"
	case "lt_LT":
		return "n%10==1 && n%100!=11 ? 0 : n%10>=2 && (n%100<10 || n%100>=20) ? 1 : 2"
	case "ro_RO":
		return "n==1 ? 0 : n==0 || (n%100>0 && n%100<20) ? 1 : 2"
	case "lv":
		// Singular is the second form in the Latvian packs ("1 gads", "2 gadi", "0 gadu").
		return "(n%10==0 || (n%100>=11 && n%100<=19)) ? 0 : (n%10==1 && n%100!=11) ? 1 : 2"
	case "sl_SI", "dsb", "hsb":
		return "n%100==1 ? 0 : n%100==2 ? 1 : n%100==3 || n%100==4 ? 2 : 3"
	case "cy":
		return "n==1 ? 0 : n==2 ? 1 : n!=8 && n!=11 ? 2 : 3"
	case "gd":
		return "n==1 || n==11 ? 0 : n==2 || n==12 ? 1 : n>2 && n<20 ? 2 : 3"
	case "ar", "ary":
		return "n==0 ? 0 : n==1 ? 1 : n==2 ? 2 : n%100>=3 && n%100<=10 ? 3 : n%100>=11 ? 4 : 5"
	default:
		return ""
	}
}
