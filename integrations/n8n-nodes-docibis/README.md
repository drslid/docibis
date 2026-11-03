# n8n-nodes-docibis

An [n8n](https://n8n.io) community node for [DocIbis](https://github.com/drslid/docibis): PDF and Word documents from Word templates (Docmosis tags) and JSON data.

## Operations

| Operation | What it does |
|---|---|
| Render | Produces the document of a template of the library now; the file comes out as binary data, with the time, the engine and the missing fields. |
| Render in Background | Answers at once with a job (`POST /v1/render/async`); with an Upload URL (a presigned S3 address) DocIbis puts the document there, with a Callback URL it calls back when it ends. |
| Get Job | The state of a job: queued, running, done or failed. |
| Download Job Document | The document of a finished job, as binary data. |
| List Templates | The published templates and the data each one expects. |

Options: output name, PDF/A, accessible PDF (PDF/UA), title, watermark, data check.

## Credentials

A DocIbis API key (`dib_…`, page **API keys** of the console) and the address of the instance. Callback and upload addresses must be allowed by an administrator (**Configuration › Background rendering**).

## Install

In n8n: **Settings › Community nodes › Install**, then `n8n-nodes-docibis`. Or build it:

```bash
npm install && npm run build
```
