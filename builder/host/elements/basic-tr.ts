/**
 * Turkish strings of Basic mode's block panel (BasicCode.tsx, index.ts summaries), kept beside it so
 * the panel adds no lines to the shared host catalog (../i18n-tr.ts). tbasic() falls back to the
 * host's t().
 */
import { lang, type Vars } from '@/builder/i18n'
import { t as hostT } from '../i18n'

const tr: Record<string, string> = {
  "On this section": "Bu bölümde", // i18n-ignore
  "Heading": "Başlık", // i18n-ignore
  "Text": "Metin", // i18n-ignore
  "Button": "Düğme", // i18n-ignore
  "Link": "Bağlantı", // i18n-ignore
  "Image": "Görsel", // i18n-ignore
  "Form field": "Form alanı", // i18n-ignore
  "Video": "Video", // i18n-ignore
  "Embed": "Gömülü içerik", // i18n-ignore
  "Placeholder": "Yer tutucu metin", // i18n-ignore
  "Address": "Adres", // i18n-ignore
  "Alternative text": "Alternatif metin", // i18n-ignore
  "Describe the image (empty: decorative)": "Görseli anlatın (boş: süs amaçlı)", // i18n-ignore
  "Choose a page of the site": "Sitenin bir sayfasını seç", // i18n-ignore
  "Search pages and posts": "Sayfa ve yazı ara", // i18n-ignore
  "Replace image": "Görseli değiştir", // i18n-ignore
  "Card {n}": "Kart {n}", // i18n-ignore
  "Item {n}": "Öğe {n}", // i18n-ignore
  "Slide {n}": "Slayt {n}", // i18n-ignore
  "Row {n}": "Satır {n}", // i18n-ignore
  "Links": "Bağlantılar", // i18n-ignore
  "Buttons": "Düğmeler", // i18n-ignore
  "Show {count} more": "{count} alan daha göster", // i18n-ignore
  "Nothing to edit here yet.": "Burada düzenlenecek bir şey yok.", // i18n-ignore
  "This section is made of the design's own layout. To change more of it, switch to Studio.": "Bu bölüm tasarımın kendi yerleşiminden oluşuyor. Daha fazlasını değiştirmek için Studio'ya geçin.", // i18n-ignore
  "Need more control?": "Daha fazla denetim mi lazım?", // i18n-ignore
  "Switch to Studio": "Studio'ya geç", // i18n-ignore
  "Your role may not change this part of the design, so the fields are read-only here.": "Rolünüz tasarımın bu bölümünü değiştirmeye yetmiyor; alanlar burada yalnızca okunur.", // i18n-ignore
  "Hero": "Giriş", // i18n-ignore
  "Features": "Özellikler", // i18n-ignore
  "Pricing": "Fiyatlar", // i18n-ignore
  "Testimonials": "Yorumlar", // i18n-ignore
  "FAQ": "Sık sorulanlar", // i18n-ignore
  "Contact": "İletişim", // i18n-ignore
  "Footer": "Alt bilgi", // i18n-ignore
  "Menu": "Menü", // i18n-ignore
  "Header": "Üst bilgi", // i18n-ignore
  "Call to action": "Harekete geçirici bölüm", // i18n-ignore
  "About": "Hakkında", // i18n-ignore
  "Team": "Ekip", // i18n-ignore
  "Gallery": "Galeri", // i18n-ignore
  "Blog": "Blog", // i18n-ignore
  "Logos": "Logolar", // i18n-ignore
  "Numbers": "Sayılar", // i18n-ignore
  "Services": "Hizmetler", // i18n-ignore
  "Slider": "Slayt gösterisi", // i18n-ignore
  "Form": "Form", // i18n-ignore
  "Steps": "Adımlar", // i18n-ignore
  "{count} heading": "{count} başlık", // i18n-ignore
  "{count} headings": "{count} başlık", // i18n-ignore
  "{count} text": "{count} metin", // i18n-ignore
  "{count} texts": "{count} metin", // i18n-ignore
  "{count} button": "{count} düğme", // i18n-ignore
  "{count} buttons": "{count} düğme", // i18n-ignore
  "{count} link": "{count} bağlantı", // i18n-ignore
  "{count} links": "{count} bağlantı", // i18n-ignore
  "{count} image": "{count} görsel", // i18n-ignore
  "{count} images": "{count} görsel", // i18n-ignore
  "{count} form field": "{count} form alanı", // i18n-ignore
  "{count} form fields": "{count} form alanı", // i18n-ignore
  "{count} video": "{count} video", // i18n-ignore
  "{count} videos": "{count} video", // i18n-ignore
}

/** The panel's string `source` in the editor's language. */
export function tbasic(source: string, vars?: Vars): string {
  if (lang() === 'tr' && tr[source]) {
    const msg = tr[source]
    return vars ? msg.replace(/\{(\w+)\}/g, (m, k: string) => (k in vars ? String(vars[k] ?? '') : m)) : msg
  }
  return hostT(source, vars)
}
