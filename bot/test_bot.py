import importlib.util
import os
import tempfile
import unittest
from pathlib import Path
from unittest.mock import patch

TMP = tempfile.TemporaryDirectory()
os.environ.setdefault("TELEGRAM_BOT_TOKEN", "test-token")
os.environ.setdefault("INTERNAL_TOKEN", "test-internal")
os.environ["BOT_DATA_DIR"] = TMP.name
spec = importlib.util.spec_from_file_location(
    "bot_app", Path(__file__).with_name("main.py")
)
bot = importlib.util.module_from_spec(spec)
spec.loader.exec_module(bot)


class BotDeliveryTest(unittest.TestCase):
    def test_custom_emoji_offsets_after_surrogate_pairs(self):
        text = "🏆 Команда\n\nСпасибо 💙\n\n#Вкоманде"
        ref = {"text": "Спасибо 💙", "entities": [{"type": "custom_emoji", "offset": 8, "length": 2, "custom_emoji_id": "12345"}]}
        entities = bot.text_entities(text, {"emphasis": ["Спасибо"]}, {"references": [ref]})
        custom = next(e for e in entities if e["type"] == "custom_emoji")
        self.assertEqual(custom["offset"], bot.utf16_len(text[:text.index("💙")]))
        self.assertEqual(custom["length"], 2)
        self.assertEqual(custom["custom_emoji_id"], "12345")

    def test_long_post_is_delivered_in_full_with_image(self):
        body = "💎 Заголовок\n\n" + ("История команды. " * 90) + "\n\n#Вкоманде"
        post = {"body": body, "image": "image.png"}
        with patch.object(bot, "photo", return_value={}) as photo, patch.object(bot, "tg", return_value={"message_id": 99}) as tg:
            result = bot.deliver_post(1, post, {})
        photo.assert_called_once()
        sent_text = "".join(c.args[1]["text"] for c in tg.call_args_list)
        self.assertEqual(sent_text, body)
        self.assertEqual(result["message_id"], 99)

    def test_forwarded_reference_does_not_start_generation(self):
        update = {"update_id": 200, "message": {"from": {"id": 1}, "chat": {"id": 1, "type": "private"}, "forward_origin": {"type": "channel", "chat": {"title": "Бренд"}, "message_id": 13}, "caption": "🏆 Коллеги!\n\n#Вкоманде", "caption_entities": []}}
        with patch.object(bot, "api", return_value={"company": "САЛАВАТСТЕКЛО"}) as api, patch.object(bot, "acknowledge"):
            bot.handle(update)
        self.assertEqual(api.call_args.args[0], "reference")
        self.assertEqual(api.call_args.args[1]["update_id"], 200)

    def test_explicit_custom_emoji_rejection_uses_standard_fallback(self):
        company = {"references": [{"text": "💎", "entities": [{"type": "custom_emoji", "offset": 0, "length": 2, "custom_emoji_id": "12345"}]}]}
        calls = []
        def mock_tg(method, data):
            calls.append(data.copy())
            if len(calls) == 1:
                raise RuntimeError("Bad Request: custom emoji not allowed")
            return {"message_id": 1}
        with patch.object(bot, "tg", side_effect=mock_tg):
            bot.rich_send(1, "💎 Наша команда", {}, company)
        self.assertEqual(len(calls), 2)
        self.assertFalse(any(e["type"] == "custom_emoji" for e in calls[1]["entities"]))

    def test_unknown_rich_message_delivery_is_not_retried(self):
        with patch.object(bot, "tg", side_effect=bot.TransportError("offline")) as tg:
            with self.assertRaises(bot.TransportError):
                bot.rich_send(1, "💎 Наша команда", {}, {})
        tg.assert_called_once()

    def test_revision_ack_failure_does_not_report_action_failure(self):
        bot.STATE["revisions"] = {"1": {"post_id": "post", "revision": 1}}
        update = {
            "update_id": 123,
            "message": {
                "from": {"id": 1},
                "chat": {"id": 1, "type": "private"},
                "text": "Shorter",
            },
        }
        with (
            patch.object(bot, "api", return_value={"status": "generating"}) as api,
            patch.object(bot, "send", side_effect=bot.TransportError("offline")),
        ):
            bot.handle(update)
        self.assertNotIn("1", bot.STATE["revisions"])
        self.assertEqual(api.call_args.args[1]["update_id"], 123)
        api.assert_called_once()

    def test_callback_ack_failure_does_not_fail_approval(self):
        update = {
            "update_id": 124,
            "callback_query": {
                "id": "query",
                "from": {"id": 1},
                "message": {"chat": {"id": 1}, "message_id": 10},
                "data": "approve:post:2",
            },
        }
        with (
            patch.object(bot, "api", return_value={"status": "approved"}) as api,
            patch.object(bot, "tg", side_effect=bot.TransportError("offline")),
        ):
            bot.handle(update)
        api.assert_called_once()
        self.assertEqual(api.call_args.args[1]["update_id"], 124)

    def test_network_retry_only_for_idempotent_action(self):
        with (
            patch.object(
                bot,
                "request",
                side_effect=[bot.TransportError("offline"), {"ok": True}],
            ) as request,
            patch.object(bot.STOP, "wait"),
        ):
            self.assertEqual(bot.api("action", {"update_id": 125}), {"ok": True})
            self.assertEqual(request.call_count, 2)

    def test_outbox_claim_is_not_blindly_retried(self):
        with patch.object(
            bot, "request", side_effect=bot.TransportError("offline")
        ) as request:
            with self.assertRaises(bot.TransportError):
                bot.api("outbox")
            request.assert_called_once()


if __name__ == "__main__":
    unittest.main()
