/**
 * UI language of the editor, set before any builder module is evaluated.
 *
 * Builder modules translate some strings once, at module load (section and design names, labels in
 * constant tables), so the language must be known before `@/App` is imported. editor-entry.tsx
 * imports this module and only then loads the editor (editor-main.tsx) with a dynamic import; it
 * reads only the boot config's `locale` (contract §2) and does not import cms-host.ts, which pulls
 * in builder modules.
 *
 * The language is the locale's language part ("tr_TR" → Turkish); a language without a catalog
 * (upstream src/builder/i18n/) falls back to the English source strings.
 */
import { setLocale } from '@/builder/i18n'

const boot = (window as { __WEB_BUILDER__?: { locale?: unknown } }).__WEB_BUILDER__
setLocale(typeof boot?.locale === 'string' ? boot.locale : 'en_US')
