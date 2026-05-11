"""
Kerjain — Instagram Scraper Service (multi-account, dynamic config)

Config: config.json  — reload otomatis tiap cycle, tidak perlu restart.
Login : add_session.py — tambah bot account via cookies JSON dari browser.
"""

import json
import logging
import os
import sys
import time
from datetime import datetime, timezone
from pathlib import Path

import requests
from dotenv import load_dotenv
from instagrapi import Client
from instagrapi.exceptions import (
    ChallengeRequired,
    LoginRequired,
    RateLimitError,
    UserNotFound,
)

load_dotenv()

logging.basicConfig(
    level=logging.INFO,
    format="%(asctime)s [%(levelname)s] %(message)s",
    handlers=[logging.StreamHandler(sys.stdout)],
)
log = logging.getLogger(__name__)

# ── Static env ────────────────────────────────────────────────────────────────

GO_API_URL     = os.getenv("GO_API_URL", "http://localhost:8080")
INGEST_API_KEY = os.getenv("INGEST_API_KEY", "")
GO_MEDIA_DIR   = Path(os.getenv("GO_MEDIA_DIR",
                    str(Path(__file__).parent.parent / "data" / "media")))
BASE_DIR       = Path(__file__).parent
CONFIG_FILE    = BASE_DIR / "config.json"
SEEN_FILE      = BASE_DIR / "seen_ids.json"

# ── Config ────────────────────────────────────────────────────────────────────

def load_config() -> dict:
    try:
        cfg = json.loads(CONFIG_FILE.read_text())
        # Validate minimal keys
        cfg.setdefault("poll_interval", 3600)
        cfg.setdefault("scraper_accounts", [])
        cfg.setdefault("targets", [])
        return cfg
    except Exception as e:
        log.error(f"config.json error: {e}")
        sys.exit(1)

# ── Seen IDs (global deduplication across all accounts) ───────────────────────

def load_seen() -> set:
    if SEEN_FILE.exists():
        try:
            return set(json.loads(SEEN_FILE.read_text()))
        except Exception:
            pass
    return set()

def save_seen(seen: set):
    ids = list(seen)[-10000:]  # keep last 10k
    SEEN_FILE.write_text(json.dumps(ids))

# ── IG Client pool ────────────────────────────────────────────────────────────

# username → Client instance
_clients: dict[str, Client] = {}

def get_client(account_cfg: dict) -> Client | None:
    """Return a ready Client for this bot account, (re)building if needed."""
    username = account_cfg["username"]
    session_file = BASE_DIR / account_cfg["session_file"]

    if not session_file.exists():
        log.error(f"[{username}] session file '{session_file}' not found.")
        log.error(f"  Run: python add_session.py {username}")
        return None

    # Return cached client if already connected
    if username in _clients:
        return _clients[username]

    cl = Client()
    cl.delay_range = [2, 4]
    try:
        cl.load_settings(session_file)
        log.info(f"[{username}] session loaded (user_id={cl.user_id})")
        _clients[username] = cl
        return cl
    except Exception as e:
        log.warning(f"[{username}] session load failed: {e}")
        if username in _clients:
            del _clients[username]
        return None

def invalidate_client(username: str):
    _clients.pop(username, None)

# ── Image download ────────────────────────────────────────────────────────────

def download_image(media) -> tuple[str, str] | tuple[None, None]:
    try:
        GO_MEDIA_DIR.mkdir(parents=True, exist_ok=True)

        # Prefer first resource (carousel) else thumbnail
        url = None
        if hasattr(media, "resources") and media.resources:
            res = media.resources[0]
            url = str(res.thumbnail_url or "")
        if not url and hasattr(media, "thumbnail_url") and media.thumbnail_url:
            url = str(media.thumbnail_url)
        if not url:
            return None, None

        # Skip re-download if file already exists
        for ext in (".jpg", ".webp", ".png"):
            existing = GO_MEDIA_DIR / f"ig_{media.pk}{ext}"
            if existing.exists():
                mime = {"jpg": "image/jpeg", "webp": "image/webp", "png": "image/png"}.get(ext[1:], "image/jpeg")
                return existing.name, mime

        resp = requests.get(url, timeout=15)
        resp.raise_for_status()

        ct = resp.headers.get("content-type", "image/jpeg").split(";")[0].strip()
        ext_map = {"image/jpeg": ".jpg", "image/png": ".png", "image/webp": ".webp"}
        ext = ext_map.get(ct, ".jpg")

        filename = f"ig_{media.pk}{ext}"
        (GO_MEDIA_DIR / filename).write_bytes(resp.content)
        return filename, ct
    except Exception as e:
        log.warning(f"  image download failed: {e}")
        return None, None

# ── Ingest ────────────────────────────────────────────────────────────────────

def send_to_go(text: str, account: str, media_pk: str, taken_at: datetime,
               img_filename: str | None = None, img_mime: str | None = None) -> bool:
    payload = {
        "text":      (text or "").strip(),
        "source":    "instagram",
        "account":   "@" + account,
        "posted_at": taken_at.astimezone(timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ"),
        "msg_id":    f"ig_{media_pk}",
        "api_key":   INGEST_API_KEY,
    }
    if img_filename:
        payload["media_path"] = img_filename
        payload["media_mime"] = img_mime or "image/jpeg"

    if not payload["text"] and not img_filename:
        return False

    try:
        r = requests.post(f"{GO_API_URL}/api/ingest", json=payload, timeout=10)
        data = r.json()
        if r.status_code == 200 and data.get("ok"):
            icon = "🖼 " if img_filename else "📝 "
            log.info(f"  ✓ {icon}@{account} | {data['id']} | job={data['is_job']} | {data.get('title','')!r}")
            return True
        # 409/duplicate is treated as success (already in DB)
        if r.status_code in (409,):
            return True
        log.warning(f"  ✗ @{account} | {r.status_code} | {data}")
        return False
    except Exception as e:
        log.error(f"  ✗ send_to_go: {e}")
        return False

# ── Poll one target account ───────────────────────────────────────────────────

def poll_target(cl: Client, bot_username: str, target: dict, seen: set) -> int:
    username = target["username"]
    n_posts  = int(target.get("posts", 20))
    new_count = 0

    try:
        user_id = cl.user_id_from_username(username)
        medias  = cl.user_medias(user_id, amount=n_posts)
    except UserNotFound:
        log.warning(f"  [{bot_username}] @{username} not found")
        return 0
    except RateLimitError:
        log.warning(f"  [{bot_username}] rate limited, sleeping 90s ...")
        time.sleep(90)
        return 0
    except LoginRequired:
        raise  # bubble up to invalidate client
    except Exception as e:
        log.error(f"  [{bot_username}] fetch @{username}: {e}")
        return 0

    for media in medias:
        pk = str(media.pk)
        if pk in seen:
            continue
        seen.add(pk)

        caption    = (media.caption_text or "").strip()
        media_type = getattr(media, "media_type", 1)

        img_filename, img_mime = None, None
        if media_type in (1, 8):  # photo or carousel
            img_filename, img_mime = download_image(media)

        if not caption and not img_filename:
            continue

        if send_to_go(caption, username, pk, media.taken_at, img_filename, img_mime):
            new_count += 1

        time.sleep(1.5)

    return new_count

# ── Main loop ─────────────────────────────────────────────────────────────────

def run_cycle(seen: set) -> int:
    cfg     = load_config()
    bots    = [a for a in cfg["scraper_accounts"] if a.get("enabled", True)]
    targets = [t for t in cfg["targets"]          if t.get("enabled", True)]

    if not bots:
        log.error("No enabled scraper_accounts in config.json")
        return 0
    if not targets:
        log.warning("No enabled targets in config.json")
        return 0

    log.info(f"── Cycle: {len(targets)} targets, {len(bots)} bot(s) ──")

    total_new = 0
    for i, target in enumerate(targets):
        # Round-robin across bot accounts
        bot_cfg = bots[i % len(bots)]
        bot_name = bot_cfg["username"]

        cl = get_client(bot_cfg)
        if cl is None:
            log.warning(f"  skip @{target['username']} (no valid session for {bot_name})")
            continue

        try:
            count = poll_target(cl, bot_name, target, seen)
            log.info(f"  @{target['username']} [{bot_name}]: {count} new")
            total_new += count
        except LoginRequired:
            log.warning(f"  [{bot_name}] login required, invalidating session ...")
            invalidate_client(bot_name)
            session_f = BASE_DIR / bot_cfg["session_file"]
            session_f.unlink(missing_ok=True)

        time.sleep(3)

    return total_new


def main():
    if not INGEST_API_KEY:
        log.error("INGEST_API_KEY not set in .env")
        sys.exit(1)
    if not CONFIG_FILE.exists():
        log.error(f"config.json not found at {CONFIG_FILE}")
        sys.exit(1)

    cfg = load_config()
    log.info(f"Starting IG scraper | config={CONFIG_FILE}")
    log.info(f"Poll interval: {cfg['poll_interval']}s | targets: {len(cfg['targets'])}")

    seen = load_seen()

    while True:
        try:
            total = run_cycle(seen)
            save_seen(seen)

            cfg = load_config()  # re-read for updated interval
            interval = cfg.get("poll_interval", 3600)
            log.info(f"── Done: {total} new posts | next sync in {interval}s ({interval//60}m) ──")

        except KeyboardInterrupt:
            log.info("Stopped by user")
            save_seen(seen)
            break

        except ChallengeRequired:
            log.error("Challenge required! Approve login from Instagram app on your phone, then restart.")
            sys.exit(1)

        except Exception as e:
            log.error(f"Cycle error: {e}", exc_info=True)
            time.sleep(30)
            continue

        time.sleep(interval)


if __name__ == "__main__":
    main()
