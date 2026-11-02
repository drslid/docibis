// Calls of /v1/render, one per language, ready to run with the example
// template and data served by the console.

export interface Example {
  id: string
  label: string
  code: string
}

export function examples(origin: string): Example[] {
  const url = `${origin}/v1/render`
  return [
    {
      id: 'curl',
      label: 'curl',
      code: `# La clé créée dans « Clés API »
export DOCIBIS_API_KEY="dib_..."

# Le modèle et les données d'exemple
curl -sS -O ${origin}/exemple-docibis.docx -O ${origin}/exemple-docibis.json

# Le PDF (si le code affiché n'est pas 200, document.pdf contient l'erreur en JSON)
curl -sS -w 'HTTP %{http_code} en %{time_total} s\\n' \\
  -H "Authorization: Bearer $DOCIBIS_API_KEY" \\
  -F template=@exemple-docibis.docx \\
  -F data=@exemple-docibis.json \\
  -o document.pdf \\
  ${url}

# Le même document en Word : -F format=docx -o document.docx
# Des données sans fichier : -F 'data={"client": {"prenom": "Marie"}}'`,
    },
    {
      id: 'python',
      label: 'Python',
      code: `import os

import requests  # pip install requests

with open("exemple-docibis.docx", "rb") as template, open("exemple-docibis.json", encoding="utf-8") as data:
    response = requests.post(
        "${url}",
        headers={"Authorization": f"Bearer {os.environ['DOCIBIS_API_KEY']}"},
        files={"template": ("exemple-docibis.docx", template)},
        data={"data": data.read()},  # ou json.dumps(un_dictionnaire)
        timeout=60,
    )

if not response.ok:
    raise RuntimeError(response.json()["detail"])
with open("document.pdf", "wb") as pdf:
    pdf.write(response.content)
print("Produit en", response.headers["X-Render-Ms"], "ms")`,
    },
    {
      id: 'javascript',
      label: 'JavaScript',
      code: `// Node.js 18 ou plus (fetch, FormData et Blob sont inclus)
import { readFile, writeFile } from 'node:fs/promises'

const form = new FormData()
form.append('template', new Blob([await readFile('exemple-docibis.docx')]), 'exemple-docibis.docx')
form.append('data', await readFile('exemple-docibis.json', 'utf8')) // ou JSON.stringify(objet)

const response = await fetch('${url}', {
  method: 'POST',
  headers: { Authorization: \`Bearer \${process.env.DOCIBIS_API_KEY}\` },
  body: form,
})
if (!response.ok) throw new Error((await response.json()).detail)

await writeFile('document.pdf', Buffer.from(await response.arrayBuffer()))
console.log('Produit en', response.headers.get('X-Render-Ms'), 'ms')`,
    },
    {
      id: 'java',
      label: 'Java',
      code: `// Java 11 ou plus, sans bibliothèque
import java.io.ByteArrayOutputStream;
import java.net.URI;
import java.net.http.HttpClient;
import java.net.http.HttpRequest;
import java.net.http.HttpResponse;
import java.nio.charset.StandardCharsets;
import java.nio.file.Files;
import java.nio.file.Path;
import java.util.UUID;

public class Document {
    public static void main(String[] args) throws Exception {
        var boundary = UUID.randomUUID().toString();
        var body = new ByteArrayOutputStream();
        body.write(("--" + boundary + "\\r\\nContent-Disposition: form-data; name=\\"template\\"; filename=\\"exemple-docibis.docx\\"\\r\\n\\r\\n").getBytes(StandardCharsets.UTF_8));
        body.write(Files.readAllBytes(Path.of("exemple-docibis.docx")));
        body.write(("\\r\\n--" + boundary + "\\r\\nContent-Disposition: form-data; name=\\"data\\"\\r\\n\\r\\n").getBytes(StandardCharsets.UTF_8));
        body.write(Files.readAllBytes(Path.of("exemple-docibis.json")));
        body.write(("\\r\\n--" + boundary + "--\\r\\n").getBytes(StandardCharsets.UTF_8));

        var request = HttpRequest.newBuilder(URI.create("${url}"))
            .header("Authorization", "Bearer " + System.getenv("DOCIBIS_API_KEY"))
            .header("Content-Type", "multipart/form-data; boundary=" + boundary)
            .POST(HttpRequest.BodyPublishers.ofByteArray(body.toByteArray()))
            .build();
        var response = HttpClient.newHttpClient().send(request, HttpResponse.BodyHandlers.ofByteArray());
        if (response.statusCode() != 200) {
            throw new IllegalStateException(new String(response.body(), StandardCharsets.UTF_8));
        }
        Files.write(Path.of("document.pdf"), response.body());
    }
}`,
    },
    {
      id: 'csharp',
      label: 'C#',
      code: `// .NET 6 ou plus
using System.Net.Http.Headers;

using var http = new HttpClient();
http.DefaultRequestHeaders.Authorization =
    new AuthenticationHeaderValue("Bearer", Environment.GetEnvironmentVariable("DOCIBIS_API_KEY"));

using var form = new MultipartFormDataContent();
form.Add(new ByteArrayContent(await File.ReadAllBytesAsync("exemple-docibis.docx")), "template", "exemple-docibis.docx");
form.Add(new StringContent(await File.ReadAllTextAsync("exemple-docibis.json")), "data");

var response = await http.PostAsync("${url}", form);
if (!response.IsSuccessStatusCode)
    throw new Exception(await response.Content.ReadAsStringAsync());
await File.WriteAllBytesAsync("document.pdf", await response.Content.ReadAsByteArrayAsync());`,
    },
    {
      id: 'php',
      label: 'PHP',
      code: `<?php
$ch = curl_init('${url}');
curl_setopt_array($ch, [
    CURLOPT_POST => true,
    CURLOPT_HTTPHEADER => ['Authorization: Bearer ' . getenv('DOCIBIS_API_KEY')],
    CURLOPT_POSTFIELDS => [
        'template' => new CURLFile('exemple-docibis.docx'),
        'data' => file_get_contents('exemple-docibis.json'), // ou json_encode($tableau)
    ],
    CURLOPT_RETURNTRANSFER => true,
]);
$document = curl_exec($ch);
if (curl_getinfo($ch, CURLINFO_HTTP_CODE) !== 200) {
    throw new RuntimeException($document);
}
file_put_contents('document.pdf', $document);`,
    },
    {
      id: 'go',
      label: 'Go',
      code: `package main

import (
	"bytes"
	"io"
	"mime/multipart"
	"net/http"
	"os"
)

func main() {
	template, _ := os.ReadFile("exemple-docibis.docx")
	data, _ := os.ReadFile("exemple-docibis.json")

	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	part, _ := form.CreateFormFile("template", "exemple-docibis.docx")
	part.Write(template)
	form.WriteField("data", string(data))
	form.Close()

	req, _ := http.NewRequest(http.MethodPost, "${url}", &body)
	req.Header.Set("Authorization", "Bearer "+os.Getenv("DOCIBIS_API_KEY"))
	req.Header.Set("Content-Type", form.FormDataContentType())
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		panic(err)
	}
	defer resp.Body.Close()
	document, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		panic(string(document))
	}
	os.WriteFile("document.pdf", document, 0o644)
}`,
    },
  ]
}
