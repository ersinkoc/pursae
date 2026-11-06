/**
 * Turkish catalog of the host's own UI strings (./i18n.ts). English source → Turkish; every line
 * carries i18n-ignore, since the UI string lint (scripts/lint-strings.mjs) allows Turkish text only
 * in upstream's catalog. test/host/i18n-host.test.ts keeps it complete.
 */
const tr: Record<string, string> = {
  "(no title)": "(başlıksız)", // i18n-ignore
  "404 Page": "404 Sayfası", // i18n-ignore
  "404 page": "404 sayfası", // i18n-ignore
  "A category": "Bir kategori", // i18n-ignore
  "A page": "Bir sayfa", // i18n-ignore
  "A post": "Bir yazı", // i18n-ignore
  "A post type archive": "Bir yazı tipi arşivi", // i18n-ignore
  "A tag": "Bir etiket", // i18n-ignore
  "Add condition": "Koşul ekle", // i18n-ignore
  "All Archives": "Tüm arşivler", // i18n-ignore
  "All Singular": "Tüm tekil görünümler", // i18n-ignore
  "All authors": "Tüm yazarlar", // i18n-ignore
  "All {taxonomy}": "Tüm {taxonomy}", // i18n-ignore
  "All {type}": "Tüm {type}", // i18n-ignore
  "An author": "Bir yazar", // i18n-ignore
  "An entry (by ID)": "Bir içerik (ID ile)", // i18n-ignore
  "Apply": "Uygula", // i18n-ignore
  "Archive": "Arşiv", // i18n-ignore
  "Archive: {name}": "Arşiv: {name}", // i18n-ignore
  "Archives": "Arşivler", // i18n-ignore
  "At most {n} conditions.": "En fazla {n} koşul.", // i18n-ignore
  "Author Archives": "Yazar arşivleri", // i18n-ignore
  "Author: {name}": "Yazar: {name}", // i18n-ignore
  "By author": "Yazara göre", // i18n-ignore
  "Cancel": "Vazgeç", // i18n-ignore
  "Categories": "Kategoriler", // i18n-ignore
  "Category: {name}": "Kategori: {name}", // i18n-ignore
  "Child of": "Alt öğesi", // i18n-ignore
  "Choose an item, or pick “All”.": "Bir öğe seç ya da “Tümü”nü seç.", // i18n-ignore
  "Choose where this template is used on the site.": "Bu şablonun sitede nerede kullanılacağını seç.", // i18n-ignore
  "Choose where “{title}” is used on the site.": "“{title}” şablonunun sitede nerede kullanılacağını seç.", // i18n-ignore
  "Close": "Kapat", // i18n-ignore
  "Conditions": "Koşullar", // i18n-ignore
  "Date Archives": "Tarih arşivleri", // i18n-ignore
  "Display Conditions": "Görünme Koşulları", // i18n-ignore
  "Entire Site": "Tüm site", // i18n-ignore
  "Entry ID": "İçerik ID", // i18n-ignore
  "Exclude": "Hariç tut", // i18n-ignore
  "Excluded: {what}": "Hariç: {what}", // i18n-ignore
  "Footer": "Alt bilgi", // i18n-ignore
  "Front Page": "Ana sayfa", // i18n-ignore
  "Front page": "Ana sayfa", // i18n-ignore
  "Header": "Üst bilgi", // i18n-ignore
  "ID": "ID", // i18n-ignore
  "In {taxonomy}": "{taxonomy} içinde", // i18n-ignore
  "Include": "Dahil et", // i18n-ignore
  "Include or exclude": "Dahil et ya da hariç tut", // i18n-ignore
  "Include rules add views, Exclude rules take them out again. Among templates that match a view, the most specific rule wins; on a tie, the higher priority.": "Dahil et kuralları görünüm ekler, Hariç tut kuralları onları yeniden çıkarır. Bir görünüme uyan şablonlar arasında en belirgin kural kazanır; eşitlikte önceliği yüksek olan.", // i18n-ignore
  "Item": "Öğe", // i18n-ignore
  "Latest entry": "En yeni içerik", // i18n-ignore
  "Loading…": "Yükleniyor…", // i18n-ignore
  "Narrow down": "Daralt", // i18n-ignore
  "No conditions yet: the template is not shown anywhere.": "Henüz koşul yok: şablon hiçbir yerde gösterilmiyor.", // i18n-ignore
  "Nothing found.": "Hiçbir şey bulunamadı.", // i18n-ignore
  "Nothing was found to preview with this choice.": "Bu seçimle önizlenecek bir şey bulunamadı.", // i18n-ignore
  "Now: {label}": "Şu an: {label}", // i18n-ignore
  "Nowhere": "Hiçbir yerde", // i18n-ignore
  "On each view the most specific rule decides which template is used.": "Her görünümde hangi şablonun kullanılacağına en belirgin kural karar verir.", // i18n-ignore
  "Open on the site": "Sitede aç", // i18n-ignore
  "Other published templates can show on the same views:": "Yayındaki başka şablonlar da aynı görünümlerde çıkabilir:", // i18n-ignore
  "Page: {name}": "Sayfa: {name}", // i18n-ignore
  "Pages": "Sayfalar", // i18n-ignore
  "Post type": "Yazı tipi", // i18n-ignore
  "Post: {name}": "Yazı: {name}", // i18n-ignore
  "Posts": "Yazılar", // i18n-ignore
  "Posts Page": "Yazılar sayfası", // i18n-ignore
  "Posts Page: {name}": "Yazılar sayfası: {name}", // i18n-ignore
  "Posts page": "Yazılar sayfası", // i18n-ignore
  "Preview as": "Önizleme içeriği", // i18n-ignore
  "Preview as: {label}": "Önizleme içeriği: {label}", // i18n-ignore
  "Preview on the site": "Sitede önizle", // i18n-ignore
  "Priority": "Öncelik", // i18n-ignore
  "Priority {n}": "Öncelik {n}", // i18n-ignore
  "Remove condition": "Koşulu kaldır", // i18n-ignore
  "Save and close": "Kaydet ve kapat", // i18n-ignore
  "Save and publish": "Kaydet ve yayınla", // i18n-ignore
  "Saving…": "Kaydediliyor…", // i18n-ignore
  "Search Results": "Arama sonuçları", // i18n-ignore
  "Search for": "Aranacak", // i18n-ignore
  "Search results": "Arama sonuçları", // i18n-ignore
  "Search: {name}": "Arama: {name}", // i18n-ignore
  "Searching…": "Aranıyor…", // i18n-ignore
  "Search…": "Ara…", // i18n-ignore
  "Show": "Göster", // i18n-ignore
  "Single": "Tekil", // i18n-ignore
  "Single Page": "Tek sayfa", // i18n-ignore
  "Single Post": "Tek yazı", // i18n-ignore
  "Singular": "Tekil", // i18n-ignore
  "Specific author": "Belirli bir yazar", // i18n-ignore
  "Specific item": "Belirli bir öğe", // i18n-ignore
  "Specific term": "Belirli bir terim", // i18n-ignore
  "Tag: {name}": "Etiket: {name}", // i18n-ignore
  "Tags": "Etiketler", // i18n-ignore
  "Template published": "Şablon yayınlandı", // i18n-ignore
  "Template saved as draft": "Şablon taslak olarak kaydedildi", // i18n-ignore
  "Template type": "Şablon türü", // i18n-ignore
  "The content the dynamic widgets show while you edit. It does not change where the template is used.": "Dinamik bileşenlerin düzenleme sırasında gösterdiği içerik. Şablonun nerede kullanıldığını değiştirmez.", // i18n-ignore
  "The display conditions could not be saved.": "Görünme koşulları kaydedilemedi.", // i18n-ignore
  "The preview could not be changed.": "Önizleme değiştirilemedi.", // i18n-ignore
  "The preview data could not be read.": "Önizleme verisi okunamadı.", // i18n-ignore
  "The search failed.": "Arama başarısız oldu.", // i18n-ignore
  "This condition is not valid.": "Bu koşul geçerli değil.", // i18n-ignore
  "This preview is not valid.": "Bu önizleme geçerli değil.", // i18n-ignore
  "Use a whole number from {min} to {max}.": "{min} ile {max} arasında bir tam sayı kullan.", // i18n-ignore
  "Used only when two templates match a view equally well; the higher number wins.": "Yalnızca iki şablon bir görünüme eşit derecede uyduğunda kullanılır; büyük sayı kazanır.", // i18n-ignore
  "Where": "Nerede", // i18n-ignore
  "Which": "Hangisi", // i18n-ignore
  "Without an Include rule the template is not shown anywhere.": "Dahil et kuralı olmadan şablon hiçbir yerde gösterilmez.", // i18n-ignore
  "{first} +{n}": "{first} +{n}", // i18n-ignore
  "{type} Archive": "{type} arşivi", // i18n-ignore
  "{type} by {item}": "{item} yazarlı {type}", // i18n-ignore
  "{type} in {taxonomy}: {item}": "{taxonomy} içindeki {type}: {item}", // i18n-ignore
  "{type}: child of {item}": "{type}: {item} alt öğesi", // i18n-ignore
}

export default tr
