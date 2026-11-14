"""Reproducible local acceptance checks, without publishing or VK/MAX requests."""
from pathlib import Path
import argparse
import datetime
import hashlib
import json
import subprocess
import sys


def main():
    root = Path(__file__).resolve().parent.parent
    parser = argparse.ArgumentParser()
    parser.add_argument("--output", default=str(root / "acceptance.json"))
    args = parser.parse_args()
    checks = []
    commands = [
        ("Go core: race detector and unit/integration checks", root / "backend", ["go", "test", "-race", "./..."]),
        ("Telegram, document extraction and image export", root, [sys.executable, "-m", "unittest", "discover", "-s", "bot", "-p", "test_*.py"]),
        ("Vue production build", root / "frontend", ["npm", "run", "build"]),
    ]
    for name, cwd, command in commands:
        run = subprocess.run(command, cwd=cwd, capture_output=True, text=True)
        checks.append({"name": name, "status": "passed" if run.returncode == 0 else "failed", "exit_code": run.returncode, "output": (run.stdout + run.stderr)[-3000:]})
        print(name + ": " + checks[-1]["status"])
    digest = hashlib.sha256()
    files = list((root / "backend").glob("*.go")) + list((root / "bot").glob("*.py")) + list((root / "frontend/src").glob("*.vue"))
    for file in sorted(files):
        digest.update(str(file.relative_to(root)).encode()); digest.update(file.read_bytes())
    report = {
        "generated_at": datetime.datetime.now(datetime.timezone.utc).isoformat(),
        "source_fingerprint": digest.hexdigest(),
        "checks": checks,
        "scope": {"server_deployment": "excluded_by_user", "vk_max_api_tests": "not_performed_by_user_request", "monetary_budget": "deferred_by_user; subscription usage is measured"},
        "acceptance": [
            {"criterion": 1, "name": "Telegram task and answer", "evidence": "existing Telegram transport; unit checks; previous live use"},
            {"criterion": 2, "name": "Context and clarification", "evidence": "tasks_test.go and conversation_test.go"},
            {"criterion": 3, "name": "Company knowledge", "evidence": "sources_test.go; company-scoped sources"},
            {"criterion": 4, "name": "Text and image", "evidence": "previous live generated materials; render and image-input checks"},
            {"criterion": 5, "name": "Approval and revision", "evidence": "main_test.go, operations_test.go"},
            {"criterion": 6, "name": "No publication without permission", "evidence": "rejection, revision, two-person approval and revoked-access checks"},
            {"criterion": 7, "name": "Real Telegram publication", "evidence": "previous live publications; no new automatic publication during this acceptance run"},
            {"criterion": 8, "name": "Autonomous schedule", "evidence": "schedule_test.go and marketing automation checks; previous scheduled Telegram publications"},
            {"criterion": 9, "name": "Company isolation", "evidence": "sources, media, task, artifact, metric and approval checks"},
            {"criterion": 10, "name": "Persistence and restart", "evidence": "state, source, schedule and uncertain-delivery checks"},
            {"criterion": 11, "name": "No-code company setup", "evidence": "existing company UI; onboarding steps in README"},
            {"criterion": 12, "name": "ChatGPT usage mechanism", "evidence": "Codex subscription rate-limit endpoint and measured generation usage; manual OpenAI device sign-in for a new account"},
        ],
        "manual_verification": "This report does not certify a new end-to-end live publication or real VK/MAX operation. Follow the README demonstration with explicit human approval.",
    }
    Path(args.output).write_text(json.dumps(report, ensure_ascii=False, indent=2))
    raise SystemExit(0 if all(c["status"] == "passed" for c in checks) else 1)


if __name__ == "__main__":
    main()
