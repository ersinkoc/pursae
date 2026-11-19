/**
 * Turkish strings of the content-binding panel (BindPanel.tsx), kept beside it so the panel adds no
 * lines to the shared host catalog (../i18n-tr.ts). tb() falls back to the host's t().
 */
import { lang, type Vars } from '@/builder/i18n'
import { t as hostT } from '../i18n'

const tr: Record<string, string> = {
  "Content binding": "İçerik bağlama", // i18n-ignore
  "Not bound": "Bağlı değil", // i18n-ignore
  "Menu": "Menü", // i18n-ignore
  "Submenu": "Alt menü", // i18n-ignore
  "Loop": "Döngü", // i18n-ignore
  "Field": "Alan", // i18n-ignore
  "Form": "Form", // i18n-ignore
  "Bind to": "Bağla", // i18n-ignore
  "Location": "Konum", // i18n-ignore
  "Menu id": "Menü kimliği", // i18n-ignore
  "Post type": "Yazı türü", // i18n-ignore
  "Parent page id": "Üst sayfa kimliği", // i18n-ignore
  "Terms": "Terimler", // i18n-ignore
  "Count": "Adet", // i18n-ignore
  "Order by": "Sıralama ölçütü", // i18n-ignore
  "Order": "Sıra", // i18n-ignore
  "Offset": "Atla", // i18n-ignore
  "Title": "Başlık", // i18n-ignore
  "Excerpt": "Özet", // i18n-ignore
  "Content": "İçerik", // i18n-ignore
  "Featured image": "Öne çıkan görsel", // i18n-ignore
  "Permalink": "Kalıcı bağlantı", // i18n-ignore
  "Link with title": "Başlıklı bağlantı", // i18n-ignore
  "Date": "Tarih", // i18n-ignore
  "Modified date": "Değiştirilme tarihi", // i18n-ignore
  "Author": "Yazar", // i18n-ignore
  "Custom field": "Özel alan", // i18n-ignore
  "Pagination": "Sayfalama", // i18n-ignore
  "Field name": "Alan adı", // i18n-ignore
  "Date format": "Tarih biçimi", // i18n-ignore
  "Taxonomy": "Sınıflandırma", // i18n-ignore
  "Separator": "Ayraç", // i18n-ignore
  "Fills": "Doldurur", // i18n-ignore
  "Text": "Metin", // i18n-ignore
  "Link or image URL": "Bağlantı veya görsel adresi", // i18n-ignore
  "Attributes (<name>_href, <name>_src, …)": "Öznitelikler (<name>_href, <name>_src, …)", // i18n-ignore
  "Words": "Sözcük", // i18n-ignore
  "Success message": "Başarı mesajı", // i18n-ignore
  "Error message": "Hata mesajı", // i18n-ignore
  "Default": "Varsayılan", // i18n-ignore
  "Leave out": "Gönderme", // i18n-ignore
  "Required": "Zorunlu", // i18n-ignore
  "Recipients": "Alıcılar", // i18n-ignore
  "Remove binding": "Bağlamayı kaldır", // i18n-ignore
  "Binding changed": "Bağlama değişti", // i18n-ignore
  "On the site": "Sitede", // i18n-ignore
  "No items.": "Öğe yok.", // i18n-ignore
  "Loading…": "Yükleniyor…", // i18n-ignore
  "The items of the list are templates: each menu item uses the one it was imported from, new items the first plain one. Mark a dropdown list inside an item as Submenu.": "Listenin öğeleri şablondur: her menü öğesi içe aktarıldığı öğeyi, yeni öğeler ilk sade öğeyi kullanır. Bir öğenin içindeki açılır listeyi Alt menü olarak işaretleyin.", // i18n-ignore
  "The first child is the card template; bind its elements to fields. The other children are the design's samples and are left out on the site.": "İlk çocuk kart şablonudur; içindeki öğeleri alanlara bağlayın. Diğer çocuklar tasarımın örnekleridir ve sitede gösterilmez.", // i18n-ignore
  "Filled from the post of the loop card, else from the post the template shows.": "Döngü kartının yazısından, yoksa şablonun gösterdiği yazıdan doldurulur.", // i18n-ignore
  "The form posts to Pursae: submissions are stored and mailed (Settings › Web Builder). The design's own validation still runs.": "Form Pursae'ye gönderilir: gönderimler saklanır ve e-postayla iletilir (Ayarlar › Web Builder). Tasarımın kendi doğrulaması çalışmaya devam eder.", // i18n-ignore
  "Fills its dropdown list from the item's sub-items (one level).": "Açılır listesini öğenin alt öğeleriyle doldurur (bir düzey).", // i18n-ignore
  "Shows {n} items:": "{n} öğe gösterir:", // i18n-ignore
}

/** The panel's string `source` in the editor's language. */
export function tb(source: string, vars?: Vars): string {
  if (lang() === 'tr' && tr[source]) {
    const msg = tr[source]
    return vars ? msg.replace(/\{(\w+)\}/g, (m, k: string) => (k in vars ? String(vars[k] ?? '') : m)) : msg
  }
  return hostT(source, vars)
}
