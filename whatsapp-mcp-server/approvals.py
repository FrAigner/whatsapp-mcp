"""Contact approval: hide messages from senders the user has not approved.

Why: an incoming WhatsApp message is untrusted text. If an LLM reads it, it may
follow instructions hidden in it (prompt injection). So every read tool only
sees messages from senders on a local allowlist. Approval happens out of band
(Telegram bot or local CLI, never an MCP tool), so the agent cannot approve.

How: the read-only connection gets TEMP VIEWs named ``messages`` and ``chats``.
SQLite resolves unqualified names in the temp schema first, so every existing
query is filtered without being rewritten, and future queries are too.

Fail closed: a missing, unreadable or corrupt allowlist approves nobody.
"""

import json
import os
import sqlite3
from pathlib import Path


def enabled() -> bool:
    return os.getenv("WHATSAPP_REQUIRE_APPROVAL", "true").strip().lower() not in {"0", "false", "no", "off"}


def allowlist_path(messages_db_path: str) -> Path:
    configured = os.getenv("WHATSAPP_ALLOWLIST_PATH", "").strip()
    if configured:
        return Path(configured)
    return Path(messages_db_path).resolve().parent / "allowlist.json"


def normalize(identifier: str) -> str:
    """Bare id of a JID: '49123:7@s.whatsapp.net' -> '49123'."""
    return str(identifier).split("@", 1)[0].split(":", 1)[0].strip()


def load_approved(messages_db_path: str, expand=None) -> frozenset[str]:
    """Approved bare ids; empty on any problem. `expand` adds phone<->LID aliases."""
    try:
        data = json.loads(allowlist_path(messages_db_path).read_text())
        entries = data["approved"]
        if not isinstance(entries, list):
            return frozenset()
        approved = {normalize(e) for e in entries if isinstance(e, str) and e.strip()}
    except (OSError, ValueError, KeyError, TypeError):
        return frozenset()
    if expand:
        for ident in list(approved):
            approved.update(normalize(a) for a in expand(ident))
    return frozenset(approved)


def _is_group(jid: str) -> bool:
    return jid.endswith("@g.us")


def _is_direct(jid: str) -> bool:
    return jid.endswith("@s.whatsapp.net") or jid.endswith("@lid")


def make_visibility(approved: frozenset[str]):
    def visible(chat_jid, sender, is_from_me) -> int:
        chat_jid = chat_jid or ""
        if _is_direct(chat_jid):
            # Own messages in a chat are only shown once the contact is approved.
            return int(normalize(chat_jid) in approved)
        if _is_group(chat_jid):
            if is_from_me:
                return 1
            return int(normalize(sender or "") in approved)
        return 0  # status broadcasts, newsletters, unknown chat types

    return visible


def install_views(conn: sqlite3.Connection, messages_db_path: str, expand=None) -> None:
    """Shadow `messages` and `chats` with filtered temp views. No-op if disabled."""
    if not enabled():
        return
    approved = load_approved(messages_db_path, expand)
    visible = make_visibility(approved)
    conn.create_function("wa_visible", 3, visible, deterministic=True)
    conn.execute(
        "CREATE TEMP VIEW messages AS SELECT * FROM main.messages WHERE wa_visible(chat_jid, sender, is_from_me) = 1"
    )
    # Direct chats only when approved; groups only when an approved participant has spoken.
    conn.execute(
        "CREATE TEMP VIEW chats AS SELECT * FROM main.chats c WHERE "
        "(c.jid LIKE '%@s.whatsapp.net' OR c.jid LIKE '%@lid') AND wa_visible(c.jid, '', 0) = 1 "
        "OR c.jid LIKE '%@g.us' AND EXISTS (SELECT 1 FROM temp.messages m WHERE m.chat_jid = c.jid)"
    )
