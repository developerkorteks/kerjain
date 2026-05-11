"""
Cek status semua session di config.json.
Output JSON array, satu item per scraper_account.

Usage:
  python check_sessions.py --batch   # output JSON
"""

import json
import sys
import time
from pathlib import Path

BASE_DIR    = Path(__file__).parent
CONFIG_FILE = BASE_DIR / "config.json"

try:
    from instagrapi import Client
except ImportError:
    print(json.dumps([{"error": "instagrapi not installed"}]))
    sys.exit(1)


def check_account(account_cfg: dict) -> dict:
    username     = account_cfg.get("username", "")
    session_file = BASE_DIR / account_cfg.get("session_file", "")
    enabled      = account_cfg.get("enabled", True)

    base = {
        "username": username,
        "enabled":  enabled,
        "file_exists": False,
        "file_age_days": None,
        "status": "missing",   # missing | expired | ok | error
        "message": "",
    }

    if not session_file.exists():
        base["message"] = "Session file tidak ditemukan. Silakan login ulang."
        return base

    base["file_exists"] = True
    age_days = (time.time() - session_file.stat().st_mtime) / 86400
    base["file_age_days"] = round(age_days, 1)

    # Try to load + verify via lightweight API call
    cl = Client()
    cl.delay_range = [0, 1]
    try:
        cl.load_settings(session_file)
        info = cl.account_info()
        base["status"]  = "ok"
        base["message"] = f"Aktif · @{info.username} · {base['file_age_days']}h terakhir update"
        # Refresh session file
        cl.dump_settings(session_file)
    except Exception as e:
        err = str(e).lower()
        if "login" in err or "unauthorized" in err or "session" in err:
            base["status"]  = "expired"
            base["message"] = "Session expired. Silakan login ulang."
        else:
            base["status"]  = "error"
            base["message"] = str(e)[:120]

    return base


def main():
    try:
        cfg = json.loads(CONFIG_FILE.read_text())
    except Exception as e:
        print(json.dumps([{"error": f"config.json error: {e}"}]))
        sys.exit(1)

    accounts = cfg.get("scraper_accounts", [])
    if not accounts:
        print(json.dumps([]))
        return

    results = []
    for acc in accounts:
        results.append(check_account(acc))

    print(json.dumps(results, ensure_ascii=False))


if __name__ == "__main__":
    main()
