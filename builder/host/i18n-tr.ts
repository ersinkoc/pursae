/**
 * Turkish catalog of the host's own UI strings (./i18n.ts). English source → Turkish; every line
 * carries i18n-ignore, since the UI string lint (scripts/lint-strings.mjs) allows Turkish text only
 * in upstream's catalog. test/host/i18n-host.test.ts keeps it complete.
 */
const tr: Record<string, string> = {
  "Site settings": "Site ayarları", // i18n-ignore
  "Site identity": "Site kimliği", // i18n-ignore
  "Site title": "Site başlığı", // i18n-ignore
  "Tagline": "Slogan", // i18n-ignore
  "In a few words, explain what this site is about.": "Bu sitenin ne hakkında olduğunu birkaç sözcükle anlatın.", // i18n-ignore
  "Logo": "Logo", // i18n-ignore
  "Shown by the header of your theme.": "Temanızın üst bölümünde gösterilir.", // i18n-ignore
  "Site icon": "Site simgesi", // i18n-ignore
  "The icon in browser tabs and bookmarks. It should be square, at least 512 × 512 pixels.": "Tarayıcı sekmelerinde ve yer imlerinde görünen simge. Kare olmalı ve en az 512 × 512 piksel olmalıdır.", // i18n-ignore
  "Change": "Değiştir", // i18n-ignore
  "Select": "Seç", // i18n-ignore
  "Remove": "Kaldır", // i18n-ignore
  "Homepage": "Ana sayfa", // i18n-ignore
  "Your homepage displays": "Ana sayfanız şunu gösterir", // i18n-ignore
  "Your latest posts": "Son yazılarınız", // i18n-ignore
  "A static page": "Sabit bir sayfa", // i18n-ignore
  "— Select —": "— Seçin —", // i18n-ignore
  "Additional CSS": "Ek CSS", // i18n-ignore
  "CSS added here applies to your whole site.": "Buraya eklenen CSS tüm sitenize uygulanır.", // i18n-ignore
  "Loading settings…": "Ayarlar yükleniyor…", // i18n-ignore
  "Saved. The changes are live.": "Kaydedildi. Değişiklikler yayında.", // i18n-ignore
  "Save changes": "Değişiklikleri kaydet", // i18n-ignore
  "The site settings could not be loaded.": "Site ayarları yüklenemedi.", // i18n-ignore
  "The site settings could not be saved.": "Site ayarları kaydedilemedi.", // i18n-ignore
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
  "Back to the library": "Kütüphaneye dön", // i18n-ignore
  "Back": "Geri", // i18n-ignore
  "Browse theme library": "Tema kütüphanesine göz at", // i18n-ignore
  "Creating the templates…": "Şablonlar oluşturuluyor…", // i18n-ignore
  "Demo pages: {list}": "Demo sayfaları: {list}", // i18n-ignore
  "Disabled (you can enable them again): {list}": "Devre dışı bırakıldı (yeniden etkinleştirebilirsiniz): {list}", // i18n-ignore
  "Finishing…": "Tamamlanıyor…", // i18n-ignore
  "Import as drafts": "Taslak olarak içe aktar", // i18n-ignore
  "Import theme": "Temayı içe aktar", // i18n-ignore
  "Import {name}": "{name} temasını içe aktar", // i18n-ignore
  "Import": "İçe aktar", // i18n-ignore
  "Imported": "İçe aktarıldı", // i18n-ignore
  "Keep both": "İkisini de tut", // i18n-ignore
  "Keep this window open until it is done.": "Bitene kadar bu pencereyi açık tutun.", // i18n-ignore
  "Loading themes…": "Temalar yükleniyor…", // i18n-ignore
  "Nothing of the theme is live yet. Importing again picks up where it stopped; your own content was not changed.": "Temanın hiçbir parçası henüz yayında değil. Yeniden içe aktarmak kaldığı yerden devam eder; kendi içeriğinize dokunulmadı.", // i18n-ignore
  "Published today: {list}.": "Şu an yayında: {list}.", // i18n-ignore
  "Publishing “{name}”…": "“{name}” yayınlanıyor…", // i18n-ignore
  "Ready-made full-site themes · header, footer and pages": "Hazır site temaları · üst bilgi, alt bilgi ve sayfalar", // i18n-ignore
  "Ready-made full-site themes: header, footer and every page template": "Hazır site temaları: üst bilgi, alt bilgi ve her sayfa şablonu", // i18n-ignore
  "Replace them": "Onların yerine geç", // i18n-ignore
  "Saved as drafts": "Taslak olarak kaydedildi", // i18n-ignore
  "Shop pages (products, cart, checkout) keep using Pursae Commerce; the theme only designs the header, footer and the pages around them.": "Mağaza sayfaları (ürünler, sepet, ödeme) Pursae Commerce'i kullanmaya devam eder; tema yalnızca üst bilgiyi, alt bilgiyi ve çevresindeki sayfaları tasarlar.", // i18n-ignore
  "Templates: {list}": "Şablonlar: {list}", // i18n-ignore
  "The import stopped.": "İçe aktarma durdu.", // i18n-ignore
  "The library has no themes for Pursae Web Builder yet.": "Kütüphanede henüz Pursae Web Builder için tema yok.", // i18n-ignore
  "The theme could not be imported.": "Tema içe aktarılamadı.", // i18n-ignore
  "The theme library could not be loaded.": "Tema kütüphanesi yüklenemedi.", // i18n-ignore
  "The theme’s menus, in the Primary and Footer menu locations": "Temanın menüleri, Birincil ve Alt bilgi menü konumlarında", // i18n-ignore
  "Theme library": "Tema kütüphanesi", // i18n-ignore
  "This editor is out of date. Reload it and try again.": "Bu düzenleyici güncel değil. Yeniden yükleyip tekrar deneyin.", // i18n-ignore
  "Try again": "Tekrar dene", // i18n-ignore
  "View the site": "Siteyi görüntüle", // i18n-ignore
  "What you get": "Neler geliyor", // i18n-ignore
  "Your site already has templates": "Sitenizde zaten şablonlar var", // i18n-ignore
  "Your site has no templates yet, so nothing is replaced. Your posts, pages and settings stay as they are.": "Sitenizde henüz şablon yok, bu yüzden hiçbir şey değiştirilmez. Yazılarınız, sayfalarınız ve ayarlarınız olduğu gibi kalır.", // i18n-ignore
  "the theme’s templates are saved as drafts; nothing changes on your site until you publish them.": "temanın şablonları taslak olarak kaydedilir; siz yayınlayana kadar sitenizde hiçbir şey değişmez.", // i18n-ignore
  "the theme’s templates go live; yours are disabled (not deleted) and stay in the Theme Builder.": "temanın şablonları yayına girer; sizinkiler devre dışı kalır (silinmez) ve Tema Oluşturucu'da durur.", // i18n-ignore
  "{name} is live": "{name} yayında", // i18n-ignore
  "{n} demo images, added to your Media Library": "{n} demo görseli, Ortam Kitaplığınıza eklenir", // i18n-ignore
  "{n} demo pages published": "{n} demo sayfası yayınlandı", // i18n-ignore
  "{n} images added to the Media Library": "{n} görsel Ortam Kitaplığına eklendi", // i18n-ignore
  "{n} menus created and assigned": "{n} menü oluşturuldu ve atandı", // i18n-ignore
  "{n} menus": "{n} menü", // i18n-ignore
  "{n} pages": "{n} sayfa", // i18n-ignore
  "{n} templates published": "{n} şablon yayınlandı", // i18n-ignore
  "{n} templates saved as drafts in the Theme Builder": "{n} şablon Tema Oluşturucu'da taslak olarak kaydedildi", // i18n-ignore
  "{n} templates": "{n} şablon", // i18n-ignore
  "“{name}” has no design.": "“{name}” için tasarım yok.", // i18n-ignore
  "Page tools": "Sayfa araçları", // i18n-ignore
  "Languages": "Diller", // i18n-ignore
  "SEO": "SEO", // i18n-ignore
  "Loading languages…": "Diller yükleniyor…", // i18n-ignore
  "Loading SEO settings…": "SEO ayarları yükleniyor…", // i18n-ignore
  "The languages could not be loaded.": "Diller yüklenemedi.", // i18n-ignore
  "Languages are not available for this page.": "Bu sayfa için diller kullanılamıyor.", // i18n-ignore
  "This page": "Bu sayfa", // i18n-ignore
  "No language is set for this page.": "Bu sayfa için dil belirlenmemiş.", // i18n-ignore
  "Translations": "Çeviriler", // i18n-ignore
  "Missing translations": "Eksik çeviriler", // i18n-ignore
  "Current": "Geçerli", // i18n-ignore
  "Open": "Aç", // i18n-ignore
  "Add translation": "Çeviri ekle", // i18n-ignore
  "A translation starts as a draft copy of the published page. Your latest changes are saved as a draft first.": "Çeviri, sayfanın yayınlanmış halinin taslak bir kopyası olarak başlar. Önce son değişiklikleriniz taslak olarak kaydedilir.", // i18n-ignore
  "Your latest changes could not be saved yet. Try again in a moment.": "Son değişiklikleriniz henüz kaydedilemedi. Biraz sonra yeniden deneyin.", // i18n-ignore
  "The translation was created, but your latest changes could not be saved yet. Try again in a moment.": "Çeviri oluşturuldu, ancak son değişiklikleriniz henüz kaydedilemedi. Biraz sonra yeniden deneyin.", // i18n-ignore
  "You cannot edit this translation.": "Bu çeviriyi düzenleme yetkiniz yok.", // i18n-ignore
  "You do not have permission to change the social sharing settings.": "Sosyal paylaşım ayarlarını değiştirme yetkiniz yok.", // i18n-ignore
  "You do not have permission to change the canonical URL or search engine settings.": "Kanonik URL'yi veya arama motoru ayarlarını değiştirme yetkiniz yok.", // i18n-ignore
  "The translation could not be created.": "Çeviri oluşturulamadı.", // i18n-ignore
  "Published": "Yayında", // i18n-ignore
  "Draft": "Taslak", // i18n-ignore
  "Pending review": "İnceleme bekliyor", // i18n-ignore
  "Private": "Özel", // i18n-ignore
  "Scheduled": "Zamanlanmış", // i18n-ignore
  "SEO score": "SEO puanı", // i18n-ignore
  "Not analyzed yet. The score appears after you save.": "Henüz analiz edilmedi. Puan, kaydettiğinizde görünür.", // i18n-ignore
  "Calculated from the saved page content and these settings.": "Kaydedilmiş sayfa içeriğinden ve bu ayarlardan hesaplanır.", // i18n-ignore
  "Good": "İyi", // i18n-ignore
  "Needs improvement": "Geliştirilmeli", // i18n-ignore
  "Poor": "Zayıf", // i18n-ignore
  "Search appearance": "Arama görünümü", // i18n-ignore
  "Focus keyword": "Odak anahtar kelime", // i18n-ignore
  "The word or phrase you want this page to be found for.": "Bu sayfanın bulunmasını istediğiniz sözcük veya ifade.", // i18n-ignore
  "SEO title": "SEO başlığı", // i18n-ignore
  "Leave empty to use the template from the SEO settings.": "SEO ayarlarındaki şablonu kullanmak için boş bırakın.", // i18n-ignore
  "Meta description": "Meta açıklaması", // i18n-ignore
  "A short summary for search results, about 155 characters.": "Arama sonuçları için kısa bir özet, yaklaşık 155 karakter.", // i18n-ignore
  "Canonical URL": "Kanonik URL", // i18n-ignore
  "Only when the same content lives at another address.": "Yalnızca aynı içerik başka bir adreste de bulunuyorsa.", // i18n-ignore
  "Search engines": "Arama motorları", // i18n-ignore
  "Hide this page from search results": "Bu sayfayı arama sonuçlarından gizle", // i18n-ignore
  "Adds noindex: search engines will not list the page.": "noindex ekler: arama motorları sayfayı listelemez.", // i18n-ignore
  "Do not follow links on this page": "Bu sayfadaki bağlantıları takip etme", // i18n-ignore
  "Adds nofollow: search engines will not pass credit through its links.": "nofollow ekler: arama motorları bağlantıları üzerinden değer aktarmaz.", // i18n-ignore
  "Social sharing": "Sosyal paylaşım", // i18n-ignore
  "Social title": "Sosyal başlık", // i18n-ignore
  "Leave empty to use the SEO title.": "SEO başlığını kullanmak için boş bırakın.", // i18n-ignore
  "Social description": "Sosyal açıklama", // i18n-ignore
  "Leave empty to use the meta description.": "Meta açıklamasını kullanmak için boş bırakın.", // i18n-ignore
  "Social image": "Sosyal görsel", // i18n-ignore
  "Shown when the page is shared. 1200 × 630 pixels works best.": "Sayfa paylaşıldığında gösterilir. En iyi sonuç için 1200 × 630 piksel.", // i18n-ignore
  "Saved.": "Kaydedildi.", // i18n-ignore
  "The SEO settings could not be loaded.": "SEO ayarları yüklenemedi.", // i18n-ignore
  "The SEO settings could not be saved.": "SEO ayarları kaydedilemedi.", // i18n-ignore
}

export default tr
