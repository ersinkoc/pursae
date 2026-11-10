package server

import (
	"context"
	"fmt"
	"html"
	"strings"

	"wpgo/internal/store"
)

// gooikePolicyDoc is a generated legal page: a title, a slug and block content.
type gooikePolicyDoc struct {
	Title, Slug, Content string
}

func gooikePara(s string) string {
	return "<!-- wp:paragraph -->\n<p>" + s + "</p>\n<!-- /wp:paragraph -->\n\n"
}
func gooikeHead(s string) string {
	return "<!-- wp:heading -->\n<h2>" + html.EscapeString(s) + "</h2>\n<!-- /wp:heading -->\n\n"
}
func gooikeList(items ...string) string {
	var b strings.Builder
	b.WriteString("<!-- wp:list -->\n<ul>")
	for _, i := range items {
		b.WriteString("<li>" + i + "</li>")
	}
	b.WriteString("</ul>\n<!-- /wp:list -->\n\n")
	return b.String()
}

// gooikeCookiePolicyDoc builds the cookie policy for a regulation: a template text plus the live
// [gooike_cookie_table] and [gooike_preferences] shortcodes. The owner reviews it before publishing.
func gooikeCookiePolicyDoc(reg, siteName string) gooikePolicyDoc {
	site := html.EscapeString(siteName)
	if site == "" {
		site = "this website"
	}
	var b strings.Builder
	d := gooikePolicyDoc{Title: "Cookie Policy", Slug: "cookie-policy"}
	switch reg {
	case "kvkk":
		d = gooikePolicyDoc{Title: "Cerez Politikasi", Slug: "cerez-politikasi"}
		b.WriteString(gooikePara(site + " internet sitesi, 6698 sayili Kisisel Verilerin Korunmasi Kanunu (KVKK) kapsaminda cerezler kullanir. Bu politika, hangi cerezlerin neden kullanildigini ve tercihlerinizi nasil yonetebileceginizi aciklar."))
		b.WriteString(gooikeHead("Cerez nedir?"))
		b.WriteString(gooikePara("Cerezler, bir internet sitesini ziyaret ettiginizde cihazinizda saklanan kucuk metin dosyalaridir. Sitenin calismasini saglar, tercihlerinizi hatirlar ve ziyaretlerin nasil kullanildigini anlamamiza yardimci olur."))
		b.WriteString(gooikeHead("Kullandigimiz cerez turleri"))
		b.WriteString(gooikeList("<strong>Zorunlu cerezler:</strong> Sitenin calismasi icin gereklidir ve acik riza gerektirmez.", "<strong>Tercih cerezleri:</strong> Dil gibi secimlerinizi hatirlar.", "<strong>Istatistik cerezleri:</strong> Ziyaretlerin anonim olarak olculmesini saglar.", "<strong>Pazarlama cerezleri:</strong> Ilginizi ceken icerik ve reklamlari gostermek icin kullanilir."))
		b.WriteString(gooikeHead("Kullanilan cerezler"))
		b.WriteString("[gooike_cookie_table]\n\n")
		b.WriteString(gooikeHead("Tercihlerinizi yonetin"))
		b.WriteString(gooikePara("Zorunlu olmayan cerezler yalnizca acik riza verdiginizde calisir. Rizanizi diledginiz zaman geri cekebilirsiniz: [gooike_preferences text=\"Cerez tercihlerini degistir\"]"))
		b.WriteString(gooikePara("Kisisel verilerinizin islenmesine iliskin ayrintili bilgi icin Aydinlatma Metni'ni inceleyiniz."))
	case "ccpa":
		b.WriteString(gooikePara(site + " uses cookies and similar technologies. This policy explains what they are, why we use them and how you can opt out of the sale or sharing of your personal information under the CCPA/CPRA."))
		b.WriteString(gooikeHead("What are cookies?"))
		b.WriteString(gooikePara("Cookies are small text files stored on your device when you visit a website. They keep the site working, remember your choices and help us understand how the site is used."))
		b.WriteString(gooikeHead("Cookies we use"))
		b.WriteString("[gooike_cookie_table]\n\n")
		b.WriteString(gooikeHead("Your choices"))
		b.WriteString(gooikePara("You can opt out of the sale or sharing of your personal information at any time: [gooike_preferences text=\"Do Not Sell or Share My Personal Information\"]. We also honor the Global Privacy Control signal sent by your browser."))
	default:
		b.WriteString(gooikePara(site + " uses cookies and similar technologies. This policy explains what they are, why we use them and how you can control them, in line with the GDPR and the ePrivacy rules."))
		b.WriteString(gooikeHead("What are cookies?"))
		b.WriteString(gooikePara("Cookies are small text files stored on your device when you visit a website. They keep the site working, remember your choices and help us understand how the site is used."))
		b.WriteString(gooikeHead("Types of cookies we use"))
		b.WriteString(gooikeList("<strong>Necessary:</strong> required for the site to work; they do not need consent.", "<strong>Preferences:</strong> remember choices such as your language.", "<strong>Statistics:</strong> measure how visitors use the site.", "<strong>Marketing:</strong> show relevant content and advertising."))
		b.WriteString(gooikeHead("Cookies in detail"))
		b.WriteString("[gooike_cookie_table]\n\n")
		b.WriteString(gooikeHead("Manage your consent"))
		b.WriteString(gooikePara("Optional cookies are only set after you agree. You can change or withdraw your consent at any time: [gooike_preferences text=\"Change cookie settings\"]"))
	}
	d.Content = b.String()
	return d
}

// gooikeNoticeDoc is the Turkish KVKK information notice (Aydinlatma Metni) template.
func gooikeNoticeDoc(siteName string) gooikePolicyDoc {
	site := html.EscapeString(siteName)
	if site == "" {
		site = "[Veri sorumlusu unvani]"
	}
	var b strings.Builder
	b.WriteString(gooikePara("Bu Aydinlatma Metni, 6698 sayili Kisisel Verilerin Korunmasi Kanunu'nun 10. maddesi uyarinca veri sorumlusu sifatiyla <strong>" + site + "</strong> tarafindan hazirlanmistir. Asagidaki bos alanlari yayinlamadan once doldurunuz."))
	b.WriteString(gooikeHead("1. Veri sorumlusu"))
	b.WriteString(gooikePara("Unvan: " + site + " &mdash; Adres: [adres] &mdash; E-posta: [e-posta]"))
	b.WriteString(gooikeHead("2. Islenen kisisel veriler ve amaclar"))
	b.WriteString(gooikePara("Internet sitemizi ziyaret ettiginizde IP adresiniz, cihaz ve tarayici bilgileriniz ile cerezler araciligiyla elde edilen kullanim verileriniz islenir. Bu veriler sitenin calismasini saglamak, guvenligi korumak, istatistik cikarmak ve acik rizaniz varsa pazarlama faaliyetleri yurutmek amaclariyla islenir."))
	b.WriteString(gooikeHead("3. Hukuki sebepler"))
	b.WriteString(gooikePara("Veriler, KVKK madde 5/2 kapsaminda sozlesmenin kurulmasi veya ifasi, hukuki yukumlulugun yerine getirilmesi ve mesru menfaat sebeplerine; zorunlu olmayan cerezler bakimindan acik rizaniza dayanilarak islenir."))
	b.WriteString(gooikeHead("4. Verilerin aktarilmasi"))
	b.WriteString(gooikePara("Verileriniz, yukaridaki amaclarla sinirli olmak uzere hizmet saglayicilarimiza ve kanunen yetkili kamu kurumlarina aktarilabilir. Yurt disina aktarim yapiliyorsa KVKK madde 9'a uygun olarak gerceklestirilir."))
	b.WriteString(gooikeHead("5. Cerezler"))
	b.WriteString(gooikePara("Cerezlerin turleri ve tercihlerinizi yonetme yollari Cerez Politikasi'nda aciklanmistir. Tercihlerinizi buradan degistirebilirsiniz: [gooike_preferences text=\"Cerez tercihlerini degistir\"]"))
	b.WriteString(gooikeHead("6. Haklariniz"))
	b.WriteString(gooikePara("KVKK madde 11 uyarinca asagidaki haklara sahipsiniz:"))
	b.WriteString(gooikeList("Kisisel verilerinizin islenip islenmedigini ogrenme", "Islenmisse buna iliskin bilgi talep etme", "Islenme amacini ve amacina uygun kullanilip kullanilmadigini ogrenme", "Yurt ici veya yurt disinda aktarildigi ucuncu kisileri bilme", "Eksik veya yanlis islenmisse duzeltilmesini isteme", "KVKK madde 7 kapsaminda silinmesini veya yok edilmesini isteme", "Duzeltme, silme ve yok etme islemlerinin aktarildigi ucuncu kisilere bildirilmesini isteme", "Islenen verilerin analiz edilmesi sonucu aleyhinize bir sonuc ortaya cikmasina itiraz etme", "Kanuna aykiri isleme nedeniyle zarara ugramaniz halinde zararin giderilmesini talep etme"))
	b.WriteString(gooikePara("Basvurularinizi yukaridaki e-posta adresine veya yazili olarak iletebilirsiniz."))
	return gooikePolicyDoc{Title: "Aydinlatma Metni", Slug: "aydinlatma-metni", Content: b.String()}
}

// gooikeCreatePolicyPages inserts the documents for a regulation as draft pages (the owner reviews
// the text before publishing) and returns their titles and edit links.
func (s *Server) gooikeCreatePolicyPages(ctx context.Context, reg string, authorID int64) ([]string, error) {
	site := s.settings.String("blogname")
	docs := []gooikePolicyDoc{gooikeCookiePolicyDoc(reg, site)}
	if reg == "kvkk" {
		docs = append(docs, gooikeNoticeDoc(site))
	}
	var made []string
	for _, d := range docs {
		slug, err := s.store.UniqueSlug(ctx, "page", 0, d.Slug, 0)
		if err != nil {
			return made, err
		}
		p := &store.Post{Type: "page", Title: d.Title, Slug: slug, Status: "draft", AuthorID: authorID, Content: d.Content}
		if err := s.store.InsertPost(ctx, p); err != nil {
			return made, err
		}
		made = append(made, fmt.Sprintf("%s (#%d)", d.Title, p.ID))
	}
	return made, nil
}
