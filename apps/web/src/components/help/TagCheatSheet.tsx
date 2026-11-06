import { Link } from 'react-router'
import { CopyButton } from '@/components/common/CopyButton'
import { useI18n, type Key } from '@/i18n/useI18n'

// The tags most used, to copy into Word.
const TAGS: [string, Key][] = [
  ['<<client.nom>>', 'sheet.field'],
  ["<<montant{numFormat='#,##0.00 €'}>>", 'sheet.number'],
  ["<<dateEffet{dateFormat='dd/MM/yyyy'}>>", 'sheet.date'],
  ['<<cs_{options.assistance = true}>> … <<else>> … <<es_>>', 'sheet.condition'],
  ['<<rr_lignes>> … <<er_lignes>>', 'sheet.rows'],
  ['<<rs_beneficiaires>> … <<es_beneficiaires>>', 'sheet.block'],
  ["<<rr_lignes:filter(type='option'):sort(DESC,montant)>>", 'sheet.sortFilter'],
  ['<<rs_lignes:group(type)>> <<$groupKey>> <<rs_$groupItems>> … <<es_$groupItems>> <<es_lignes>>', 'sheet.group'],
  ['<<$itemNum>>', 'sheet.number_item'],
  ['<<options.cg{renderer=boolean}>>', 'sheet.box'],
  ['<<{toUpper(client.nom)}>>', 'sheet.expression'],
  ['<<op:client.adresse2>>', 'sheet.optional'],
  ['<<ref:clauses/rgpd>>', 'sheet.ref'],
  ['<<refLookup:clause>>', 'sheet.refLookup'],
  ['<<link:client.site>>', 'sheet.link'],
  ['<<{numToEuros(montant)}>>', 'sheet.words'],
  ['<<{qrcode(contrat.lien)}>>', 'sheet.qr'],
  ["<<{barcode(colis.numero, 'code128')}>>", 'sheet.barcode'],
  ['imgfit_logo', 'sheet.image'],
]

/** The syntax of the tags at hand, while writing or trying a template. */
export function TagCheatSheet() {
  const { t } = useI18n()
  return (
    <details className="group rounded-xl border border-gray-200 dark:border-gray-700 bg-white dark:bg-gray-800">
      <summary className="cursor-pointer select-none px-4 py-3 text-sm font-semibold text-gray-800 dark:text-gray-200">{t('sheet.title')}</summary>
      <div className="px-4 pb-4">
        <p className="text-xs text-gray-500 dark:text-gray-400 mb-3">{t('sheet.intro')}</p>
        <ul className="divide-y divide-gray-100 dark:divide-gray-700">
          {TAGS.map(([tag, label]) => (
            <li key={tag} className="flex items-center gap-3 py-1.5">
              <div className="flex-1 min-w-0">
                <code className="text-xs font-mono text-brand-700 dark:text-brand-300 break-all">{tag}</code>
                <div className="text-xs text-gray-500 dark:text-gray-400">{t(label)}</div>
              </div>
              <CopyButton text={tag} className="shrink-0 py-0.5 px-2 text-xs" />
            </li>
          ))}
        </ul>
        <Link to="/aide/syntaxe" className="inline-block mt-3 text-xs font-medium text-brand-700 dark:text-brand-400 underline">{t('sheet.more')}</Link>
      </div>
    </details>
  )
}
