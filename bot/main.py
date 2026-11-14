"""Telegram transport: long polling + durable Go outbox. Secrets stay server-side."""

import json, logging, os, secrets, signal, threading, time
import re
import urllib.parse
import urllib.request, urllib.error
from pathlib import Path
from datetime import datetime, timedelta, timezone
import social as social_platforms

TOKEN = os.environ["TELEGRAM_BOT_TOKEN"]
INTERNAL = os.environ["INTERNAL_TOKEN"]
BACKEND = os.getenv("BACKEND_URL", "http://127.0.0.1:8080").rstrip("/")
PUBLIC = os.getenv("PUBLIC_URL", "").rstrip("/")
DATA = Path(os.getenv("BOT_DATA_DIR", "data/bot"))
DATA.mkdir(parents=True, exist_ok=True)
STOP = threading.Event()
STATE_LOCK = threading.RLock()


def publication_time(value):
    if not value:
        return "после одобрения"
    moment = datetime.fromisoformat(value.replace("Z", "+00:00"))
    return moment.astimezone(timezone(timedelta(hours=3))).strftime("%d.%m.%Y в %H:%M МСК")


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
        or (path in {"action", "message", "reference"} and bool((data or {}).get("update_id")))
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


def utf16_len(text):
    return len(text.encode("utf-16-le")) // 2


EMOJI_DATA = Path(os.getenv("EMOJI_DATA", str(Path(__file__).resolve().parent.parent / "backend/brand/emoji_sequences.txt")))
_EMOJI_SEQUENCES = [line for line in EMOJI_DATA.read_text().splitlines() if line and not line.startswith("#")]
EMOJI_TOKENS = re.compile("(?:" + "|".join(re.escape(value) for value in sorted(_EMOJI_SEQUENCES, key=len, reverse=True)) + r")[\ufe0e\ufe0f]*|[\U0001f000-\U0001faff\u200d\u20e3\ufe0e\ufe0f\U000e0020-\U000e007f]")


def canonical_emoji(value):
    return value.replace("\ufe0f", "").replace("\ufe0e", "")


def strict_pack_entities(text, company):
    allowed = {canonical_emoji(item["emoji"]): str(item["custom_emoji_id"]) for item in company.get("emoji_items") or [] if item.get("emoji") and str(item.get("custom_emoji_id", "")).isdigit()}
    entities = []
    for match in EMOJI_TOKENS.finditer(text):
        emoji_id = allowed.get(canonical_emoji(match.group()))
        if not emoji_id:
            raise RuntimeError("В тексте есть эмодзи вне выбранного пака. Исправьте текст перед отправкой")
        entities.append({"type": "custom_emoji", "offset": utf16_len(text[:match.start()]), "length": utf16_len(match.group()), "custom_emoji_id": emoji_id})
    return entities


def text_entities(text, post, company):
    """Telegram entity positions use UTF-16, including emoji surrogate pairs."""
    entities = []
    first = text.split("\n", 1)[0]
    if first.strip():
        entities.append({"type": "bold", "offset": 0, "length": utf16_len(first)})
    for phrase in post.get("emphasis") or []:
        if not isinstance(phrase, str) or not phrase or phrase not in text:
            continue
        start = utf16_len(text[:text.index(phrase)])
        length = utf16_len(phrase)
        if not any(e["offset"] < start + length and start < e["offset"] + e["length"] for e in entities):
            entities.append({"type": "bold", "offset": start, "length": length})
    quote = post.get("quote")
    if quote and quote in text and not first.startswith(quote):
        entities.append({"type": "blockquote", "offset": utf16_len(text[:text.index(quote)]), "length": utf16_len(quote)})
    if company.get("emoji_source") == "pack":
        entities.extend(strict_pack_entities(text, company))
        return sorted(entities, key=lambda e: (e["offset"], -e["length"]))
    emoji_map = {}
    source = company.get("emoji_source", "references")
    if source == "pack":
        for item in company.get("emoji_items") or []:
            if item.get("emoji") and str(item.get("custom_emoji_id", "")).isdigit():
                emoji_map[item["emoji"]] = str(item["custom_emoji_id"])
    elif source != "standard":
        for ref in company.get("references") or []:
            raw = ref["text"].encode("utf-16-le")
            for e in ref.get("entities") or []:
                emoji_id = e.get("custom_emoji_id", "")
                if e.get("type") != "custom_emoji" or not emoji_id.isdigit():
                    continue
                emoji = raw[e["offset"] * 2:(e["offset"] + e["length"]) * 2].decode("utf-16-le", errors="ignore")
                if emoji and len(emoji) <= 12:
                    emoji_map[emoji] = emoji_id
    for emoji, emoji_id in list(emoji_map.items()):
        simple = emoji.replace("\ufe0f", "")
        if simple:
            emoji_map.setdefault(simple, emoji_id)
            if not emoji.endswith("\ufe0f"):
                emoji_map.setdefault(emoji + "\ufe0f", emoji_id)
    used = set()
    for emoji in sorted(emoji_map, key=len, reverse=True):
        offset = 0
        while (index := text.find(emoji, offset)) >= 0:
            start = utf16_len(text[:index])
            length = utf16_len(emoji)
            if not any(n in used for n in range(start, start + length)):
                entities.append({"type": "custom_emoji", "offset": start, "length": length, "custom_emoji_id": emoji_map[emoji]})
                used.update(range(start, start + length))
            offset = index + len(emoji)
    return sorted(entities, key=lambda e: (e["offset"], -e["length"]))


def rich_send(chat, text, post, company):
    if company.get("emoji_source") == "pack": strict_pack_entities(text, company)
    # Never split a Unicode surrogate or truncate a long corporate post.
    chunks, current = [], ""
    for char in re.findall(EMOJI_TOKENS.pattern + r"|[\s\S]", text):
        if utf16_len(current + char) > 3900:
            chunks.append(current)
            current = ""
        current += char
    if current:
        chunks.append(current)
    result = None
    for chunk in chunks:
        data = {"chat_id": chat, "text": chunk, "entities": text_entities(chunk, post, company)}
        try:
            result = tg("sendMessage", data)
        except RuntimeError as exc:
            if company.get("emoji_source") == "pack" or isinstance(exc, TransportError) or not emoji_rejected(exc, data["entities"]):
                raise
            data["entities"] = [e for e in data["entities"] if e["type"] != "custom_emoji"]
            logging.warning("Telegram rejected custom emoji for this destination; using standard emoji")
            result = tg("sendMessage", data)
    return result


def emoji_rejected(error, entities):
    message = str(error).lower()
    return any(e["type"] == "custom_emoji" for e in entities) and any(term in message for term in ("custom emoji", "custom_emoji", "premium account", "premium required"))


def photo(chat, name, caption, markup=None, entities=None, strict_emoji=False):
    req = urllib.request.Request(
        BACKEND + "/internal/media/" + name,
        headers={"Authorization": "Bearer " + INTERNAL},
    )
    with urllib.request.urlopen(req, timeout=20) as r:
        content = r.read()
    boundary = "awesome" + secrets.token_hex(12)
    parts = []
    fields = {"chat_id": str(chat), "caption": caption}
    if entities:
        fields["caption_entities"] = json.dumps(entities)
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
    try:
        res = request(
            "https://api.telegram.org/bot" + TOKEN + "/sendPhoto",
            b"".join(parts),
            {"Content-Type": "multipart/form-data; boundary=" + boundary},
            timeout=60,
        )
    except RuntimeError as exc:
        # A positive Telegram rejection is safe to retry. An unknown network
        # outcome is never retried here because the photo may already exist.
        if strict_emoji or isinstance(exc, TransportError) or not emoji_rejected(exc, entities or []):
            raise
        logging.warning("Telegram rejected custom emoji for this destination; using standard emoji")
        return photo(chat, name, caption, markup, [e for e in entities if e["type"] != "custom_emoji"])
    if not res.get("ok"):
        raise RuntimeError(res.get("description", "Telegram error"))
    return res["result"]


def deliver_post(chat, post, company):
    body = post["body"]
    entities = text_entities(body, post, company)  # Validate the whole post before any delivery.
    strict = company.get("emoji_source") == "pack"
    if utf16_len(body) <= 1000:
        return photo(chat, post["image"], body, entities=entities, strict_emoji=strict)
    hook = body.split("\n", 1)[0]
    photo(chat, post["image"], hook, entities=text_entities(hook, post, company), strict_emoji=strict)
    return rich_send(chat, body, post, company)


def receive_photo(message, update_id):
    uid, chat = message["from"]["id"], message["chat"]["id"]
    key = str(uid)
    with STATE_LOCK:
        drafts = STATE.setdefault("media_drafts", {})
        draft = drafts.setdefault(key, {"photos": [], "chat_id": chat, "user_id": uid, "aspect_ratio": STATE.get("image_formats", {}).get(key, "1:1")})
        if len(draft["photos"]) >= 6:
            acknowledge(chat, "К задаче можно приложить до 6 фотографий. Отправьте описание задачи или /cancel.")
            return
        if update_id in draft.get("updates", []):
            return
        draft["uploading"] = True
        draft["updated_at"] = time.time()
        company_id = draft.get("company_id", "")
        save()
    try:
        photo = message["photo"][-1] if message.get("photo") else message["document"]
        if photo.get("file_size", 0) > 12 * 1024 * 1024:
            raise RuntimeError("Выберите JPG или PNG до 12 МБ")
        file = tg("getFile", {"file_id": photo["file_id"]})
        with urllib.request.urlopen("https://api.telegram.org/file/bot" + TOKEN + "/" + file["file_path"], timeout=35) as response:
            content = response.read(12 * 1024 * 1024 + 1)
        if len(content) > 12 * 1024 * 1024:
            raise RuntimeError("Выберите JPG или PNG до 12 МБ")
        query = urllib.parse.urlencode({"user_id": uid, "company_id": company_id})
        asset = request(BACKEND + "/internal/assets?" + query, content, {"Authorization": "Bearer " + INTERNAL, "Content-Type": "application/octet-stream", "X-File-Name": "telegram-photo.jpg"})
        with STATE_LOCK:
            draft = STATE.setdefault("media_drafts", {}).get(key)
            if draft is None:
                return
            first = not draft["photos"]
            draft["photos"].append(asset["id"])
            draft["company_id"] = asset["company_id"]
            draft.setdefault("updates", []).append(update_id)
            draft["update_id"] = update_id
            draft["updated_at"] = time.time()
            draft["uploading"] = False
            if message.get("caption"):
                draft["caption"] = message["caption"]
            if key in STATE.get("revisions", {}):
                draft["revision"] = STATE["revisions"][key].copy()
            save()
        if first and not message.get("caption"):
            acknowledge(chat, "Фото сохранено. Можно добавить ещё фото или прислать задачу текстом. AI использует снимки в самой генерации, чтобы естественно вписать их в композицию.\n\nФормат: /format_square — 1:1, /format_portrait — 4:5, /format_wide — 16:9.")
    except Exception:
        with STATE_LOCK:
            draft = STATE.get("media_drafts", {}).get(key)
            if draft:
                if draft["photos"]:
                    draft["uploading"] = False
                else:
                    STATE["media_drafts"].pop(key, None)
                save()
        raise


def flush_media_once():
    with STATE_LOCK:
        drafts = STATE.setdefault("media_drafts", {})
        pending = STATE.setdefault("media_submissions", {})
        for key, draft in list(drafts.items()):
            if draft.get("photos") and draft.get("caption") and not draft.get("uploading") and time.time() - draft.get("updated_at", 0) >= 3:
                pending[str(draft["update_id"])] = drafts.pop(key)
        ready = next(((key, draft.copy()) for key, draft in pending.items() if draft.get("retry_at", 0) <= time.time()), None)
        save()
    if not ready:
        return
    key, draft = ready
    payload = {"user_id": draft["user_id"], "update_id": draft["update_id"], "attachments": draft["photos"], "image_mode": "style", "aspect_ratio": draft.get("aspect_ratio", "1:1")}
    try:
        if draft.get("revision"):
            payload.update(draft["revision"], action="revise", comment=draft["caption"], image_only=True)
            result = api("action", payload)
        else:
            payload.update(chat_id=draft["chat_id"], company_id=draft["company_id"], text=draft["caption"])
            result = api("message", payload)
        with STATE_LOCK:
            STATE["media_submissions"].pop(key, None)
            if STATE.get("revisions", {}).get(str(draft["user_id"])) == draft.get("revision"):
                STATE["revisions"].pop(str(draft["user_id"]), None)
            save()
        acknowledge(draft["chat_id"], "Фото и задача приняты. Готовлю новый макет; пришлю на согласование.")
    except TransportError:
        with STATE_LOCK:
            if key in STATE["media_submissions"]:
                STATE["media_submissions"][key]["retry_at"] = time.time() + 8
            save()
    except RuntimeError as error:
        with STATE_LOCK:
            STATE["media_submissions"].pop(key, None)
            save()
        acknowledge(draft["chat_id"], "Не удалось принять фото: " + str(error)[:180])


def media_loop():
    while not STOP.is_set():
        try:
            flush_media_once()
        except Exception as error:
            logging.warning("Media queue: %s", type(error).__name__)
        STOP.wait(1)


def handle(update):
    if update.get("message_reaction_count"):
        reaction = update["message_reaction_count"]
        api("social/reaction", {"chat_id": reaction["chat"]["id"], "username": reaction["chat"].get("username", ""), "message_id": reaction["message_id"], "count": sum(item.get("total_count", 0) for item in reaction.get("reactions", []))})
        return
    incoming = update.get("message", {})
    if incoming.get("chat", {}).get("type") in {"group", "supergroup"}:
        root = incoming if incoming.get("is_automatic_forward") else incoming.get("reply_to_message", {})
        origin = root.get("forward_origin", {})
        if origin.get("type") == "channel":
            api("social/thread", {"chat_id": incoming["chat"]["id"], "message_id": root["message_id"], "channel_id": origin["chat"]["id"], "channel_username": origin["chat"].get("username", ""), "channel_message_id": origin["message_id"]})
        if not incoming.get("is_automatic_forward") and not incoming.get("from", {}).get("is_bot"):
            author = incoming.get("from", {})
            api("social/incoming", {"chat_id": incoming["chat"]["id"], "message_id": incoming["message_id"], "author": " ".join(filter(None, [author.get("first_name"), author.get("last_name")])), "text": incoming.get("text") or incoming.get("caption") or "", "reply_to_message_id": incoming.get("reply_to_message", {}).get("message_id", 0), "thread_id": incoming.get("message_thread_id", 0)})
        return
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
        result = api(
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
            ("Одобрение сохранено, ждём остальных ответственных" if result and result.get("status") == "review" else "Одобрено, публикация " + publication_time(result.get("scheduled_at")) if result and result.get("scheduled_at") else "Одобрено, отправляю в канал")
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
    if m.get("forward_origin") and (m.get("text") or m.get("caption")):
        origin = m["forward_origin"]
        result = api("reference", {
            "user_id": uid, "update_id": update["update_id"],
            "text": m.get("text") or m.get("caption"),
            "entities": m.get("entities") or m.get("caption_entities") or [],
            "source": origin.get("chat", {}).get("title") or origin.get("sender_user_name") or "Пересланный пример",
            "message_id": origin.get("message_id", 0),
        })
        acknowledge(cid, "Пример сохранён для «" + result["company"] + "»: текст, оформление и фирменные эмодзи будут учитываться при генерации.")
        return
    if text == "/cancel":
        STATE["revisions"].pop(str(uid), None)
        STATE.get("media_drafts", {}).pop(str(uid), None)
        save()
        acknowledge(cid, "Доработка отменена.")
        return
    formats = {"/format_square": "1:1", "/format_portrait": "4:5", "/format_wide": "16:9"}
    if text in formats:
        ratio = formats[text]
        with STATE_LOCK:
            STATE.setdefault("image_formats", {})[str(uid)] = ratio
            if str(uid) in STATE.get("media_drafts", {}):
                STATE["media_drafts"][str(uid)]["aspect_ratio"] = ratio
                STATE["media_drafts"][str(uid)]["updated_at"] = time.time()
            save()
        acknowledge(cid, "Формат для следующих изображений: " + ratio + ". Теперь отправьте задачу или фотографии с подписью.")
        return
    if text in {"/image_style", "/image_collage"}:
        acknowledge(cid, "Фото теперь участвуют в самой генерации: AI естественно вписывает их в общую композицию. Отправьте одно фото или альбом с описанием задачи.")
        return
    if m.get("photo") or m.get("document", {}).get("mime_type") in {"image/jpeg", "image/png"}:
        receive_photo(m, update["update_id"])
        return
    if text and not text.startswith("/") and str(uid) in STATE.get("media_drafts", {}):
        with STATE_LOCK:
            draft = STATE["media_drafts"][str(uid)]
            draft["caption"] = text
            draft["updated_at"] = time.time()
            if str(uid) in STATE.get("revisions", {}):
                draft["revision"] = STATE["revisions"][str(uid)].copy()
            save()
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
                "aspect_ratio": STATE.get("image_formats", {}).get(str(uid), ""),
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
            "aspect_ratio": STATE.get("image_formats", {}).get(str(uid), ""),
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
                    "allowed_updates": ["message", "callback_query", "message_reaction_count"],
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
                    if cid and m.get("chat", {}).get("type", "private") == "private":
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
                company = data.get("company") or {}
                if kind == "message":
                    rich_send(chat, event["text"], {}, company)
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
                    deliver_post(chat, post, company)
                    stamp = publication_time(post.get("scheduled_at"))
                    send(
                        chat,
                        f"На согласовании · версия {rev}\n{company['name']} → {company.get('channel') or 'канал не подключён'}\nВремя публикации: {stamp}\n\n{post['title']}\n\nПубликация только после вашего одобрения.",
                        buttons,
                    )
                elif kind == "publish":
                    if not api("publication-check", {"post_id": post["id"]}).get("ok"):
                        api("outbox/" + event["id"], {"ok": True})
                        continue
                    if post.get("platform") in {"vk", "max"}:
                        with urllib.request.urlopen(urllib.request.Request(BACKEND + "/internal/media/" + post["image"], headers={"Authorization": "Bearer " + INTERNAL}), timeout=20) as response:
                            image = response.read()
                        outcome.update(social_platforms.publish(data.get("connection"), post, image))
                        outcome["ok"] = True
                        api("outbox/" + event["id"], outcome)
                        continue
                    msg = deliver_post(company["channel"], post, company)
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
                elif kind == "reply":
                    checked = api("reply-check", {"id": event["reply_id"]})
                    if not checked.get("ok"):
                        api("outbox/" + event["id"], {"ok": True})
                        continue
                    entry = checked["entry"]
                    if entry.get("platform") in {"vk", "max"}:
                        outcome.update(social_platforms.send_reply(checked.get("connection"), entry))
                    else:
                        msg = tg("sendMessage", {"chat_id": entry["chat_id"], "text": entry["draft"], "reply_parameters": {"message_id": entry["message_id"], "allow_sending_without_reply": False}})
                        outcome.update(message_id=msg["message_id"])
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


def health_loop():
    admins, failures, alerted = [], 0, False
    while not STOP.is_set():
        try:
            request(BACKEND + "/healthz", timeout=5)
            config = api("config")
            admins = [int(uid) for uid, allowed in config.get("admins", {}).items() if allowed]
            if alerted:
                for uid in admins:
                    acknowledge(uid, "Сервис AI-маркетолога снова доступен.")
            failures, alerted = 0, False
        except Exception:
            failures += 1
            if failures >= 3 and not alerted:
                for uid in admins:
                    acknowledge(uid, "Сервис AI-маркетолога не отвечает на проверку доступности. Проверьте локальный процесс backend.")
                alerted = True
        STOP.wait(15)


def social_loop():
    while not STOP.is_set():
        try:
            for connection in api("social/config"):
                try:
                    result = social_platforms.collect(connection)
                except Exception:
                    result = {"company_id": connection["company_id"], "platform": connection["platform"], "error": "Не удалось обновить площадку. Проверьте токен, права и соединение"}
                api("social/observations", result)
        except Exception:
            logging.warning("Social monitoring temporarily unavailable")
        STOP.wait(120)


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
                {"command": "format_square", "description": "Картинки 1:1 — квадрат"},
                {"command": "format_portrait", "description": "Картинки 4:5 — вертикаль для ленты"},
                {"command": "format_wide", "description": "Картинки 16:9 — широкий формат"},
                {"command": "cancel", "description": "Отменить текущую доработку или загрузку фото"},
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
    threading.Thread(target=media_loop, daemon=True).start()
    threading.Thread(target=social_loop, daemon=True).start()
    threading.Thread(target=health_loop, daemon=True).start()
    updates_loop()


if __name__ == "__main__":
    main()
