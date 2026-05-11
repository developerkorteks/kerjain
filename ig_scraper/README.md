# ig_scraper — Kerjain Instagram Scraper

Poll akun Instagram target, kirim caption post ke Go `/api/ingest`.

## Setup

```bash
cd ig_scraper
python -m venv venv
source venv/bin/activate
pip install -r requirements.txt
cp .env.example .env
# Edit .env: isi IG_USERNAME, IG_PASSWORD, INGEST_API_KEY, IG_TARGET_ACCOUNTS
```

## Jalankan

```bash
source venv/bin/activate
python main.py
```

## Config di .env

| Variable | Keterangan |
|---|---|
| `IG_USERNAME` | Username akun IG bot (buat akun baru, jangan akun pribadi) |
| `IG_PASSWORD` | Password akun IG bot |
| `GO_API_URL` | URL Go service, default `http://localhost:8080` |
| `INGEST_API_KEY` | Sama dengan `INGEST_API_KEY` di env Go |
| `IG_TARGET_ACCOUNTS` | Akun yang di-monitor, pisah koma, tanpa `@` |
| `POLL_INTERVAL` | Interval polling detik, default 600 (10 menit) |
| `POSTS_PER_ACCOUNT` | Jumlah post terakhir yang dicek per akun |

## INGEST_API_KEY

Set di Go backend `.env` (atau env variable):
```
INGEST_API_KEY=buat-random-string-panjang
```

Kalau tidak diset, Go akan generate key random dan print ke log saat startup.

## Tips Anti-ban

- Gunakan akun IG yang berumur > 1 bulan
- Set `POLL_INTERVAL` minimal 600 (jangan terlalu agresif)
- Jangan monitor lebih dari 10-15 akun sekaligus
- Kalau kena Challenge, buka Instagram di HP, approve login, lalu restart
