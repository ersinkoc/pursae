package server

import (
	"context"
	"encoding/json"
	"sort"
	"strings"
	"sync"
)

// gooikeTextPreset is a ready-made set of visitor-facing texts in one language: the banner texts
// and the label and description of the four built-in categories.
type gooikeTextPreset struct {
	Label string
	Texts func() gooikeTexts
	Cats  map[string][2]string // built-in category key -> label, description ("en": the defaults)
}

// gooikeTextPresets are the built-in language sets. They are applied by hand from Banner > Texts,
// and served automatically in the visitor's language while "Automatic language" is on (see
// gooikeLocalize). English is gooikeDefaultTexts.
var gooikeTextPresets = map[string]gooikeTextPreset{
	"en": {Label: "English", Texts: gooikeDefaultTexts},
	"tr": {Label: "Türkçe", Texts: func() gooikeTexts {
		return gooikeTexts{Title: "Gizliliğinize değer veriyoruz", Message: "Sitenin çalışmasını sağlamak, kullanımını anlamak ve size uygun içerik sunmak için çerezler kullanıyoruz. Tüm çerezleri kabul edebilir, isteğe bağlı olanları reddedebilir veya kategorileri kendiniz seçebilirsiniz. Tercihinizi istediğiniz zaman değiştirebilirsiniz.",
			Accept: "Tümünü kabul et", Reject: "Tümünü reddet", Customize: "Özelleştir", Save: "Tercihlerimi kaydet", PrefsTitle: "Çerez tercihleri",
			PrefsIntro: "Bu sitenin hangi çerezleri kullanabileceğini seçin. Zorunlu çerezler, site onlarsız çalışamayacağı için her zaman açıktır. Seçiminiz bu cihazda bir çerezde saklanır.",
			AlwaysOn:   "Her zaman etkin", PrivacyLink: "Gizlilik politikası", CookieLink: "Çerez politikası", NoticeLink: "Aydınlatma Metni (KVKK)",
			DoNotSell: "Kişisel Bilgilerimi Satma veya Paylaşma", FloatLabel: "Çerez ayarları", ConsentID: "Rıza kimliğiniz", Close: "Kapat", CookiesLabel: "Kullanılan çerezler",
			EmbedText: "Bu içerik, çerez bırakabilen %s tarafından sunulmaktadır. Görmek için %s çerezlerini kabul edin.", EmbedButton: "İçeriği yükle",
			TableName: "Ad", TableProvider: "Sağlayıcı", TablePurpose: "Amaç", TableDuration: "Süre", TableEmpty: "Bu kategoride bildirilmiş çerez yok.",
			PreferencesSC: "Çerez ayarları", WithdrawNotice: "Tercihiniz kaydedildi."}
	}, Cats: map[string][2]string{
		"necessary":   {"Zorunlu", "Bu çerezler sitenin çalışması için gereklidir: sayfalar arasında gezinme, güvenlik, oturum açma ve çerez tercihinizin hatırlanması. Kapatılamazlar."},
		"preferences": {"Tercihler", "Tercih (işlevsel) çerezleri, size daha kişisel bir deneyim sunmak için dil veya bölge gibi seçimlerinizi hatırlar."},
		"statistics":  {"İstatistik", "İstatistik (analitik) çerezleri, bilgileri anonim olarak toplayarak ziyaretçilerin siteyi nasıl kullandığını anlamamıza ve siteyi geliştirmemize yardımcı olur."},
		"marketing":   {"Pazarlama", "Pazarlama çerezleri, ilgili reklamları göstermek, kampanyaları ölçmek ve video ile harita gibi başka sitelerden gömülen içerikleri çalıştırmak için ziyaretçileri siteler arasında izler."},
	}},
	"de": {Label: "Deutsch", Texts: func() gooikeTexts {
		return gooikeTexts{Title: "Wir respektieren Ihre Privatsphäre", Message: "Wir verwenden Cookies, damit diese Website funktioniert, um die Nutzung zu verstehen und Ihnen passende Inhalte zu zeigen. Sie können alle Cookies akzeptieren, optionale ablehnen oder Kategorien selbst wählen. Ihre Wahl können Sie jederzeit ändern.",
			Accept: "Alle akzeptieren", Reject: "Alle ablehnen", Customize: "Anpassen", Save: "Auswahl speichern", PrefsTitle: "Cookie-Einstellungen",
			PrefsIntro: "Wählen Sie, welche Cookies diese Website verwenden darf. Notwendige Cookies sind immer aktiv, weil die Website ohne sie nicht funktioniert. Ihre Wahl wird in einem Cookie auf diesem Gerät gespeichert.",
			AlwaysOn:   "Immer aktiv", PrivacyLink: "Datenschutzerklärung", CookieLink: "Cookie-Richtlinie", NoticeLink: "Informationshinweis (KVKK)",
			DoNotSell: "Meine personenbezogenen Daten nicht verkaufen oder weitergeben", FloatLabel: "Cookie-Einstellungen", ConsentID: "Ihre Einwilligungs-ID", Close: "Schließen", CookiesLabel: "Verwendete Cookies",
			EmbedText: "Dieser Inhalt wird von %s bereitgestellt und kann Cookies setzen. Akzeptieren Sie %s-Cookies, um ihn zu sehen.", EmbedButton: "Inhalt laden",
			TableName: "Name", TableProvider: "Anbieter", TablePurpose: "Zweck", TableDuration: "Dauer", TableEmpty: "Für diese Kategorie sind keine Cookies angegeben.",
			PreferencesSC: "Cookie-Einstellungen", WithdrawNotice: "Ihre Auswahl wurde gespeichert."}
	}, Cats: map[string][2]string{
		"necessary":   {"Notwendig", "Diese Cookies sind für den Betrieb der Website erforderlich: Seitennavigation, Sicherheit, Anmeldung und das Speichern Ihrer Cookie-Auswahl. Sie können nicht deaktiviert werden."},
		"preferences": {"Präferenzen", "Präferenz-Cookies (funktionale Cookies) merken sich Ihre Einstellungen, etwa Sprache oder Region, um Ihnen ein persönlicheres Erlebnis zu bieten."},
		"statistics":  {"Statistik", "Statistik-Cookies (Analyse-Cookies) helfen uns zu verstehen, wie Besucher die Website nutzen, indem sie Informationen anonym erfassen, damit wir sie verbessern können."},
		"marketing":   {"Marketing", "Marketing-Cookies verfolgen Besucher über Websites hinweg, um relevante Anzeigen zu zeigen, Kampagnen zu messen und eingebettete Inhalte anderer Seiten wie Videos und Karten zu ermöglichen."},
	}},
	"fr": {Label: "Français", Texts: func() gooikeTexts {
		return gooikeTexts{Title: "Nous respectons votre vie privée", Message: "Nous utilisons des cookies pour faire fonctionner ce site, comprendre son utilisation et vous proposer des contenus pertinents. Vous pouvez tout accepter, refuser les cookies optionnels ou choisir les catégories. Vous pouvez modifier votre choix à tout moment.",
			Accept: "Tout accepter", Reject: "Tout refuser", Customize: "Personnaliser", Save: "Enregistrer mes choix", PrefsTitle: "Préférences de cookies",
			PrefsIntro: "Choisissez les cookies que ce site peut utiliser. Les cookies nécessaires sont toujours actifs car le site ne peut pas fonctionner sans eux. Votre choix est enregistré dans un cookie sur cet appareil.",
			AlwaysOn:   "Toujours actif", PrivacyLink: "Politique de confidentialité", CookieLink: "Politique de cookies", NoticeLink: "Notice d'information (KVKK)",
			DoNotSell: "Ne pas vendre ni partager mes informations personnelles", FloatLabel: "Paramètres des cookies", ConsentID: "Votre identifiant de consentement", Close: "Fermer", CookiesLabel: "Cookies utilisés",
			EmbedText: "Ce contenu est hébergé par %s, qui peut déposer des cookies. Acceptez les cookies %s pour le voir.", EmbedButton: "Charger le contenu",
			TableName: "Nom", TableProvider: "Fournisseur", TablePurpose: "Finalité", TableDuration: "Durée", TableEmpty: "Aucun cookie n'est déclaré pour cette catégorie.",
			PreferencesSC: "Paramètres des cookies", WithdrawNotice: "Votre choix a été enregistré."}
	}, Cats: map[string][2]string{
		"necessary":   {"Nécessaires", "Ces cookies sont indispensables au fonctionnement du site : navigation entre les pages, sécurité, connexion, mémorisation de votre choix de cookies. Ils ne peuvent pas être désactivés."},
		"preferences": {"Préférences", "Les cookies de préférences (fonctionnels) mémorisent vos choix, comme la langue ou la région, pour vous offrir une expérience plus personnelle."},
		"statistics":  {"Statistiques", "Les cookies statistiques (analytiques) nous aident à comprendre comment les visiteurs utilisent le site en collectant des informations de manière anonyme, afin de l'améliorer."},
		"marketing":   {"Marketing", "Les cookies marketing suivent les visiteurs d'un site à l'autre pour afficher des publicités pertinentes, mesurer les campagnes et permettre les contenus intégrés d'autres sites, comme les vidéos et les cartes."},
	}},
	"es": {Label: "Español", Texts: func() gooikeTexts {
		return gooikeTexts{Title: "Valoramos su privacidad", Message: "Usamos cookies para que este sitio funcione, para entender cómo se usa y para mostrarle contenido relevante. Puede aceptar todas, rechazar las opcionales o elegir las categorías. Puede cambiar su decisión en cualquier momento.",
			Accept: "Aceptar todas", Reject: "Rechazar todas", Customize: "Personalizar", Save: "Guardar mis opciones", PrefsTitle: "Preferencias de cookies",
			PrefsIntro: "Elija qué cookies puede usar este sitio. Las cookies necesarias siempre están activas porque el sitio no puede funcionar sin ellas. Su elección se guarda en una cookie en este dispositivo.",
			AlwaysOn:   "Siempre activas", PrivacyLink: "Política de privacidad", CookieLink: "Política de cookies", NoticeLink: "Aviso informativo (KVKK)",
			DoNotSell: "No vender ni compartir mi información personal", FloatLabel: "Ajustes de cookies", ConsentID: "Su ID de consentimiento", Close: "Cerrar", CookiesLabel: "Cookies utilizadas",
			EmbedText: "Este contenido está alojado en %s, que puede instalar cookies. Acepte las cookies de %s para verlo.", EmbedButton: "Cargar contenido",
			TableName: "Nombre", TableProvider: "Proveedor", TablePurpose: "Finalidad", TableDuration: "Duración", TableEmpty: "No hay cookies declaradas en esta categoría.",
			PreferencesSC: "Ajustes de cookies", WithdrawNotice: "Su elección se ha guardado."}
	}, Cats: map[string][2]string{
		"necessary":   {"Necesarias", "Estas cookies son imprescindibles para que el sitio funcione: navegación entre páginas, seguridad, inicio de sesión y recordar su elección de cookies. No se pueden desactivar."},
		"preferences": {"Preferencias", "Las cookies de preferencias (funcionales) recuerdan sus elecciones, como el idioma o la región, para ofrecerle una experiencia más personal."},
		"statistics":  {"Estadísticas", "Las cookies estadísticas (analíticas) nos ayudan a entender cómo usan el sitio los visitantes recopilando información de forma anónima, para que podamos mejorarlo."},
		"marketing":   {"Marketing", "Las cookies de marketing siguen a los visitantes entre sitios web para mostrar anuncios relevantes, medir campañas y permitir contenido incrustado de otros sitios, como vídeos y mapas."},
	}},
	"it": {Label: "Italiano", Texts: func() gooikeTexts {
		return gooikeTexts{Title: "Rispettiamo la tua privacy", Message: "Usiamo i cookie per far funzionare questo sito, per capire come viene usato e per mostrarti contenuti pertinenti. Puoi accettare tutti i cookie, rifiutare quelli facoltativi o scegliere le categorie. Puoi cambiare la tua scelta in qualsiasi momento.",
			Accept: "Accetta tutti", Reject: "Rifiuta tutti", Customize: "Personalizza", Save: "Salva le mie scelte", PrefsTitle: "Preferenze sui cookie",
			PrefsIntro: "Scegli quali cookie può usare questo sito. I cookie necessari sono sempre attivi perché il sito non può funzionare senza di essi. La tua scelta viene salvata in un cookie su questo dispositivo.",
			AlwaysOn:   "Sempre attivi", PrivacyLink: "Informativa sulla privacy", CookieLink: "Informativa sui cookie", NoticeLink: "Informativa (KVKK)",
			DoNotSell: "Non vendere né condividere le mie informazioni personali", FloatLabel: "Impostazioni dei cookie", ConsentID: "Il tuo ID di consenso", Close: "Chiudi", CookiesLabel: "Cookie utilizzati",
			EmbedText: "Questo contenuto è ospitato da %s, che può impostare cookie. Accetta i cookie %s per vederlo.", EmbedButton: "Carica il contenuto",
			TableName: "Nome", TableProvider: "Fornitore", TablePurpose: "Finalità", TableDuration: "Durata", TableEmpty: "Nessun cookie dichiarato per questa categoria.",
			PreferencesSC: "Impostazioni dei cookie", WithdrawNotice: "La tua scelta è stata salvata."}
	}, Cats: map[string][2]string{
		"necessary":   {"Necessari", "Questi cookie sono indispensabili per il funzionamento del sito: navigazione tra le pagine, sicurezza, accesso e memorizzazione della tua scelta sui cookie. Non possono essere disattivati."},
		"preferences": {"Preferenze", "I cookie di preferenza (funzionali) ricordano le tue scelte, come la lingua o la regione, per offrirti un'esperienza più personale."},
		"statistics":  {"Statistiche", "I cookie statistici (analitici) ci aiutano a capire come i visitatori usano il sito raccogliendo informazioni in forma anonima, così da poterlo migliorare."},
		"marketing":   {"Marketing", "I cookie di marketing seguono i visitatori tra i siti web per mostrare annunci pertinenti, misurare le campagne e consentire i contenuti incorporati da altri siti, come video e mappe."},
	}},
	"nl": {Label: "Nederlands", Texts: func() gooikeTexts {
		return gooikeTexts{Title: "Wij respecteren uw privacy", Message: "Wij gebruiken cookies om deze site te laten werken, om te begrijpen hoe de site wordt gebruikt en om u relevante inhoud te tonen. U kunt alle cookies accepteren, de optionele weigeren of zelf categorieën kiezen. U kunt uw keuze op elk moment wijzigen.",
			Accept: "Alles accepteren", Reject: "Alles weigeren", Customize: "Aanpassen", Save: "Mijn keuzes opslaan", PrefsTitle: "Cookievoorkeuren",
			PrefsIntro: "Kies welke cookies deze site mag gebruiken. Noodzakelijke cookies staan altijd aan, omdat de site zonder deze cookies niet werkt. Uw keuze wordt opgeslagen in een cookie op dit apparaat.",
			AlwaysOn:   "Altijd actief", PrivacyLink: "Privacybeleid", CookieLink: "Cookiebeleid", NoticeLink: "Informatieverklaring (KVKK)",
			DoNotSell: "Mijn persoonlijke gegevens niet verkopen of delen", FloatLabel: "Cookie-instellingen", ConsentID: "Uw toestemmings-ID", Close: "Sluiten", CookiesLabel: "Gebruikte cookies",
			EmbedText: "Deze inhoud wordt aangeboden door %s, dat cookies kan plaatsen. Accepteer %s-cookies om de inhoud te bekijken.", EmbedButton: "Inhoud laden",
			TableName: "Naam", TableProvider: "Aanbieder", TablePurpose: "Doel", TableDuration: "Duur", TableEmpty: "Voor deze categorie zijn geen cookies opgegeven.",
			PreferencesSC: "Cookie-instellingen", WithdrawNotice: "Uw keuze is opgeslagen."}
	}, Cats: map[string][2]string{
		"necessary":   {"Noodzakelijk", "Deze cookies zijn nodig om de site te laten werken: navigeren tussen pagina's, beveiliging, inloggen en het onthouden van uw cookiekeuze. Ze kunnen niet worden uitgeschakeld."},
		"preferences": {"Voorkeuren", "Voorkeurscookies (functionele cookies) onthouden uw keuzes, zoals taal of regio, om u een persoonlijkere ervaring te bieden."},
		"statistics":  {"Statistieken", "Statistische (analytische) cookies helpen ons te begrijpen hoe bezoekers de site gebruiken door anoniem informatie te verzamelen, zodat we de site kunnen verbeteren."},
		"marketing":   {"Marketing", "Marketingcookies volgen bezoekers over websites heen om relevante advertenties te tonen, campagnes te meten en ingesloten inhoud van andere sites, zoals video's en kaarten, mogelijk te maken."},
	}},
	"pt": {Label: "Português", Texts: func() gooikeTexts {
		return gooikeTexts{Title: "Valorizamos a sua privacidade", Message: "Usamos cookies para manter este site a funcionar, para compreender como é utilizado e para lhe mostrar conteúdo relevante. Pode aceitar todos os cookies, rejeitar os opcionais ou escolher as categorias. Pode alterar a sua escolha a qualquer momento.",
			Accept: "Aceitar todos", Reject: "Rejeitar todos", Customize: "Personalizar", Save: "Guardar as minhas escolhas", PrefsTitle: "Preferências de cookies",
			PrefsIntro: "Escolha que cookies este site pode usar. Os cookies necessários estão sempre ativos porque o site não funciona sem eles. A sua escolha fica guardada num cookie neste dispositivo.",
			AlwaysOn:   "Sempre ativos", PrivacyLink: "Política de privacidade", CookieLink: "Política de cookies", NoticeLink: "Aviso informativo (KVKK)",
			DoNotSell: "Não vender nem partilhar as minhas informações pessoais", FloatLabel: "Definições de cookies", ConsentID: "O seu ID de consentimento", Close: "Fechar", CookiesLabel: "Cookies utilizados",
			EmbedText: "Este conteúdo é alojado por %s, que pode definir cookies. Aceite os cookies de %s para o ver.", EmbedButton: "Carregar conteúdo",
			TableName: "Nome", TableProvider: "Fornecedor", TablePurpose: "Finalidade", TableDuration: "Duração", TableEmpty: "Não há cookies declarados nesta categoria.",
			PreferencesSC: "Definições de cookies", WithdrawNotice: "A sua escolha foi guardada."}
	}, Cats: map[string][2]string{
		"necessary":   {"Necessários", "Estes cookies são indispensáveis para o site funcionar: navegação entre páginas, segurança, início de sessão e memorização da sua escolha de cookies. Não podem ser desativados."},
		"preferences": {"Preferências", "Os cookies de preferências (funcionais) memorizam as suas escolhas, como o idioma ou a região, para lhe oferecer uma experiência mais pessoal."},
		"statistics":  {"Estatísticas", "Os cookies estatísticos (analíticos) ajudam-nos a compreender como os visitantes usam o site, recolhendo informação de forma anónima, para o podermos melhorar."},
		"marketing":   {"Marketing", "Os cookies de marketing acompanham os visitantes entre sites para mostrar anúncios relevantes, medir campanhas e permitir conteúdo incorporado de outros sites, como vídeos e mapas."},
	}},
}

// gooikeTextPresetKeys lists the preset languages in a stable order.
func gooikeTextPresetKeys() []string {
	k := make([]string, 0, len(gooikeTextPresets))
	for key := range gooikeTextPresets {
		k = append(k, key)
	}
	sort.Strings(k)
	return k
}

// gooikePresetIndex is the presets as field maps, plus every built-in value per field: a stored
// text that is still one of the built-in values was not written by the admin and may be swapped
// for the same text in another language.
type gooikePresetIndex struct {
	texts     map[string]map[string]string // language -> text field key -> value
	cats      map[string]map[string][2]string
	textKnown map[string]map[string]bool // field key -> built-in values
	catKnown  map[string][2]map[string]bool
}

var gooikePresetIdx = sync.OnceValue(func() *gooikePresetIndex {
	ix := &gooikePresetIndex{texts: map[string]map[string]string{}, cats: map[string]map[string][2]string{},
		textKnown: map[string]map[string]bool{}, catKnown: map[string][2]map[string]bool{}}
	for lang, p := range gooikeTextPresets {
		raw, _ := json.Marshal(p.Texts())
		var m map[string]string
		_ = json.Unmarshal(raw, &m)
		ix.texts[lang] = m
		for k, v := range m {
			if ix.textKnown[k] == nil {
				ix.textKnown[k] = map[string]bool{}
			}
			ix.textKnown[k][v] = true
		}
		cats := p.Cats
		if lang == "en" {
			cats = map[string][2]string{}
			for _, c := range gooikeDefaultCategories() {
				cats[c.Key] = [2]string{c.Label, c.Description}
			}
		}
		ix.cats[lang] = cats
		for k, v := range cats {
			kn, ok := ix.catKnown[k]
			if !ok {
				kn = [2]map[string]bool{{}, {}}
				ix.catKnown[k] = kn
			}
			kn[0][v[0]], kn[1][v[1]] = true, true
		}
	}
	return ix
})

// gooikeLang is the language of a front-end request as a preset key: the visitor's language under
// Pursae Multi Language (the language of the URL being viewed), else the site language. "" when
// there is no built-in set for it.
func (s *Server) gooikeLang(ctx context.Context) string {
	code := s.settings.String("WPLANG")
	if l := s.pllCurrent(ctx); l != nil {
		code = l.Slug
		if l.Locale != "" {
			code = l.Locale
		}
	}
	if code == "" {
		code = "en"
	}
	code = strings.ToLower(code)
	if i := strings.IndexAny(code, "_-"); i > 0 {
		code = code[:i]
	}
	if _, ok := gooikeTextPresets[code]; !ok {
		return ""
	}
	return code
}

// gooikeLocalize serves the built-in texts in the visitor's language. Only texts that are still a
// built-in value (of any language set) are swapped; a text the admin wrote stays as written and is
// translated with Pursae Multi Language's string translations as before. The same goes for the
// label and description of the built-in categories. It changes st in place.
func (s *Server) gooikeLocalize(ctx context.Context, st *gooikeSettings) {
	if !st.TextsAuto {
		return
	}
	lang := s.gooikeLang(ctx)
	if lang == "" {
		return
	}
	ix := gooikePresetIdx()
	raw, _ := json.Marshal(st.Texts)
	var cur map[string]string
	if json.Unmarshal(raw, &cur) != nil {
		return
	}
	changed := false
	for k, v := range cur {
		if want := ix.texts[lang][k]; want != "" && want != v && ix.textKnown[k][v] {
			cur[k], changed = want, true
		}
	}
	if changed {
		raw, _ = json.Marshal(cur)
		_ = json.Unmarshal(raw, &st.Texts)
	}
	cats := append([]gooikeCategory(nil), st.Categories...) // the settings share the slice
	for i := range cats {
		want, ok := ix.cats[lang][cats[i].Key]
		kn, known := ix.catKnown[cats[i].Key]
		if !ok || !known {
			continue
		}
		if kn[0][cats[i].Label] {
			cats[i].Label = want[0]
		}
		if kn[1][cats[i].Description] {
			cats[i].Description = want[1]
		}
	}
	st.Categories = cats
}
