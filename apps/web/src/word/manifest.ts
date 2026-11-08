// The manifest of the Word add-in, made for the instance that serves it: Word
// loads the pane and its icons from this address.

const esc = (s: string) => s.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/"/g, '&quot;')

/** A stable identifier per instance, so that INT, REC and PROD install side by side. */
export async function addinId(origin: string): Promise<string> {
  const hash = new Uint8Array(await crypto.subtle.digest('SHA-256', new TextEncoder().encode(`docibis-word ${origin}`)))
  const b = hash.slice(0, 16)
  b[6] = (b[6] & 0x0f) | 0x50
  b[8] = (b[8] & 0x3f) | 0x80
  const hex = Array.from(b, (x) => x.toString(16).padStart(2, '0')).join('')
  return `${hex.slice(0, 8)}-${hex.slice(8, 12)}-${hex.slice(12, 16)}-${hex.slice(16, 20)}-${hex.slice(20)}`
}

export function wordManifest(origin: string, name: string, id: string): string {
  const url = (path: string) => esc(origin + path)
  const icons = (indent: string) =>
    ['16', '32', '80'].map((size) => `${indent}<bt:Image size="${size}" resid="Icon.${size}"/>`).join('\n')
  return `<?xml version="1.0" encoding="UTF-8"?>
<OfficeApp xmlns="http://schemas.microsoft.com/office/appforoffice/1.1" xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance" xmlns:bt="http://schemas.microsoft.com/office/officeappbasictypes/1.0" xmlns:ov="http://schemas.microsoft.com/office/taskpaneappversionoverrides" xsi:type="TaskPaneApp">
  <Id>${id}</Id>
  <Version>1.0.0.0</Version>
  <ProviderName>DocIbis</ProviderName>
  <DefaultLocale>fr-FR</DefaultLocale>
  <DisplayName DefaultValue="${esc(name)}"/>
  <Description DefaultValue="Balises Docmosis, v\u00e9rification et aper\u00e7u PDF des mod\u00e8les DocIbis."/>
  <IconUrl DefaultValue="${url('/word/icon-32.png')}"/>
  <HighResolutionIconUrl DefaultValue="${url('/word/icon-64.png')}"/>
  <SupportUrl DefaultValue="${url('/aide/syntaxe')}"/>
  <AppDomains>
    <AppDomain>${esc(origin)}</AppDomain>
  </AppDomains>
  <Hosts>
    <Host Name="Document"/>
  </Hosts>
  <DefaultSettings>
    <SourceLocation DefaultValue="${url('/word/')}"/>
  </DefaultSettings>
  <Permissions>ReadWriteDocument</Permissions>
  <VersionOverrides xmlns="http://schemas.microsoft.com/office/taskpaneappversionoverrides" xsi:type="VersionOverridesV1_0">
    <Hosts>
      <Host xsi:type="Document">
        <DesktopFormFactor>
          <GetStarted>
            <Title resid="GetStarted.Title"/>
            <Description resid="GetStarted.Description"/>
            <LearnMoreUrl resid="Help.Url"/>
          </GetStarted>
          <ExtensionPoint xsi:type="PrimaryCommandSurface">
            <OfficeTab id="TabHome">
              <Group id="DocIbis.Group">
                <Label resid="Button.Label"/>
                <Icon>
${icons('                  ')}
                </Icon>
                <Control xsi:type="Button" id="DocIbis.Open">
                  <Label resid="Button.Label"/>
                  <Supertip>
                    <Title resid="Button.Label"/>
                    <Description resid="Button.Tooltip"/>
                  </Supertip>
                  <Icon>
${icons('                    ')}
                  </Icon>
                  <Action xsi:type="ShowTaskpane">
                    <TaskpaneId>DocIbis</TaskpaneId>
                    <SourceLocation resid="Taskpane.Url"/>
                  </Action>
                </Control>
              </Group>
            </OfficeTab>
          </ExtensionPoint>
        </DesktopFormFactor>
      </Host>
    </Hosts>
    <Resources>
      <bt:Images>
${['16', '32', '80'].map((size) => `        <bt:Image id="Icon.${size}" DefaultValue="${url(`/word/icon-${size}.png`)}"/>`).join('\n')}
      </bt:Images>
      <bt:Urls>
        <bt:Url id="Taskpane.Url" DefaultValue="${url('/word/')}"/>
        <bt:Url id="Help.Url" DefaultValue="${url('/aide/syntaxe')}"/>
      </bt:Urls>
      <bt:ShortStrings>
        <bt:String id="GetStarted.Title" DefaultValue="${esc(name)} est pr\u00eat"/>
        <bt:String id="Button.Label" DefaultValue="DocIbis"/>
      </bt:ShortStrings>
      <bt:LongStrings>
        <bt:String id="GetStarted.Description" DefaultValue="Ouvrez le volet DocIbis depuis l'onglet Accueil."/>
        <bt:String id="Button.Tooltip" DefaultValue="Ins\u00e9rer des balises, v\u00e9rifier le mod\u00e8le et voir son PDF avec des donn\u00e9es."/>
      </bt:LongStrings>
    </Resources>
  </VersionOverrides>
</OfficeApp>
`
}
