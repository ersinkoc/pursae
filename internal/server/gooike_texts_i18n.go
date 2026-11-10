package server

import "sort"

// gooikeTextPresets are ready-made banner texts per language, applied from Banner > Texts.
// English is gooikeDefaultTexts.
var gooikeTextPresets = map[string]struct {
	Label string
	Texts func() gooikeTexts
}{
	"en": {"English", gooikeDefaultTexts},
	"tr": {"Turkce", func() gooikeTexts {
		return gooikeTexts{Title: "Gizliliginize deger veriyoruz", Message: "Sitenin calismasini saglamak, kullanimi anlamak ve size uygun icerik sunmak icin cerezler kullaniyoruz. Tum cerezleri kabul edebilir, istege bagli olanlari reddedebilir veya kategorileri kendiniz secebilirsiniz. Tercihinizi istediginiz zaman degistirebilirsiniz.",
			Accept: "Tumunu kabul et", Reject: "Tumunu reddet", Customize: "Ozellestir", Save: "Tercihlerimi kaydet", PrefsTitle: "Cerez tercihleri",
			PrefsIntro: "Bu sitenin hangi cerezleri kullanabilecegini secin. Zorunlu cerezler site onlarsiz calisamayacagi icin her zaman aciktir. Seciminiz bu cihazda bir cerezde saklanir.",
			AlwaysOn:   "Her zaman aktif", PrivacyLink: "Gizlilik politikasi", CookieLink: "Cerez politikasi", NoticeLink: "Aydinlatma Metni (KVKK)",
			DoNotSell: "Kisisel Bilgilerimi Satma veya Paylasma", FloatLabel: "Cerez ayarlari", ConsentID: "Riza kimliginiz", Close: "Kapat", CookiesLabel: "Kullanilan cerezler",
			EmbedText: "Bu icerik, cerez birakabilen %s tarafindan sunulmaktadir. Gormek icin %s cerezlerini kabul edin.", EmbedButton: "Icerigi yukle",
			TableName: "Ad", TableProvider: "Saglayici", TablePurpose: "Amac", TableDuration: "Sure", TableEmpty: "Bu kategoride bildirilmis cerez yok.",
			PreferencesSC: "Cerez ayarlari", WithdrawNotice: "Tercihiniz kaydedildi."}
	}},
	"de": {"Deutsch", func() gooikeTexts {
		return gooikeTexts{Title: "Wir respektieren Ihre Privatsphaere", Message: "Wir verwenden Cookies, damit diese Website funktioniert, um die Nutzung zu verstehen und Ihnen passende Inhalte zu zeigen. Sie koennen alle Cookies akzeptieren, optionale ablehnen oder Kategorien selbst waehlen. Ihre Wahl koennen Sie jederzeit aendern.",
			Accept: "Alle akzeptieren", Reject: "Alle ablehnen", Customize: "Anpassen", Save: "Auswahl speichern", PrefsTitle: "Cookie-Einstellungen",
			PrefsIntro: "Waehlen Sie, welche Cookies diese Website verwenden darf. Notwendige Cookies sind immer aktiv, weil die Website ohne sie nicht funktioniert. Ihre Wahl wird in einem Cookie auf diesem Geraet gespeichert.",
			AlwaysOn:   "Immer aktiv", PrivacyLink: "Datenschutzerklaerung", CookieLink: "Cookie-Richtlinie", NoticeLink: "Informationshinweis",
			DoNotSell: "Meine personenbezogenen Daten nicht verkaufen oder weitergeben", FloatLabel: "Cookie-Einstellungen", ConsentID: "Ihre Einwilligungs-ID", Close: "Schliessen", CookiesLabel: "Verwendete Cookies",
			EmbedText: "Dieser Inhalt wird von %s bereitgestellt und kann Cookies setzen. Akzeptieren Sie %s-Cookies, um ihn zu sehen.", EmbedButton: "Inhalt laden",
			TableName: "Name", TableProvider: "Anbieter", TablePurpose: "Zweck", TableDuration: "Dauer", TableEmpty: "Fuer diese Kategorie sind keine Cookies angegeben.",
			PreferencesSC: "Cookie-Einstellungen", WithdrawNotice: "Ihre Auswahl wurde gespeichert."}
	}},
	"fr": {"Francais", func() gooikeTexts {
		return gooikeTexts{Title: "Nous respectons votre vie privee", Message: "Nous utilisons des cookies pour faire fonctionner ce site, comprendre son utilisation et vous proposer des contenus pertinents. Vous pouvez tout accepter, refuser les cookies optionnels ou choisir les categories. Vous pouvez modifier votre choix a tout moment.",
			Accept: "Tout accepter", Reject: "Tout refuser", Customize: "Personnaliser", Save: "Enregistrer mes choix", PrefsTitle: "Preferences de cookies",
			PrefsIntro: "Choisissez les cookies que ce site peut utiliser. Les cookies necessaires sont toujours actifs car le site ne peut pas fonctionner sans eux. Votre choix est enregistre dans un cookie sur cet appareil.",
			AlwaysOn:   "Toujours actif", PrivacyLink: "Politique de confidentialite", CookieLink: "Politique de cookies", NoticeLink: "Notice d'information",
			DoNotSell: "Ne pas vendre ni partager mes informations personnelles", FloatLabel: "Parametres des cookies", ConsentID: "Votre identifiant de consentement", Close: "Fermer", CookiesLabel: "Cookies utilises",
			EmbedText: "Ce contenu est heberge par %s, qui peut deposer des cookies. Acceptez les cookies %s pour le voir.", EmbedButton: "Charger le contenu",
			TableName: "Nom", TableProvider: "Fournisseur", TablePurpose: "Finalite", TableDuration: "Duree", TableEmpty: "Aucun cookie n'est declare pour cette categorie.",
			PreferencesSC: "Parametres des cookies", WithdrawNotice: "Votre choix a ete enregistre."}
	}},
	"es": {"Espanol", func() gooikeTexts {
		return gooikeTexts{Title: "Valoramos su privacidad", Message: "Usamos cookies para que este sitio funcione, para entender como se usa y para mostrarle contenido relevante. Puede aceptar todas, rechazar las opcionales o elegir las categorias. Puede cambiar su decision en cualquier momento.",
			Accept: "Aceptar todas", Reject: "Rechazar todas", Customize: "Personalizar", Save: "Guardar mis opciones", PrefsTitle: "Preferencias de cookies",
			PrefsIntro: "Elija que cookies puede usar este sitio. Las cookies necesarias siempre estan activas porque el sitio no puede funcionar sin ellas. Su eleccion se guarda en una cookie en este dispositivo.",
			AlwaysOn:   "Siempre activas", PrivacyLink: "Politica de privacidad", CookieLink: "Politica de cookies", NoticeLink: "Aviso informativo",
			DoNotSell: "No vender ni compartir mi informacion personal", FloatLabel: "Ajustes de cookies", ConsentID: "Su ID de consentimiento", Close: "Cerrar", CookiesLabel: "Cookies utilizadas",
			EmbedText: "Este contenido esta alojado en %s, que puede instalar cookies. Acepte las cookies de %s para verlo.", EmbedButton: "Cargar contenido",
			TableName: "Nombre", TableProvider: "Proveedor", TablePurpose: "Finalidad", TableDuration: "Duracion", TableEmpty: "No hay cookies declaradas en esta categoria.",
			PreferencesSC: "Ajustes de cookies", WithdrawNotice: "Su eleccion se ha guardado."}
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
