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
