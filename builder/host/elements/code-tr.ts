/**
 * Turkish strings of the code editor (CodeEditor.tsx) and the Content tab's Edit code group
 * (ContentPanel.tsx), kept beside them so they add no lines to the shared host catalog
 * (../i18n-tr.ts). tc() falls back to the host's t().
 */
import { lang, type Vars } from '@/builder/i18n'
import { t as hostT } from '../i18n'

const tr: Record<string, string> = {
  "Edit code": "Kodu düzenle", // i18n-ignore
  "Whole element": "Tüm öğe", // i18n-ignore
  "Larger editor": "Büyük düzenleyici", // i18n-ignore
  "Close": "Kapat", // i18n-ignore
  "Find": "Bul", // i18n-ignore
  "Next match": "Sonraki eşleşme", // i18n-ignore
  "Previous match": "Önceki eşleşme", // i18n-ignore
  "{n} matches": "{n} eşleşme", // i18n-ignore
  "No match": "Eşleşme yok", // i18n-ignore
  "Close find": "Aramayı kapat", // i18n-ignore
  "Tab indents · Shift+Tab outdents · Esc leaves the field": "Tab girinti ekler · Shift+Tab geri alır · Esc alandan çıkar", // i18n-ignore
  "{key}+Enter applies": "{key}+Enter uygular", // i18n-ignore
  "Code editor": "Kod düzenleyici", // i18n-ignore
  "Element code": "Öğe kodu", // i18n-ignore
  "Code inside the element": "Öğenin içindeki kod", // i18n-ignore
  "HTML code": "HTML kodu", // i18n-ignore
  "Head code": "Head kodu", // i18n-ignore
  "Apply": "Uygula", // i18n-ignore
  "Revert": "Geri al", // i18n-ignore
  "Code changed": "Kod değişti", // i18n-ignore
  "HTML changed": "HTML değişti", // i18n-ignore
  "The element’s own markup, as stored. The editor’s ids are kept for you.": "Öğenin kendi işaretlemesi, saklandığı gibi. Düzenleyicinin kimlikleri sizin için korunur.", // i18n-ignore
  "The markup inside the element.": "Öğenin içindeki işaretleme.", // i18n-ignore
  "Your role may not edit code (it needs unfiltered HTML). It is shown here read-only.": "Rolünüz kodu düzenleyemez (filtresiz HTML yetkisi gerekir). Burada salt okunur gösterilir.", // i18n-ignore
  "The code is empty. Delete the element instead?": "Kod boş. Bunun yerine öğe silinsin mi?", // i18n-ignore
  "Delete element": "Öğeyi sil", // i18n-ignore
  "Line {line}: <{tag}> is never closed.": "Satır {line}: <{tag}> hiç kapatılmamış.", // i18n-ignore
  "Line {line}: </{tag}> has no opening tag.": "Satır {line}: </{tag}> için açılış etiketi yok.", // i18n-ignore
  "Line {line}: <{tag}> is not finished (a quote or > is missing).": "Satır {line}: <{tag}> bitmemiş (bir tırnak ya da > eksik).", // i18n-ignore
  "Line {line}: this code would change <{tag}> outside the element.": "Satır {line}: bu kod öğenin dışındaki <{tag}> öğesini değiştirir.", // i18n-ignore
  "Nothing was applied: the element is gone or cannot be edited here.": "Hiçbir şey uygulanmadı: öğe yok ya da burada düzenlenemez.", // i18n-ignore
  "Nothing changed.": "Değişen bir şey yok.", // i18n-ignore
  "You have code changes that were not applied. They are kept while you stay on this page; open the element again to continue.": "Uygulanmamış kod değişiklikleriniz var. Bu sayfada kaldığınız sürece saklanır; devam etmek için öğeyi yeniden açın.", // i18n-ignore
  "A draft you had not applied was restored.": "Uygulamadığınız bir taslak geri getirildi.", // i18n-ignore
  "Discard draft": "Taslağı sil", // i18n-ignore
  "Scripts run on the published page and in the Live canvas.": "Betikler yayınlanan sayfada ve canlı tuvalde çalışır.", // i18n-ignore
}

/** The panel's string `source` in the editor's language. */
export function tc(source: string, vars?: Vars): string {
  if (lang() === 'tr' && tr[source]) {
    const msg = tr[source]
    return vars ? msg.replace(/\{(\w+)\}/g, (m, k: string) => (k in vars ? String(vars[k] ?? '') : m)) : msg
  }
  return hostT(source, vars)
}
