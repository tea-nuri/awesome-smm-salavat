import importlib.util
import io
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
    def test_photo_album_submits_once_with_all_sources(self):
        with patch.dict(bot.STATE, {"offset":0,"revisions":{},"image_formats":{"1":"4:5"}},clear=True), patch.object(bot,"tg",return_value={"file_path":"photo.jpg"}), patch.object(bot.urllib.request,"urlopen",side_effect=lambda *a,**k:io.BytesIO(b"image")), patch.object(bot,"request",side_effect=[{"id":"one","company_id":"a"},{"id":"two","company_id":"a"}]), patch.object(bot,"api",return_value={"reply":"ok"}) as api, patch.object(bot,"acknowledge"), patch.object(bot.time,"time",return_value=100):
            for index in [1,2]:
                message={"from":{"id":1},"chat":{"id":1},"media_group_id":"album","photo":[{"file_id":str(index)}]}
                if index==1:message["caption"]="Создай коллаж"
                bot.receive_photo(message,1000+index)
            bot.flush_media_once()
            api.assert_not_called()
            with patch.object(bot.time,"time",return_value=104):
                bot.flush_media_once()
                bot.flush_media_once()
            api.assert_called_once()
            self.assertEqual(api.call_args.args[1]["attachments"],["one","two"])
            self.assertEqual(api.call_args.args[1]["company_id"],"a")
            self.assertEqual(api.call_args.args[1]["aspect_ratio"],"4:5")
            self.assertEqual(api.call_args.args[1]["image_mode"],"style")
            self.assertFalse(bot.STATE["media_submissions"])

    def test_format_command_updates_pending_album_without_creating_post(self):
        with patch.dict(bot.STATE, {"revisions": {}, "media_drafts": {"1": {"photos": ["photo"]}}}, clear=True), patch.object(bot, "api") as api, patch.object(bot, "acknowledge"):
            bot.handle({"update_id": 100, "message": {"from": {"id": 1}, "chat": {"id": 1, "type": "private"}, "text": "/format_wide"}})
            self.assertEqual(bot.STATE["image_formats"]["1"], "16:9")
            self.assertEqual(bot.STATE["media_drafts"]["1"]["aspect_ratio"], "16:9")
            api.assert_not_called()

    def test_photos_wait_for_task_and_do_not_become_materials(self):
        with patch.dict(bot.STATE,{"offset":0,"revisions":{},"media_drafts":{"1":{"photos":["photo"],"user_id":1,"chat_id":1,"updated_at":0,"update_id":200}}},clear=True), patch.object(bot,"api") as api:
            bot.flush_media_once()
            api.assert_not_called()
            self.assertTrue(bot.STATE["media_drafts"])

    def test_selected_pack_replaces_reference_pack_and_can_be_disabled(self):
        company = {"emoji_source": "pack", "emoji_items": [{"emoji": "💙", "custom_emoji_id": "222"}], "references": [{"text": "💙", "entities": [{"type": "custom_emoji", "offset": 0, "length": 2, "custom_emoji_id": "111"}]}]}
        entities = bot.text_entities("💙 Команда", {}, company)
        self.assertEqual([e["custom_emoji_id"] for e in entities if e["type"] == "custom_emoji"], ["222"])
        company["emoji_source"] = "standard"
        self.assertFalse(any(e["type"] == "custom_emoji" for e in bot.text_entities("💙 Команда", {}, company)))

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

    def test_pack_mode_rejects_unknown_emoji_before_any_delivery(self):
        company = {"emoji_source": "pack", "emoji_items": [{"emoji": "🍏", "custom_emoji_id": "123"}]}
        body = "🍏 Фрукты\n\n" + "Обычный текст. " * 100 + "🥕"
        with patch.object(bot, "photo") as photo, patch.object(bot, "tg") as tg:
            with self.assertRaisesRegex(RuntimeError, "вне выбранного пака"):
                bot.deliver_post(1, {"body": body, "image": "existing.png"}, company)
            photo.assert_not_called()
            tg.assert_not_called()

    def test_pack_mode_assigns_ids_to_whole_emoji_sequences(self):
        company = {"emoji_source": "pack", "emoji_items": [{"emoji": "👩‍💻", "custom_emoji_id": "1"}, {"emoji": "⚽️", "custom_emoji_id": "2"}]}
        text = "👩‍💻 Go 1.23 #Команда — ⚽"
        entities = [e for e in bot.text_entities(text, {}, company) if e["type"] == "custom_emoji"]
        self.assertEqual([e["custom_emoji_id"] for e in entities], ["1", "2"])
        self.assertEqual(entities[0]["length"], 5)
        self.assertEqual(entities[1]["offset"], bot.utf16_len(text[:text.index("⚽")]))
        with self.assertRaises(RuntimeError):
            bot.text_entities("👩‍🚀", {}, company)

    def test_rich_chunks_do_not_split_a_custom_emoji(self):
        company = {"emoji_source": "pack", "emoji_items": [{"emoji": "👩‍💻", "custom_emoji_id": "123"}]}
        text = "a" * 3898 + "👩‍💻" + "x"
        with patch.object(bot, "tg", side_effect=lambda method, payload: {"message_id": 1,"entities":payload["entities"]}) as tg:
            bot.rich_send(1, text, {}, company)
            payloads = [call.args[1] for call in tg.call_args_list]
            self.assertEqual("".join(item["text"] for item in payloads), text)
            self.assertEqual(payloads[1]["text"], "👩‍💻x")
            self.assertEqual(next(e for e in payloads[1]["entities"] if e["type"] == "custom_emoji")["length"], 5)

    def test_pack_mode_never_retries_text_as_standard_emoji(self):
        company = {"emoji_source": "pack", "emoji_items": [{"emoji": "🍏", "custom_emoji_id": "123"}]}
        with patch.object(bot, "tg", side_effect=RuntimeError("Bad Request: custom emoji not allowed")) as tg:
            with self.assertRaises(RuntimeError):
                bot.rich_send(1, "🍏 Фрукты", {}, company)
            tg.assert_called_once()

    def test_pack_mode_never_retries_photo_as_standard_emoji(self):
        with patch.object(bot.urllib.request, "urlopen", return_value=io.BytesIO(b"image")), patch.object(bot, "request", side_effect=RuntimeError("Bad Request: custom emoji not allowed")) as request:
            with self.assertRaises(RuntimeError):
                bot.photo(1, "existing.png", "🍏 Фрукты", entities=[{"type": "custom_emoji", "offset": 0, "length": 2, "custom_emoji_id": "123"}], strict_emoji=True)
            request.assert_called_once()

    def test_success_response_must_keep_exact_pack_entities(self):
        expected=[{"type":"custom_emoji","offset":0,"length":2,"custom_emoji_id":"123"}]
        bot.verify_emoji_delivery({"entities":expected},expected,True,1)
        with patch.object(bot,"tg",return_value=True) as tg:
            with self.assertRaises(bot.EmojiDeliveryError):
                bot.verify_emoji_delivery({"message_id":9,"chat":{"id":-100,"type":"channel"},"entities":[]},expected,True,"@test")
            tg.assert_called_once_with("deleteMessage",{"chat_id":-100,"message_id":9})
        bot.EMOJI_BLOCKED_CHATS.clear()

    def test_different_custom_emoji_is_not_accepted(self):
        expected=[{"type":"custom_emoji","offset":0,"length":2,"custom_emoji_id":"123"}]
        with patch.object(bot,"tg",return_value=True):
            with self.assertRaises(bot.EmojiDeliveryError):
                bot.verify_emoji_delivery({"message_id":9,"chat":{"id":1},"caption_entities":[{**expected[0],"custom_emoji_id":"999"}]},expected,True,1)

    def test_failed_rollback_is_not_reported_as_safe_rejection(self):
        expected=[{"type":"custom_emoji","offset":0,"length":2,"custom_emoji_id":"123"}]
        with patch.object(bot,"tg",side_effect=bot.TransportError("offline")):
            with self.assertRaises(bot.EmojiDeliveryError) as caught:
                bot.verify_emoji_delivery({"message_id":9,"chat":{"id":1}},expected,True,1)
        self.assertFalse(caught.exception.rollback_complete)

    def test_known_unsupported_channel_is_rejected_before_sending(self):
        bot.EMOJI_BLOCKED_CHATS.add("@test")
        try:
            with patch.object(bot,"photo") as photo:
                with self.assertRaises(bot.EmojiDeliveryError):
                    bot.deliver_post("@test",{"body":"🍏","image":"a.png"},{"emoji_source":"pack"})
                photo.assert_not_called()
        finally:
            bot.EMOJI_BLOCKED_CHATS.clear()

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
