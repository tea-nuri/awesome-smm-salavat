"""Telegram transport: long polling + durable Go outbox. Secrets stay server-side."""

import json, logging, os, secrets, signal, threading
import urllib.request, urllib.error
from pathlib import Path

TOKEN = os.environ["TELEGRAM_BOT_TOKEN"]
INTERNAL = os.environ["INTERNAL_TOKEN"]
BACKEND = os.getenv("BACKEND_URL", "http://127.0.0.1:8080").rstrip("/")
PUBLIC = os.getenv("PUBLIC_URL", "").rstrip("/")
DATA = Path(os.getenv("BOT_DATA_DIR", "data/bot"))
DATA.mkdir(parents=True, exist_ok=True)
STOP = threading.Event()
STATE_LOCK = threading.Lock()
try:
    STATE = json.loads((DATA / "state.json").read_text())
except FileNotFoundError:
    STATE = {"offset": 0, "revisions": {}}
logging.basicConfig(level=logging.INFO, format="%(asctime)s %(levelname)s %(message)s")


def save():
    with STATE_LOCK:
        p = DATA / "state.tmp"
        p.write_text(json.dumps(STATE))
        p.chmod(0o600)
        p.replace(DATA / "state.json")


class TransportError(RuntimeError):
    """Delivery outcome is unknown; never reinterpret it as a rejected action."""


def request(url, data=None, headers=None, timeout=40):
    req = urllib.request.Request(url, data=data, headers=headers or {})
    try:
        with urllib.request.urlopen(req, timeout=timeout) as r:
            return json.load(r)
    except urllib.error.HTTPError as e:
        try:
            body = json.loads(e.read())
            msg = body.get("error") or body.get("description") or f"HTTP {e.code}"
        except Exception:
            msg = f"HTTP {e.code}"
        raise RuntimeError(msg) from None
    except Exception as e:
        raise TransportError("Временный сбой соединения") from None


def tg(method, data=None):
    result = request(
        "https://api.telegram.org/bot" + TOKEN + "/" + method,
        json.dumps(data or {}).encode(),
        {"Content-Type": "application/json"},
        timeout=35 if method == "getUpdates" else 12,
    )
    if not result.get("ok"):
        raise RuntimeError(result.get("description", "Telegram error"))
    return result["result"]


def api(path, data=None):
    # Only retry operations with a deduplication key or without mutations.
    retriable = (
        path in {"revision-check", "publication-check", "config"}
        or (path in {"action", "message"} and bool((data or {}).get("update_id")))
        or path.startswith("outbox/")
    )
    attempts = 3 if retriable else 1
    for attempt in range(attempts):
        try:
            return request(
                BACKEND + "/internal/" + path,
                None if data is None else json.dumps(data).encode(),
                {
                    "Content-Type": "application/json",
                    "Authorization": "Bearer " + INTERNAL,
                },
            )
        except TransportError:
            if attempt == attempts - 1:
                raise
            STOP.wait(attempt + 1)


def acknowledge(chat, text, markup=None):
    try:
        return send(chat, text, markup)
    except Exception as exc:
        # The backend has already accepted the action. An acknowledgement delivery
        # failure must not be reported to the user as an action failure.
        logging.warning("Acknowledgement delivery delayed: %s", type(exc).__name__)


def answer_callback(query_id, text):
    try:
        return tg("answerCallbackQuery", {"callback_query_id": query_id, "text": text})
    except Exception as exc:
        logging.warning("Callback acknowledgement: %s", type(exc).__name__)


def send(chat, text, markup=None):
    data = {"chat_id": chat, "text": text[:4096]}
    if markup:
        data["reply_markup"] = markup
    return tg("sendMessage", data)


def photo(chat, name, caption, markup=None):
    req = urllib.request.Request(
        BACKEND + "/internal/media/" + name,
        headers={"Authorization": "Bearer " + INTERNAL},
    )
    with urllib.request.urlopen(req, timeout=20) as r:
        content = r.read()
    boundary = "awesome" + secrets.token_hex(12)
    parts = []
    fields = {"chat_id": str(chat), "caption": caption[:1024]}
    if markup:
        fields["reply_markup"] = json.dumps(markup)
    for key, value in fields.items():
        parts.extend(
            [
                f'--{boundary}\r\nContent-Disposition: form-data; name="{key}"\r\n\r\n'.encode(),
                value.encode(),
                b"\r\n",
            ]
        )
    parts.extend(
        [
            f'--{boundary}\r\nContent-Disposition: form-data; name="photo"; filename="creative.png"\r\nContent-Type: image/png\r\n\r\n'.encode(),
            content,
            b"\r\n",
            f"--{boundary}--\r\n".encode(),
        ]
    )
    res = request(
        "https://api.telegram.org/bot" + TOKEN + "/sendPhoto",
        b"".join(parts),
        {"Content-Type": "multipart/form-data; boundary=" + boundary},
        timeout=60,
    )
    if not res.get("ok"):
        raise RuntimeError(res.get("description", "Telegram error"))
    return res["result"]


def handle(update):
    if "callback_query" in update:
        q = update["callback_query"]
        uid = q["from"]["id"]
        cid = q["message"]["chat"]["id"]
        data = q.get("data", "")
        if data.startswith("co:"):
            result = api(
                "message",
                {
                    "user_id": uid,
                    "chat_id": cid,
                    "text": "/company " + data[3:],
                    "update_id": update["update_id"],
                },
            )
            answer_callback(q["id"], "Компания выбрана")
            acknowledge(cid, result["reply"])
            return
        pieces = data.split(":")
        if len(pieces) != 3:
            return
        action, pid, rev = pieces
        rev = int(rev)
        if action == "revise":
            # Validate current membership, status and revision via a non-mutating validation endpoint.
            check = api(
                "revision-check", {"user_id": uid, "post_id": pid, "revision": rev}
            )
            if not check.get("ok"):
                raise RuntimeError("Материал уже изменён или недоступен")
            STATE["revisions"][str(uid)] = {"post_id": pid, "revision": rev}
            save()
            answer_callback(q["id"], "Напишите комментарий")
            acknowledge(
                cid,
                "Что изменить в материале? Пришлите следующим сообщением. /cancel — отменить.",
            )
            return
        api(
            "action",
            {
                "user_id": uid,
                "post_id": pid,
                "revision": rev,
                "action": action,
                "update_id": update["update_id"],
            },
        )
        answer_callback(
            q["id"],
            "Одобрено, отправляю в канал"
            if action == "approve"
            else "Материал отклонён",
        )
        try:
            tg(
                "editMessageReplyMarkup",
                {
                    "chat_id": cid,
                    "message_id": q["message"]["message_id"],
                    "reply_markup": {"inline_keyboard": []},
                },
            )
        except RuntimeError:
            pass
        return
    m = update.get("message", {})
    uid = m.get("from", {}).get("id")
    cid = m.get("chat", {}).get("id")
    text = m.get("text", "")
    if not uid or m.get("chat", {}).get("type") != "private":
        return
    if text == "/cancel":
        STATE["revisions"].pop(str(uid), None)
        save()
        acknowledge(cid, "Доработка отменена.")
        return
    if str(uid) in STATE["revisions"] and text and not text.startswith("/"):
        pending = STATE["revisions"][str(uid)]
        api(
            "action",
            {
                "user_id": uid,
                **pending,
                "action": "revise",
                "comment": text,
                "update_id": update["update_id"],
            },
        )
        STATE["revisions"].pop(str(uid), None)
        save()
        acknowledge(
            cid,
            "Принял правки. Подготовлю новую версию и снова пришлю на согласование.",
        )
        return
    if m.get("document"):
        doc = m["document"]
        name = doc.get("file_name", "")
        if doc.get("file_size", 0) > 5 * 1024 * 1024:
            acknowledge(cid, "Для MVP загрузите файл до 5 МБ.")
            return
        f = tg("getFile", {"file_id": doc["file_id"]})
        with urllib.request.urlopen(
            "https://api.telegram.org/file/bot" + TOKEN + "/" + f["file_path"],
            timeout=30,
        ) as r:
            content = r.read(5 * 1024 * 1024 + 1)
        import io

        if name.lower().endswith(".pdf"):
            from pypdf import PdfReader

            reader = PdfReader(io.BytesIO(content))
            text = "\n".join(page.extract_text() or "" for page in reader.pages[:40])
        elif name.lower().endswith((".txt", ".md")):
            text = content.decode("utf-8-sig", errors="replace")
        else:
            acknowledge(cid, "Пришлите PDF, TXT или MD.")
            return
        if not text.strip():
            acknowledge(
                cid, "Не удалось извлечь текст. Пришлите текстовый PDF или TXT."
            )
            return
        api("knowledge", {"user_id": uid, "text": text[:45000], "filename": name})
        acknowledge(
            cid, "Материал «" + name + "» добавлен в базу знаний выбранной компании."
        )
        return
    if not text:
        return
    result = api(
        "message",
        {
            "user_id": uid,
            "chat_id": cid,
            "text": text,
            "update_id": update["update_id"],
        },
    )
    markup = None
    if result.get("companies"):
        markup = {
            "inline_keyboard": [
                [{"text": c["name"], "callback_data": "co:" + c["id"]}]
                for c in result["companies"]
            ]
        }
    if result.get("reply"):
        acknowledge(cid, result["reply"], markup)


def updates_loop():
    while not STOP.is_set():
        try:
            updates = tg(
                "getUpdates",
                {
                    "offset": STATE["offset"],
                    "timeout": 25,
                    "allowed_updates": ["message", "callback_query"],
                },
            )
            for u in updates:
                if STOP.is_set():
                    break
                try:
                    handle(u)
                except Exception as e:
                    logging.warning("Update %s: %s", u.get("update_id"), str(e)[:160])
                    m = u.get("message") or u.get("callback_query", {}).get(
                        "message", {}
                    )
                    cid = m.get("chat", {}).get("id")
                    if cid:
                        try:
                            send(cid, "Не удалось выполнить действие: " + str(e)[:250])
                        except Exception:
                            pass
                STATE["offset"] = u["update_id"] + 1
                save()
        except Exception as e:
            logging.warning("Polling: %s", str(e)[:150])
            STOP.wait(4)


def outbox_loop():
    while not STOP.is_set():
        try:
            data = api("outbox")
            event = data.get("event")
            if not event:
                STOP.wait(1)
                continue
            outcome = {"ok": False}
            try:
                kind = event["kind"]
                chat = event["chat_id"]
                post = data.get("post", {})
                company = data.get("company", {})
                if kind == "message":
                    send(chat, event["text"])
                elif kind == "preview":
                    if post.get("status") != "review":
                        api("outbox/" + event["id"], {"ok": True})
                        continue
                    rev = post["revision"]
                    pid = post["id"]
                    buttons = {
                        "inline_keyboard": [
                            [
                                {
                                    "text": "✓ Одобрить",
                                    "callback_data": f"approve:{pid}:{rev}",
                                },
                                {
                                    "text": "↻ Доработать",
                                    "callback_data": f"revise:{pid}:{rev}",
                                },
                            ],
                            [
                                {
                                    "text": "Отклонить",
                                    "callback_data": f"reject:{pid}:{rev}",
                                }
                            ],
                        ]
                    }
                    photo(chat, post["image"], post["body"])
                    stamp = post.get("scheduled_at") or "после одобрения"
                    send(
                        chat,
                        f"На согласовании · версия {rev}\n{company['name']} → {company.get('channel') or 'канал не подключён'}\nВремя публикации: {stamp}\n\n{post['title']}\n\nПубликация только после вашего одобрения.",
                        buttons,
                    )
                elif kind == "publish":
                    if not api("publication-check", {"post_id": post["id"]}).get("ok"):
                        api("outbox/" + event["id"], {"ok": True})
                        continue
                    msg = photo(company["channel"], post["image"], post["body"])
                    chat_info = msg["chat"]
                    username = chat_info.get("username")
                    link = (
                        "https://t.me/" + username + "/" + str(msg["message_id"])
                        if username
                        else "https://t.me/c/"
                        + str(chat_info["id"]).removeprefix("-100")
                        + "/"
                        + str(msg["message_id"])
                    )
                    outcome.update(message_id=msg["message_id"], url=link)
                outcome["ok"] = True
            except Exception as e:
                outcome["error"] = str(e)[:250]
                logging.warning("Outbox %s: %s", event["id"], str(e)[:150])
            # Never resend a publication when acknowledgement is uncertain.
            for attempt in range(5):
                try:
                    api("outbox/" + event["id"], outcome)
                    break
                except Exception:
                    STOP.wait(2)
        except Exception as e:
            logging.warning("Outbox: %s", str(e)[:150])
            STOP.wait(3)


def main():
    me = tg("getMe")
    logging.info("Starting @%s", me["username"])
    info = tg("getWebhookInfo")
    if info.get("url"):
        raise RuntimeError(
            "Webhook already configured; refusing to replace it automatically"
        )
    tg(
        "setMyCommands",
        {
            "commands": [
                {"command": "start", "description": "Начать работу"},
                {"command": "companies", "description": "Выбрать компанию"},
                {"command": "status", "description": "Последние задачи"},
                {"command": "help", "description": "Как пользоваться"},
            ]
        },
    )
    if PUBLIC:
        tg(
            "setChatMenuButton",
            {
                "menu_button": {
                    "type": "web_app",
                    "text": "Открыть",
                    "web_app": {"url": PUBLIC},
                }
            },
        )
    for sig in (signal.SIGINT, signal.SIGTERM):
        signal.signal(sig, lambda *_: STOP.set())
    threading.Thread(target=outbox_loop, daemon=True).start()
    updates_loop()


if __name__ == "__main__":
    main()
