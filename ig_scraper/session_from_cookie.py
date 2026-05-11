"""
Login ke instagrapi menggunakan sessionid dari browser cookie.
"""
import sys
from pathlib import Path
from urllib.parse import unquote

# sessionid dari cookie browser (URL-decoded otomatis)
SESSIONID = unquote("48740203062%3AivCcWtPTyWetIZ%3A15%3AAYiFB12T7iHwNogYvdUmOnaVaQSncinDrf4NOZMQBA")
SESSION_FILE = Path(__file__).parent / "session.json"

# Tambahan cookie untuk memperkuat session
COOKIES = {
    "sessionid":  SESSIONID,
    "ds_user_id": "48740203062",
    "csrftoken":  "Ei9X6oabjIADMuIn2UqajhPTlxgeYE0I",
    "mid":        "aeyNHAAEAAER2de8DL-i9bMx9m9R",
    "ig_did":     "8F53B5D1-A172-4DF8-BA14-7050E15A6CFB",
    "datr":       "GI3safTbfW9oZCENx8lxgqUs",
}

try:
    from instagrapi import Client
except ImportError:
    print("Run: pip install instagrapi")
    sys.exit(1)

print(f"Session ID: {SESSIONID[:30]}...")
print("Logging in via sessionid...")

cl = Client()
try:
    cl.login_by_sessionid(SESSIONID)
    cl.dump_settings(SESSION_FILE)
    print(f"\n✓ Login berhasil sebagai: {cl.username}")
    print(f"✓ session.json tersimpan di: {SESSION_FILE.absolute()}")
except Exception as e:
    print(f"\n✗ Error: {e}")
    sys.exit(1)
