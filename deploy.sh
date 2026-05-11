#!/usr/bin/env bash
# deploy.sh — setup lokerwa di server baru
# Usage: bash deploy.sh [deploy_dir]
# Example: bash deploy.sh /opt/lokerwa

set -e

DEPLOY_DIR="${1:-/opt/lokerwa}"
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"

echo "==> Deploy ke: $DEPLOY_DIR"
mkdir -p "$DEPLOY_DIR/data/media"

# 1. Copy binary
echo "==> Copy binary..."
cp "$SCRIPT_DIR/lokerwa" "$DEPLOY_DIR/lokerwa"
chmod +x "$DEPLOY_DIR/lokerwa"

# 2. Copy ig_scraper (tanpa venv dan file sensitif)
echo "==> Copy ig_scraper/..."
rsync -av --exclude='venv/' --exclude='.env' --exclude='session*.json' \
  --exclude='seen_ids.json' \
  "$SCRIPT_DIR/ig_scraper/" "$DEPLOY_DIR/ig_scraper/"

# 3. Setup Python venv di server
echo "==> Setup Python venv..."
cd "$DEPLOY_DIR/ig_scraper"
python3 -m venv venv
venv/bin/pip install --quiet -r requirements.txt
echo "==> Python deps installed"

# 4. Buat .env jika belum ada
if [ ! -f "$DEPLOY_DIR/ig_scraper/.env" ]; then
  cp "$DEPLOY_DIR/ig_scraper/.env.example" "$DEPLOY_DIR/ig_scraper/.env"
  echo "==> .env dibuat dari .env.example — EDIT sebelum run!"
fi

# 5. Buat config.json default jika belum ada
if [ ! -f "$DEPLOY_DIR/ig_scraper/config.json" ]; then
  cat > "$DEPLOY_DIR/ig_scraper/config.json" <<'EOF'
{
  "poll_interval": 3600,
  "scraper_accounts": [],
  "targets": []
}
EOF
  echo "==> config.json default dibuat"
fi

echo ""
echo "✓ Deploy selesai!"
echo ""
echo "Langkah selanjutnya:"
echo "  1. Edit $DEPLOY_DIR/ig_scraper/.env"
echo "     Set: GO_API_URL, INGEST_API_KEY"
echo ""
echo "  2. Set env vars untuk Go binary:"
echo "     export INGEST_API_KEY=your_key"
echo "     export BOARD_USER=admin"
echo "     export BOARD_PASS=your_pass"
echo "     export IG_SCRAPER_DIR=$DEPLOY_DIR/ig_scraper"
echo ""
echo "  3. Jalankan binary:"
echo "     cd $DEPLOY_DIR && ./lokerwa"
echo "     (default port: 60880, override: PORT=xxxxx ./lokerwa)"
echo ""
echo "  4. Login bot akun via dashboard:"
echo "     http://your-server:60880/board → klik 📸 IG"
