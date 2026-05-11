"""
Tambah atau perbarui session bot account via cookies JSON dari browser.

Usage:
  python add_session.py            # interactive (paste cookies, Enter dua kali)
  python add_session.py --batch    # batch mode: baca JSON dari stdin, output JSON
"""

import json
import sys
from pathlib import Path

BASE_DIR    = Path(__file__).parent
CONFIG_FILE = BASE_DIR / "config.json"

try:
    from instagrapi import Client
    from urllib.parse import unquote
except ImportError:
    _err = {"ok": False, "error": "instagrapi not installed"}
    print(json.dumps(_err))
    sys.exit(1)

BATCH = "--batch" in sys.argv


def out(data: dict):
    """Print result. In batch mode always JSON; interactive mode human-readable."""
    if BATCH:
        print(json.dumps(data, ensure_ascii=False))
    else:
        if data.get("ok"):
            print(f"\n✓ Login berhasil sebagai: @{data['username']}")
            print(f"✓ Session: {data['session_file']}")
            if data.get("added_to_config"):
                print(f"✓ Ditambahkan ke config.json")
            else:
                print(f"  Sudah ada di config.json (session diperbarui)")
            print("\nSelesai!")
        else:
            print(f"\n✗ Error: {data.get('error')}")


def extract_cookie(cookies: list, name: str) -> str:
    for c in cookies:
        if c.get("name") == name:
            return unquote(c.get("value", ""))
    return ""


def process(raw_json: str) -> dict:
    try:
        cookies = json.loads(raw_json)
        if not isinstance(cookies, list):
            return {"ok": False, "error": "Expected a JSON array of cookies"}
    except Exception as e:
        return {"ok": False, "error": f"JSON parse error: {e}"}

    sessionid = extract_cookie(cookies, "sessionid")
    if not sessionid:
        return {"ok": False, "error": "Cookie 'sessionid' tidak ditemukan. Pastikan sudah login di browser."}

    cl = Client()
    cl.delay_range = [2, 4]
    try:
        cl.login_by_sessionid(sessionid)
    except Exception as e:
        return {"ok": False, "error": f"Login gagal: {e}"}

    username = cl.username
    session_filename = f"session_{username}.json"
    session_path = BASE_DIR / session_filename
    cl.dump_settings(session_path)

    # Update config.json
    if CONFIG_FILE.exists():
        try:
            cfg = json.loads(CONFIG_FILE.read_text())
        except Exception:
            cfg = {"poll_interval": 3600, "scraper_accounts": [], "targets": []}
    else:
        cfg = {"poll_interval": 3600, "scraper_accounts": [], "targets": []}

    existing = [a["username"] for a in cfg.get("scraper_accounts", [])]
    added = username not in existing
    if added:
        cfg.setdefault("scraper_accounts", []).append({
            "username": username,
            "session_file": session_filename,
            "enabled": True,
        })
        CONFIG_FILE.write_text(json.dumps(cfg, indent=2, ensure_ascii=False))

    return {
        "ok": True,
        "username": username,
        "session_file": session_filename,
        "added_to_config": added,
    }


def main():
    if BATCH:
        raw = sys.stdin.read().strip()
        if not raw:
            out({"ok": False, "error": "No input received"})
            sys.exit(1)
        result = process(raw)
        out(result)
        sys.exit(0 if result.get("ok") else 1)

    # ── Interactive mode ──
    print("=" * 60)
    print("Kerjain — Add Instagram Bot Account")
    print("=" * 60)
    print()
    print("1. Buka instagram.com di browser dan login")
    print("2. Install ekstensi: 'Cookie-Editor' atau 'EditThisCookie'")
    print("3. Export cookies sebagai JSON")
    print("4. Paste JSON di bawah ini (lalu tekan Enter dua kali):")
    print()

    lines = []
    try:
        while True:
            line = input()
            if line == "" and lines and lines[-1] == "":
                break
            lines.append(line)
    except EOFError:
        pass

    raw = "\n".join(lines).strip()
    if not raw:
        print("Tidak ada input.")
        sys.exit(1)

    result = process(raw)
    out(result)
    sys.exit(0 if result.get("ok") else 1)


if __name__ == "__main__":
    main()
