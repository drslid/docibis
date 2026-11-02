---
title: Day-to-day operations
description: Follow, update, back up and remove a DocIbis server.
---

## Follow the service

**Monitoring** shows the processor and memory, render times and the LibreOffice workers. Alert rules send an e-mail or a Teams message when a value crosses a threshold.

![The Monitoring page](../../../assets/screenshots/monitoring.png)

**Produced documents** lists every document: template, application, duration and result. Click a row to see an error and produce the document again. The document itself is never kept.

![The history of produced documents](../../../assets/screenshots/documents.png)

The **Audit log** records every change made in the console: who, what and when.

## Commands

```bash
cd /opt/docibis
docker compose ps             # state of the containers
docker compose logs -f api    # logs
docker compose restart api    # restart, for example after adding fonts
docker compose down           # stop; the data is kept
```

## Update

Run the installer again with the new release. The configuration (`.env`) and the data are kept.

```bash
curl -fsSL https://raw.githubusercontent.com/drslid/docibis/main/install.sh | sudo env DOCIBIS_REF=v0.2.0 bash
```

**What's new**, in the console, lists the changes of each version.

## Back up

- **SQLite:** copy the `docibis_data` Docker volume, or set `STATE_URL` for a continuous copy to S3, R2 or MinIO.
- **PostgreSQL:** use `pg_dump`.
- In both cases, keep `/opt/docibis/.env` with the backup: its `APP_SECRET_KEY` is needed to read the second-factor secrets.

## Remove

```bash
cd /opt/docibis && docker compose down -v   # removes the containers AND the data
sudo rm -rf /opt/docibis
```
