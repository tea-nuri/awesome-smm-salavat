"""VK / MAX transports. No automatic retries for sends with uncertain outcomes.

Credentials arrive only over the private backend transport and never enter logs.
These integrations are intentionally not live-tested without owner credentials.
"""
import json
import secrets
import urllib.parse
import urllib.request
import urllib.error
from datetime import datetime, timezone

MAX_BASE = "https://platform-api2.max.ru"

class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        return None

HTTP = urllib.request.build_opener(NoRedirect)



def _request(url, payload=None, headers=None, method=None):
    request = urllib.request.Request(url, data=payload, headers=headers or {}, method=method)
    try:
        with HTTP.open(request, timeout=45) as response:
            result = json.load(response)
    except urllib.error.HTTPError as exc:
        raise RuntimeError(f"Социальная платформа отклонила запрос: HTTP {exc.code}") from None
    except Exception:
        raise RuntimeError("Нет подтверждения от социальной платформы; автоматического повтора не будет") from None
    if result.get("error") or result.get("success") is False:
        raise RuntimeError("Социальная платформа вернула ошибку. Проверьте токен и права бота")
    return result


def vk(connection, method, **params):
    params.update(access_token=connection["token"], v="5.199")
    return _request("https://api.vk.com/method/" + method, urllib.parse.urlencode(params).encode(), {"Content-Type": "application/x-www-form-urlencoded"}).get("response")


def max_api(connection, method, path, params=None, body=None):
    url = MAX_BASE + path
    if params:
        url += "?" + urllib.parse.urlencode(params)
    return _request(url, None if body is None else json.dumps(body).encode(), {"Authorization": connection["token"], "Content-Type": "application/json"}, method)


def _upload(url, content, platform, token=""):
    parsed = urllib.parse.urlparse(url)
    allowed = ("vk.com", "vk.ru", "vkuserphoto.ru", "vkuser.net", "userapi.com") if platform == "vk" else ("max.ru", "oneme.ru")
    if parsed.scheme != "https" or parsed.username or parsed.password or not any(parsed.hostname == host or (parsed.hostname or "").endswith("." + host) for host in allowed):
        raise RuntimeError("Платформа вернула недопустимый адрес загрузки")
    boundary = "smm" + secrets.token_hex(12)
    field = "photo" if platform == "vk" else "data"
    payload = (f'--{boundary}\r\nContent-Disposition: form-data; name="{field}"; filename="creative.png"\r\nContent-Type: image/png\r\n\r\n'.encode() + content + f"\r\n--{boundary}--\r\n".encode())
    headers = {"Content-Type": "multipart/form-data; boundary=" + boundary}
    if platform == "max":
        headers["Authorization"] = token
    return _request(url, payload, headers)


def publish(connection, post, image):
    if not connection or not connection.get("token"):
        raise RuntimeError("Токен площадки не подключён")
    if connection["platform"] == "vk":
        owner = int(connection["target"])
        attachments = ""
        if image:
            destination = vk(connection, "photos.getWallUploadServer", group_id=abs(owner))
            uploaded = _upload(destination["upload_url"], image, "vk")
            saved = vk(connection, "photos.saveWallPhoto", group_id=abs(owner), photo=uploaded["photo"], server=uploaded["server"], hash=uploaded["hash"])[0]
            attachments = f"photo{saved['owner_id']}_{saved['id']}"
        result = vk(connection, "wall.post", owner_id=owner, from_group=1, message=post["body"], attachments=attachments, guid=f"{post['id']}-{post['revision']}")
        message_id = result["post_id"]
        return {"message_id": message_id, "external_message_id": str(message_id), "url": f"https://vk.com/wall{owner}_{message_id}"}
    attachments = []
    if image:
        destination = max_api(connection, "POST", "/uploads", {"type": "image"})
        uploaded = _upload(destination["url"], image, "max", connection["token"])
        attachments = [{"type": "image", "payload": {"photos": uploaded["photos"]}}]
    result = max_api(connection, "POST", "/messages", {"chat_id": connection["target"]}, {"text": post["body"], "attachments": attachments})
    message = result["message"]
    message_id = message["body"]["mid"]
    return {"external_message_id": message_id, "url": message.get("url", "")}


def send_reply(connection, entry):
    if connection["platform"] == "vk":
        result = vk(connection, "wall.createComment", owner_id=connection["target"], post_id=entry["parent_external_id"], reply_to_comment=entry["external_id"], from_group=abs(int(connection["target"])), message=entry["draft"], guid=f"{entry['id']}-{entry['revision']}")
        return {"message_id": result["comment_id"]}
    result = max_api(connection, "POST", "/messages/" + urllib.parse.quote(entry["parent_external_id"], safe="") + "/comments", body={"text": entry["draft"], "link": {"type": "reply", "mid": entry["external_id"]}})
    return {"external_message_id": result.get("message", result).get("body", {}).get("mid", "")}


def collect(connection):
    metrics, comments = [], []
    date = datetime.now(timezone.utc).strftime("%Y-%m-%d")
    for post in connection.get("posts", [])[-50:]:
        mid = post.get("external_message_id") or str(post.get("message_id") or "")
        if not mid:
            continue
        if connection["platform"] == "vk":
            response = vk(connection, "wall.getById", posts=f"{connection['target']}_{mid}")
            items = response.get("items", []) if isinstance(response, dict) else response
            if items:
                item = items[0]
                metric = {"post_id": post["id"], "date": date}
                for field, key in [("views", "views"), ("reactions", "likes"), ("comments", "comments")]:
                    if isinstance(item.get(key), dict) and "count" in item[key]:
                        metric[field] = item[key]["count"]
                metrics.append(metric)
            offset = 0
            for _ in range(5):
                result = vk(connection, "wall.getComments", owner_id=connection["target"], post_id=mid, count=100, offset=offset, sort="desc")
                rows = result.get("items", [])
                for row in rows:
                    if row.get("from_id") == int(connection["target"]):
                        continue
                    comments.append({"post_id": post["id"], "external_id": str(row["id"]), "parent_external_id": mid, "author": "VK ID " + str(row.get("from_id", "")), "text": row.get("text", "")})
                if len(rows) < 100:
                    break
                offset += 100
        else:
            escaped = urllib.parse.quote(mid, safe="")
            message = max_api(connection, "GET", "/messages/" + escaped)
            message = message.get("message", message)
            stat = message.get("stat") or {}
            metric = {"post_id": post["id"], "date": date}
            if isinstance(stat.get("views"), int):
                metric["views"] = stat["views"]
            metrics.append(metric)
            result = max_api(connection, "GET", "/messages/" + escaped + "/comments", {"count": 100})
            for row in result.get("messages", []):
                author = row.get("sender") or {}
                if author.get("is_bot"):
                    continue
                comments.append({"post_id": post["id"], "external_id": row["body"]["mid"], "parent_external_id": mid, "author": author.get("first_name", "Посетитель"), "text": row["body"].get("text", "")})
    return {"company_id": connection["company_id"], "platform": connection["platform"], "metrics": metrics, "comments": comments}
