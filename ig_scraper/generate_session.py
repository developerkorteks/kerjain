"""
Jalankan script ini di laptop/PC kamu (bukan server) untuk generate session.json
Lalu copy session.json ke server di folder ig_scraper/
"""
from instagrapi import Client
from pathlib import Path
import sys

username = input("IG Username: ").strip()
password = input("IG Password: ").strip()

cl = Client()
print("Logging in...")
try:
    cl.login(username, password)
    session_file = Path("session.json")
    cl.dump_settings(session_file)
    print(f"\n✓ Login berhasil! session.json tersimpan di: {session_file.absolute()}")
    print("\nSekarang copy file ini ke server:")
    print(f"  scp session.json user@servermu:/home/korteks/Data/project/lokerwa/ig_scraper/session.json")
except Exception as e:
    print(f"\n✗ Error: {e}")
    sys.exit(1)
