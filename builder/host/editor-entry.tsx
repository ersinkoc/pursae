/**
 * Editor entry of the Pursae Web Builder inside the CMS (replaces upstream src/main.tsx in the
 * build, see builder/vite.config.ts editorEntry()).
 *
 * Builder modules translate some strings once, at module load (section kinds, dynamic widget
 * labels, constant option tables). A static import of the editor would not do: the bundler puts
 * those modules in chunks the entry chunk imports statically, and imported chunks run before the
 * entry's own code, so they would see the upstream default language (Turkish) instead of the boot
 * locale. Hence two steps: ./locale sets the language (it imports only the i18n module and its
 * catalogs), then the editor is loaded with a dynamic import, which evaluates every other module
 * after it. The stylesheet stays here so the manifest lists it with the entry. Keep this file to
 * these statements (test/host/locale-order.test.ts).
 */
import './locale'
import '@/index.css'

void import('./editor-main')
