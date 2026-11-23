/**
 * Turkish strings of the canvas's Live / Safe mode, the script-trust prompt and Interact mode
 * (ElementsCanvas.tsx, ScriptsGate.tsx), kept beside them so they add no lines to the shared host
 * catalog (../i18n-tr.ts). tc() falls back to the host's t().
 */
import { lang, type Vars } from '@/builder/i18n'
import { t as hostT } from '../i18n'

const tr: Record<string, string> = {
  "This design uses scripts. Run them so the editor matches your site?": "Bu tasarım betik kullanıyor. Editör sitenizle aynı görünsün diye çalıştırılsın mı?", // i18n-ignore
  "Scripts run inside the editor with your admin access: they can do anything you can do on this site, such as change content, settings and users. Only run designs you trust.": "Betikler editörde yönetici yetkinizle çalışır: bu sitede sizin yapabildiğiniz her şeyi (içeriği, ayarları, kullanıcıları değiştirmek gibi) yapabilirler. Yalnızca güvendiğiniz tasarımları çalıştırın.", // i18n-ignore
  "Always for this design": "Bu tasarım için her zaman", // i18n-ignore
  "Links open on the published page, not in the editor: {url}": "Bağlantılar editörde değil, yayındaki sayfada açılır: {url}", // i18n-ignore
  "This session only": "Yalnızca bu oturum", // i18n-ignore
  "Not now (safe mode)": "Şimdi değil (güvenli mod)", // i18n-ignore
  "Live": "Canlı", // i18n-ignore
  "Safe mode": "Güvenli mod", // i18n-ignore
  "Live: the design’s scripts run and the canvas matches your site.": "Canlı: tasarımın betikleri çalışır, tuval sitenizle aynıdır.", // i18n-ignore
  "Safe mode: the design’s scripts do not run in the editor (they run on the published page).": "Güvenli mod: tasarımın betikleri editörde çalışmaz (yayındaki sayfada çalışır).", // i18n-ignore
  "Stop always running this design’s scripts": "Bu tasarımın betiklerini artık her zaman çalıştırma", // i18n-ignore
  "This design’s scripts no longer run automatically in the editor.": "Bu tasarımın betikleri artık editörde kendiliğinden çalışmayacak.", // i18n-ignore
  "Your choice could not be saved: {message}": "Seçiminiz kaydedilemedi: {message}", // i18n-ignore
  "Interact": "Etkileşim", // i18n-ignore
  "Interact with the page: clicks and keys go to the design (theme switch, menus, tabs, sliders). Hold {key} to interact for a moment.": "Sayfayla etkileşim: tıklamalar ve tuşlar tasarıma gider (tema düğmesi, menüler, sekmeler, kaydırıcılar). Kısa bir etkileşim için {key} tuşunu basılı tutun.", // i18n-ignore
  "Interacting with the page · Esc or Interact to go back to editing": "Sayfayla etkileşimdesiniz · düzenlemeye dönmek için Esc ya da Etkileşim", // i18n-ignore
  "Safe mode · scripts run on the published page": "Güvenli mod · betikler yayındaki sayfada çalışır", // i18n-ignore
  "Your role cannot run a design’s scripts in the editor. They run on the published page.": "Rolünüz bir tasarımın betiklerini editörde çalıştıramaz. Yayındaki sayfada çalışırlar.", // i18n-ignore
  "The design’s scripts run on the published page.": "Tasarımın betikleri yayındaki sayfada çalışır.", // i18n-ignore
  "Text shown by the design’s script": "Tasarımın betiğinin gösterdiği metin", // i18n-ignore
  "On the page the design’s script shows this text differently. This edits the text stored in the page; the script may still change it.": "Sayfada tasarımın betiği bu metni farklı gösteriyor. Burada sayfada saklanan metni düzenlersiniz; betik onu yine değiştirebilir.", // i18n-ignore
  "Save": "Kaydet", // i18n-ignore
  "Cancel": "Vazgeç", // i18n-ignore
}

/** The canvas string `source` in the editor's language. */
export function tc(source: string, vars?: Vars): string {
  if (lang() === 'tr' && tr[source]) {
    const msg = tr[source]
    return vars ? msg.replace(/\{(\w+)\}/g, (m, k: string) => (k in vars ? String(vars[k] ?? '') : m)) : msg
  }
  return hostT(source, vars)
}
