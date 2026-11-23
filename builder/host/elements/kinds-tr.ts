/**
 * Turkish strings of the element types and names (kinds.ts, ElementsNav.tsx), kept beside them so
 * they add no lines to the shared host catalog (../i18n-tr.ts). tk() falls back to the host's t().
 */
import { lang, type Vars } from '@/builder/i18n'
import { t as hostT } from '../i18n'

const tr: Record<string, string> = {
  "Form": "Form", // i18n-ignore
  "3D / Canvas": "3B / Canvas", // i18n-ignore
  "Menu": "Menü", // i18n-ignore
  "Menu items": "Menü öğeleri", // i18n-ignore
  "Header": "Üst bölüm", // i18n-ignore
  "Footer": "Alt bölüm", // i18n-ignore
  "Main content": "Ana içerik", // i18n-ignore
  "Sidebar": "Kenar çubuğu", // i18n-ignore
  "Article": "Makale", // i18n-ignore
  "Figure": "Şekil", // i18n-ignore
  "Table": "Tablo", // i18n-ignore
  "Accordion": "Akordeon", // i18n-ignore
  "Popup": "Açılır pencere", // i18n-ignore
  "Quote": "Alıntı", // i18n-ignore
  "Divider": "Ayraç", // i18n-ignore
  "Audio": "Ses", // i18n-ignore
  "Map": "Harita", // i18n-ignore
  "Field": "Alan", // i18n-ignore
  "Slider": "Slayt", // i18n-ignore
  "Tabs": "Sekmeler", // i18n-ignore
  "Cards": "Kartlar", // i18n-ignore
  "Hero": "Hero", // i18n-ignore
  "Badge": "Rozet", // i18n-ignore
  "Logo": "Logo", // i18n-ignore
  "Graphic": "Grafik", // i18n-ignore
  "Section {n}": "Bölüm {n}", // i18n-ignore
  "{n} code blocks": "{n} kod bloğu", // i18n-ignore
  "Show {n} more": "{n} tane daha göster", // i18n-ignore
}

/** The string `source` in the editor's language. */
export function tk(source: string, vars?: Vars): string {
  if (lang() === 'tr' && tr[source]) {
    const msg = tr[source]
    return vars ? msg.replace(/\{(\w+)\}/g, (m, k: string) => (k in vars ? String(vars[k] ?? '') : m)) : msg
  }
  return hostT(source, vars)
}
