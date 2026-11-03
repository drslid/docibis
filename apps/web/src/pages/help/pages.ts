// Help pages, in Markdown (react-markdown + remark-gfm). They are
// written in French; examples use the address of the instance.
import { BookOpen, Bot, Code2, FileStack, PenLine, type LucideIcon } from 'lucide-react'
import { examples } from '@/pages/apiExamples'

export interface HelpPage {
  slug: string
  title: string
  summary: string
  icon: LucideIcon
  colour: string
  content: (origin: string) => string
}

const fences: Record<string, string> = { curl: 'sh', python: 'python', javascript: 'javascript', java: 'java', csharp: 'csharp', php: 'php', go: 'go' }

const docmosis = (origin: string) => `# Passer de Docmosis à DocIbis

DocIbis garde les **mêmes modèles Word** et les **mêmes noms** : une application
passe de Docmosis à DocIbis en changeant l'adresse et la clé d'accès.

## 1. Importer les modèles

Page [Modèles](/modeles), bouton **Importer un zip** : le zip du dossier
\`templates/\` de Docmosis. Chaque fichier garde son chemin, qui devient son nom
(\`templateName\`). Le préfixe \`templates/\` est retiré.

À côté d'un modèle \`X.docx\`, deux fichiers facultatifs forment sa **référence** :

| Fichier | Contenu |
|---|---|
| \`X.docx.reference.pdf\` | le PDF produit par Docmosis |
| \`X.docx.data.json\` | les données utilisées pour ce PDF |

DocIbis produit alors le même document et le compare au PDF de Docmosis, page par
page. La comparaison recommence seule après chaque nouvelle version de DocIbis :
un écart se voit avant d'arriver aux utilisateurs.

## 2. Appeler DocIbis comme Docmosis

La route \`/api/render\` accepte l'appel de Docmosis tel quel. Seule la clé change :
une clé API DocIbis (page Clés API) dans \`accessKey\`.

\`\`\`sh
curl -X POST ${origin}/api/render \\
  -H "Content-Type: application/json" \\
  -d '{"accessKey": "dib_…", "templateName": "acme/contracts/fiche_conseil.docx",
       "outputName": "fiche.pdf", "data": {"client": {"nom": "Dupont"}}}' \\
  -o fiche.pdf
\`\`\`

- Le format vient de \`outputFormat\`, sinon de l'extension de \`outputName\` (pdf, docx).
- Une erreur revient comme chez Docmosis : \`{"succeeded": false, "shortMsg": "…"}\`.
- Le nom peut omettre l'extension : \`acme/contracts/fiche_conseil\` trouve le \`.docx\`.
- Les options PDF de Docmosis sont reprises sous leurs noms : \`passwordProtect\`,
  \`pdfRestrictPassword\`, \`pdfRestrictPrinting\`, \`pdfRestrictEditing\`, \`pdfRestrictCopy\`,
  \`pdfRestrictAllowAccessibilty\`, \`pdfSkipEmptyPages\`, \`pdfWatermark\`, \`pdfWatermarkColor\`,
  \`pdfWatermarkFontSize\`, \`pdfWatermarkRotation\`, \`pdfTagged\` et \`devMode\`.

## 3. La route DocIbis

\`POST /v1/render\` prend \`templateName\` (ou le fichier du modèle) et \`data\`, avec
les options : \`pdfa\` (PDF/A-1b, 2b, 3b), \`accessible\` (PDF balisé PDF/UA),
\`watermark\` (texte du filigrane, \`none\` pour aucun), \`watermarkColor\`,
\`watermarkTiled\`, \`dataCheck\` (off, warn, strict) et \`outputName\`. Les valeurs
par défaut se règlent dans Configuration › Documents produits.

Options PDF, sous les noms de Docmosis : \`passwordProtect\` (mot de passe d'ouverture),
\`pdfRestrictPassword\` (mot de passe du propriétaire) avec \`pdfRestrictPrinting\` (0 à 2),
\`pdfRestrictEditing\` (0 à 4), \`pdfRestrictCopy\`, \`pdfRestrictAllowAccessibilty\` ;
\`pdfSkipEmptyPages\` retire les pages blanches ajoutées pour commencer sur une page paire
ou impaire. Les mots de passe ne sont jamais gardés : un document produit à nouveau
depuis l'historique sort sans protection. \`title\`, \`author\`, \`subject\` et \`keywords\`
remplissent les propriétés du PDF (le titre est utile aux lecteurs d'écran).
\`devMode\` (comme Docmosis) écrit en surbrillance, dans le document, les données absentes
et les expressions illisibles ; la page Essai l'active par défaut.

Le filigrane ne se combine pas avec PDF/A ni avec le PDF accessible (LibreOffice
se bloque) : la demande l'emporte sur la configuration, les deux demandés
sont refusés. Un PDF/A ne peut pas avoir de mot de passe.

Le PDF accessible est conforme PDF/UA-1 (vérifié avec veraPDF) : sans \`title\` ni titre
dans le modèle, le nom du document lui sert de titre. Pour un PDF à la fois accessible et
archivable, choisissez PDF/A-2b ou PDF/A-3b : avec PDF/A-1b, il reste balisé mais sans la
marque PDF/UA.

\`POST /v1/render/batch\` produit plusieurs documents en un appel (un contrat et
ses conditions générales en PDF fixe de la bibliothèque, par exemple) et les
joint en un seul PDF, ou les rend dans un zip.

\`GET /v1/templates\` liste les modèles publiés et les données que chacun attend.

## 4. Données manquantes

Chaque modèle connaît les données qu'il lit. Une donnée absente est signalée par
l'en-tête \`X-Missing-Fields\` (contrôle « Signaler », par défaut) ou refuse le
document (contrôle « Refuser », réglé modèle par modèle).

## 5. Versions

Chaque envoi crée une version. La version **publiée** est servie aux
applications ; revenir à une ancienne se fait en un clic. Le journal d'audit
garde qui a publié quoi.
`

const start = `# Bien démarrer

DocIbis produit des documents PDF et Word à partir de **modèles Word** (balises Docmosis \`<<champ>>\`) et de **données JSON**.

## Les rôles

| Rôle | Peut… |
|---|---|
| Lecteur | consulter le tableau de bord, les documents produits, l'aide et les intégrations |
| Éditeur | en plus, essayer des modèles (page **Essai**) |
| Administrateur | tout : comptes, clés API, configuration, journal d'audit, monitoring |

Un administrateur crée les comptes dans **Utilisateurs** avec un mot de passe provisoire ; chacun le change dans **Mon profil**, où se choisissent aussi la langue et le thème.

## Essayer un modèle

1. Ouvrez **Essai** et déposez un modèle \`.docx\` (ou « Utiliser l'exemple »).
2. DocIbis lit ses balises et propose des données d'exemple : ajustez-les.
3. « Produire le PDF » affiche le document et son temps de production.

## Suivre la production

- **Documents produits** liste chaque document, de la console, de l'API ou du serveur MCP : date, modèle, auteur ou clé, durée, résultat. Ni le document ni ses données ne sont gardés.
- **Journal d'audit** (administrateurs) trace les connexions et les changements.
- **Monitoring** (administrateurs) montre l'état du service en direct.

## Brancher une application

Voir [Utiliser l'API](/aide/api) et [Serveur MCP](/aide/mcp), ou la page **Intégrations**.
`

function api(origin: string): string {
  const calls = examples(origin)
    .map((e) => `### ${e.label}\n\n\`\`\`${fences[e.id] ?? ''}\n${e.code.trimEnd()}\n\`\`\``)
    .join('\n\n')
  return `# Utiliser l'API

Les applications produisent leurs documents en appelant DocIbis : un modèle Word et des données JSON en entrée, le PDF (ou le Word) en sortie. Toutes les routes sont décrites dans la [référence de l'API](/docs).

## 1. Une clé

Une clé par application et par environnement, créée par un administrateur dans **Clés API**. Elle s'envoie dans l'en-tête \`Authorization: Bearer dib_…\` et se range dans le coffre de secrets de l'application, jamais dans le code.

## 2. Un appel

\`POST ${origin}/v1/render\`, en \`multipart/form-data\` :

| Champ | Contenu |
|---|---|
| \`template\` | Le modèle Word (\`.docx\`) avec ses balises Docmosis. Obligatoire. |
| \`data\` | Les données JSON, en texte ou en fichier. Obligatoire. |
| \`format\` | \`pdf\` (par défaut) ou \`docx\`. |

## 3. Le document

La réponse est le document ; son temps de production est dans l'en-tête \`X-Render-Ms\`. Ni le document ni ses données ne sont gardés.

## Exemples

Ils envoient le [modèle d'exemple](/exemple-docibis.docx) et ses [données](/exemple-docibis.json) ; la clé est lue dans la variable \`DOCIBIS_API_KEY\`.

${calls}

## Erreurs

| Code | Signification |
|---|---|
| 400 | Modèle ou données refusés : le champ \`detail\` dit pourquoi. |
| 401 | Clé absente, inconnue, expirée ou révoquée. |
| 413 | Modèle ou données trop gros (tailles maximales de la configuration). |
| 503 | Moteur occupé ou en démarrage : réessayer après \`Retry-After\` secondes. |

Les erreurs sont en JSON (RFC 9457) : \`{"status": 400, "title": "Bad Request", "detail": "…"}\`.

## En arrière-plan

Pour les gros documents, les envois en nombre ou un dépôt direct sur S3 : \`POST ${origin}/v1/render/async\`, avec les mêmes champs que \`/v1/render\`. DocIbis vérifie la demande, répond tout de suite \`202\` avec un travail (\`{"id": "job_…", "status": "queued"}\`), puis produit le document.

| Champ en plus | Contenu |
|---|---|
| \`callbackUrl\` | Adresse HTTPS appelée en \`POST\` à la fin (document prêt ou en échec), avec l'état du travail en JSON. |
| \`uploadUrl\` | Adresse HTTPS où DocIbis dépose le document par \`PUT\` : une adresse S3 présignée, par exemple. |

Un administrateur autorise les domaines de ces adresses (**Configuration**, **Rendu asynchrone**). Elles restent en mémoire, jamais dans la base ni dans les journaux.

- \`GET /v1/jobs/{id}\` : l'état, \`queued\`, \`running\`, \`done\` ou \`failed\` (avec \`error\`).
- \`GET /v1/jobs/{id}/document\` : le document, une fois prêt. Il est effacé après le délai de conservation (60 minutes par défaut), et n'est pas gardé s'il a été déposé à \`uploadUrl\`.

Seule la clé qui a fait la demande lit le travail. Les demandes attendent en mémoire : un redémarrage du service les met en échec (état \`failed\`, avec rappel quand il s'arrête normalement), il suffit de les relancer.

### Vérifier un rappel

Chaque rappel porte \`X-DocIbis-Event\` (\`render.done\` ou \`render.failed\`), \`X-DocIbis-Timestamp\` (secondes Unix) et, quand la variable \`WEBHOOK_SECRET\` est posée sur le serveur, \`X-DocIbis-Signature: sha256=…\` : le HMAC-SHA256 du secret sur l'horodatage, un point, puis le corps reçu. Recalculez-le, comparez en temps constant et refusez un horodatage de plus de 5 minutes. En Python :

\`\`\`python
import hashlib, hmac, time

def rappel_valide(secret: bytes, headers, body: bytes) -> bool:
    ts = headers["X-DocIbis-Timestamp"]
    attendu = "sha256=" + hmac.new(secret, ts.encode() + b"." + body, hashlib.sha256).hexdigest()
    return abs(time.time() - int(ts)) < 300 and hmac.compare_digest(attendu, headers["X-DocIbis-Signature"])
\`\`\`

## Autres langages

Générez un client depuis le contrat OpenAPI : [${origin}/v1/openapi.json](/v1/openapi.json).
`
}

function mcp(origin: string): string {
  return `# Serveur MCP

Le serveur MCP de DocIbis laisse un assistant IA (Claude, VS Code avec Copilot, Cursor…) **analyser un modèle**, **produire un document** et **consulter l'historique**, en votre nom, avec un jeton personnel.

## Adresse et jeton

- Adresse : \`${origin}/v1/mcp\` (transport *streamable HTTP*).
- Jeton : un jeton personnel (\`dit_…\`), créé dans **Mon profil**, onglet **Jetons MCP**, envoyé dans l'en-tête \`Authorization: Bearer …\` ou \`X-API-Key\`. Une clé API d'application (\`dib_…\`) fonctionne aussi.

Les documents produits par l'assistant apparaissent dans **Documents produits**, avec l'origine « MCP » et votre nom ; chaque appel est listé dans **Intégrations**, section **Appels MCP**.

## VS Code

Fichier \`.vscode/mcp.json\` ; la clé est demandée au démarrage du serveur et n'est jamais écrite dans le fichier :

\`\`\`json
{
  "inputs": [{ "type": "promptString", "id": "docibis-api-key", "description": "Jeton personnel DocIbis (dit_…, page Profil)", "password": true }],
  "servers": {
    "docibis": {
      "type": "http",
      "url": "${origin}/v1/mcp",
      "headers": { "Authorization": "Bearer \${input:docibis-api-key}" }
    }
  }
}
\`\`\`

## Claude Desktop

Claude Desktop passe par le pont \`mcp-remote\` (Node.js) ; la clé reste dans une variable d'environnement :

\`\`\`json
{
  "mcpServers": {
    "docibis": {
      "command": "npx",
      "args": ["-y", "mcp-remote", "${origin}/v1/mcp", "--header", "Authorization:Bearer \${DOCIBIS_API_KEY}"],
      "env": { "DOCIBIS_API_KEY": "dit_..." }
    }
  }
}
\`\`\`

## Outils

| Outil | Rôle |
|---|---|
| \`docibis_whoami\` | La clé utilisée, l'instance et la version : à appeler d'abord. |
| \`docibis_analyze_template\` | Lit un modèle Word (en base64) : balises, moteur possible, données d'exemple. |
| \`docibis_render_document\` | Produit le PDF ou le Word d'un modèle et de ses données ; le document revient en pièce jointe. |
| \`docibis_list_documents\` | Les derniers documents produits, avec recherche et filtre sur le résultat. |
| \`docibis_stats\` | Les chiffres du tableau de bord : volumes, taux de réussite, durées. |

L'assistant ne produit un document que si on le lui demande ; rien n'est modifié dans DocIbis par ces outils.
`
}

function syntax(origin: string): string {
  return `# Écrire un modèle

Un modèle est un document Word ordinaire. Les balises entre \`<<\` et \`>>\` sont remplacées
par les données JSON envoyées avec la demande. La syntaxe est celle de Docmosis.

## Champs

| Balise | Résultat |
|---|---|
| \`<<client.nom>>\` | la valeur de \`client.nom\` |
| \`<<montant{numFormat='#,##0.00 €'}>>\` | un nombre écrit à la française : 1 250,50 € |
| \`<<dateEffet{dateFormat='dd MMMM yyyy'}>>\` | une date : 19 octobre 2026 |
| \`<<{numFormat(prime * 12, '#,##0.00')}>>\` | un calcul mis en forme |
| \`<<{toUpper(client.nom)}>>\` | une expression : DUPONT |
| \`<<{if(client.civilite = 'M', 'Monsieur', 'Madame')}>>\` | un choix |
| \`<<op:client.adresse2>>\` | la valeur ; si elle est vide, **tout le paragraphe** disparaît (pas de ligne blanche) |
| \`<<link:client.site>>\` | un lien cliquable ; la valeur est l'adresse, ou \`texte\\|adresse\` (https ou mailto) |
| \`<<{numToEuros(montant)}>>\` | un montant en toutes lettres : mille deux cent quatre-vingts euros et cinquante centimes |

Les formats de nombres s'écrivent comme en Java (\`#,##0.00\`, \`0.0 %\`) ; le résultat suit
les usages français (espace entre les milliers, virgule décimale), ou anglais avec \`'en'\`
en troisième argument de \`numFormat\`.

Fonctions des expressions : \`dateFormat\`, \`numFormat\`, \`toUpper\`, \`toLower\`, \`trim\`,
\`capitalize\`, \`length\`, \`substring\`, \`replace\`, \`concat\`, \`contains\`, \`startsWith\`,
\`endsWith\`, \`isEmpty\`, \`isNotEmpty\`, \`round\`, \`abs\`, \`min\`, \`max\`, \`sum(liste, 'champ')\`, \`if\`.

Aussi, comme Docmosis :

- texte : \`titleCase\`, \`toSentence\`, \`toUpperCase\`, \`toLowerCase\`, \`left(texte, n)\`, \`right(texte, n)\`,
  \`split(texte, '|', 1)\`, \`indexOf\`, \`charAt\`, \`char(169)\`, \`equalsIgnoreCase\`, \`replaceStr\`,
  \`replaceFirst\`, \`countStr\`, \`toRoman\`, \`toAlpha\`, \`toAlpha2\` ;
- nombres : \`ceil\`, \`floor\`, \`pow\`, \`sqrt\`, \`isNumber\` ;
- logique : \`ifBlank(champ, 'aucun')\`, \`isBlank(champ)\`, \`map(genre, 'M', 'Monsieur', 'F', 'Madame', 'Madame, Monsieur')\`
  (\`mapi\` sans tenir compte des majuscules) ;
- dates : \`dateAdd(date, 1, 'month', 'dd/MM/yyyy')\` (millis, seconds, minutes, hours, days, weeks,
  months, years ; un format de lecture en cinquième argument), \`dateDiff(debut, fin, 'days')\`
  (fin moins début, en unités entières) ;
- nombres en lettres : \`numToText(123)\` (anglais, comme Docmosis), \`numToText(123, 'fr')\` (cent
  vingt-trois), \`numToEuros(montant)\` (en français ; \`'en'\` pour l'anglais), \`numToDollars\`.
Opérateurs : \`+ - * / %\`, \`= != < <= > >=\`, \`and or not\` (ou \`&& || !\`).

Les dates s'écrivent avec les lettres de Java : \`dd/MM/yyyy\`, \`d MMMM yyyy\`, \`EEEE\` (jour),
\`HH:mm\`. Les mois et les jours sont en français ; ajoutez \`'en'\` en troisième argument
de \`dateFormat\` pour l'anglais. \`$now\` est la date du jour.

## Conditions

\`\`\`
<<cs_{options.assistance = true}>>
Texte gardé si l'option est prise.
<<else>>
Texte sinon.
<<es_>>
\`\`\`

La condition peut être un simple champ (\`<<cs_options.assistance>>\` : vrai, non vide, non nul)
ou une expression entre accolades (\`<<cs_{capital > 60000 and age < 70}>>\`).

Pour enchaîner plusieurs cas, \`<<else_…>>\` veut dire « sinon si » :

\`\`\`
<<cs_{capital > 100000}>>
Grand capital.
<<else_{capital > 30000}>>
Capital moyen.
<<else>>
Petit capital.
<<es_>>
\`\`\`

**Lignes de tableau conditionnelles** : \`<<cr_{montant > 0}>>\` dans une ligne et \`<<er_>>\` dans
une ligne plus bas. Les lignes entre les deux ne restent que si la condition est vraie ; les
deux lignes des balises disparaissent toujours. Dans une ligne répétée (\`rr_\`), chaque élément
décide pour lui-même.

**Colonnes conditionnelles** : \`<<cc_{options.agence}>>\` dans la première ligne d'un tableau, dans
la colonne concernée. Si la condition est fausse, la colonne disparaît ; le tableau garde sa
largeur et les autres colonnes s'élargissent.

**Sauts de page** : \`<<pageBreak>>\` commence une nouvelle page à cet endroit ;
\`<<pageBreakNotLast>>\`, dans une répétition, fait de même sauf après le dernier élément.

## Répétitions

- **Lignes de tableau** : \`<<rr_garanties>>\` dans la première cellule et \`<<er_>>\` dans la
  dernière ; la ligne est répétée pour chaque élément de \`garanties\`.
- **Paragraphes ou blocs** : \`<<rs_beneficiaires>>\` … \`<<es_>>\` répète tout ce qui est entre
  les deux (paragraphes, tableaux, sauts de page).
- Dans une répétition, les champs se lisent depuis l'élément (\`<<nom>>\`) ; \`<<$itemNum>>\` donne
  son numéro (1, 2, 3…), \`<<$itemIdx>>\` sa position depuis 0.

## Cases à cocher

\`<<options.cg{renderer=boolean}>>\` écrit ☑ si la valeur est vraie, ☐ sinon ;
\`<<options.cg{renderer=yesno}>>\` écrit Oui ou Non.

## Images

Une image du modèle porte un signet \`imgfit_logo\` : elle est remplacée par l'image
de \`logo\` dans les données, à la taille du cadre. La valeur est une image en base64
(\`data:image/png;base64,…\`) ou son **adresse** (\`https://…\`) : les adresses ne sont lues
que sur les domaines autorisés par l'administrateur (Configuration, Documents produits),
et gardées un moment en mémoire.

## Codes QR et codes-barres

| Balise | Résultat |
|---|---|
| \`<<{qrcode(contrat.lien)}>>\` | un code QR de 3 cm |
| \`<<{qrcode(contrat.lien, 2)}>>\` | un code QR de 2 cm |
| \`<<{barcode(colis.numero, 'code128')}>>\` | un code-barres Code 128 |
| \`<<{barcode(produit.ean, 'ean13', 4, 1.5)}>>\` | un EAN-13 de 4 × 1,5 cm |

Types : \`code128\`, \`code39\`, \`ean13\`, \`ean8\`, \`itf\`, \`codabar\`, \`datamatrix\`,
\`pdf417\`, \`aztec\`, \`qr\`.

## Vérificateur

À chaque nouvelle version, DocIbis vérifie le modèle avant toute donnée : balises
mal fermées ou coupées par Word, sections \`cs_\`/\`rs_\` sans \`es_\`, fonctions inconnues,
polices absentes du serveur (remplacées, donc mise en page différente), révisions et
commentaires Word oubliés. Le résultat s'affiche dans la bibliothèque et sur la page Essai.

## Liasses

Une liasse (page [Liasses](/liasses)) produit plusieurs documents en un appel : des modèles
et des PDF fixes, dans l'ordre, chacun sous une condition sur les données. Le métier change
la composition sans toucher au code des applications :

\`\`\`sh
curl -H "Authorization: Bearer $DOCIBIS_KEY" -H "Content-Type: application/json" \\
  -d '{"data": {"contrat": {"numero": "HAB-2026-001"}, "options": {"assistance": true}}}' \\
  -o souscription.pdf ${origin}/v1/bundles/souscription-habitation/render
\`\`\`

## D'un environnement à l'autre

Sur la page d'un modèle, **Envoyer à** pousse une version (avec sa référence) vers REC ou
PROD, et la publie si on le demande. L'administrateur déclare les environnements dans
Configuration, Environnements, avec une clé de l'autre DocIbis créée avec « recevoir des
modèles ». Chaque envoi est gardé dans l'historique du modèle et dans le journal d'audit
des deux côtés.
`
}

export const HELP_PAGES: HelpPage[] = [
  { slug: 'demarrer', title: 'Bien démarrer', summary: 'Rôles, essai d’un modèle, suivi de la production.', icon: BookOpen, colour: 'text-teal-500', content: () => start },
  { slug: 'syntaxe', title: 'Écrire un modèle', summary: 'Champs, formats, conditions, répétitions, images, codes QR, liasses.', icon: PenLine, colour: 'text-amber-500', content: syntax },
  { slug: 'docmosis', title: 'Passer de Docmosis à DocIbis', summary: 'Bibliothèque de modèles, route /api/render, références, options.', icon: FileStack, colour: 'text-blue-600', content: docmosis },
  { slug: 'api', title: "Utiliser l'API", summary: 'Clé, appel de /v1/render, exemples en 7 langages, erreurs, rendu en arrière-plan.', icon: Code2, colour: 'text-violet-500', content: api },
  { slug: 'mcp', title: 'Serveur MCP', summary: 'Brancher Claude, VS Code ou Cursor sur DocIbis.', icon: Bot, colour: 'text-indigo-500', content: mcp },
]
