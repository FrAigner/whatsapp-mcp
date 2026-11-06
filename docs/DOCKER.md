# Betrieb auf dem Heimserver (ohne Mac)

Bridge und MCP-Server laufen als zwei Container (`docker-compose.yml`).
Claude Code auf demselben Server spricht den MCP-Server über `127.0.0.1:8000` an.
Der Port ist nicht im Netz erreichbar.

## Einrichten

1. Telegram-Bot über @BotFather anlegen; die eigene Chat-ID ermitteln.
2. Neben `docker-compose.yml` eine Datei `.env` (Rechte `600`) anlegen:

       TELEGRAM_BOT_TOKEN=...
       TELEGRAM_OWNER_CHAT_ID=...

3. Starten und WhatsApp einmalig koppeln (QR-Code erscheint im Log):

       docker compose up -d --build
       docker compose logs -f bridge

4. In Claude Code den MCP-Server eintragen (Pfad `/mcp` nach dem Start prüfen):

       claude mcp add --transport http whatsapp http://127.0.0.1:8000/mcp

Auf einem Raspberry Pi (arm64) baut `--build` direkt auf dem Gerät; der erste
Go-Build dauert einige Minuten.

## Wichtig: Der Agent darf die Freigabeliste nicht erreichen

Die Freigabe schützt nur, solange der Agent `allowlist.json` nicht ändern und
nicht auf den Bot zugreifen kann. Läuft Claude Code auf demselben Server:

- Claude Code unter einem **eigenen Benutzer** ohne `sudo` und **ohne Mitgliedschaft
  in der Gruppe `docker`** betreiben (Docker-Zugriff entspricht Root-Zugriff und
  erlaubt, das Volume zu lesen und zu ändern).
- Die Compose-Dateien und `.env` gehören einem anderen Benutzer (`chmod 600 .env`).
- Der MCP-Container mountet das Volume nur lesend; geschrieben wird nur von der Bridge.
- Kein Zugriff des Agenten auf das Verzeichnis `/var/lib/docker`.

Ohne diese Trennung kann ein manipulierter Agent sich selbst freigeben.
