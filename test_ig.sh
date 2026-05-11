#!/usr/bin/env bash
# test_ig.sh — Troubleshoot IG scraper paths & API (no blocking Python calls)
# Usage: bash test_ig.sh [api_key] [base_url]
set -o pipefail

API_KEY="${1:-kerjain-ingest-2026}"
BASE_URL="${2:-http://localhost:60880}"

RED='\033[0;31m'; GREEN='\033[0;32m'; YELLOW='\033[1;33m'
CYAN='\033[0;36m'; BOLD='\033[1m'; RESET='\033[0m'

ok()   { echo -e "  ${GREEN}✓${RESET} $1"; }
fail() { echo -e "  ${RED}✗${RESET} $1"; FAIL=$((FAIL+1)); }
warn() { echo -e "  ${YELLOW}!${RESET} $1"; }
hdr()  { echo -e "\n${BOLD}${CYAN}── $1 ──${RESET}"; }
FAIL=0

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
IG_DIR="${IG_SCRAPER_DIR:-$SCRIPT_DIR/ig_scraper}"

# ── 1. PATHS ──────────────────────────────────────────────────────────────────
hdr "1. PATH CHECK"
echo "  Script dir    : $SCRIPT_DIR"
echo "  IG_SCRAPER_DIR: ${IG_SCRAPER_DIR:-(not set, auto)}"
echo "  Resolved IG   : $IG_DIR"

[ -f "$SCRIPT_DIR/lokerwa" ] && ok "binary lokerwa ada" || fail "binary lokerwa TIDAK ADA di $SCRIPT_DIR"
[ -d "$IG_DIR" ]             && ok "ig_scraper/ ada: $IG_DIR" || fail "ig_scraper/ TIDAK ADA: $IG_DIR"

# Python venv (hanya cek file, TIDAK dieksekusi)
PYTHON=""
for name in python python3; do
  P="$IG_DIR/venv/bin/$name"
  if [ -f "$P" ]; then
    PYTHON="$P"
    ok "venv python: $P"
    # cek symlink valid
    if [ -L "$P" ] && [ ! -e "$P" ]; then
      fail "Symlink rusak: $P → $(readlink $P)"
    else
      TARGET=$(readlink -f "$P" 2>/dev/null)
      [ -n "$TARGET" ] && ok "  → $TARGET"
    fi
    break
  fi
done
[ -z "$PYTHON" ] && fail "venv/bin/python TIDAK ADA di $IG_DIR/venv/bin/"

# ── 2. FILE CHECK ─────────────────────────────────────────────────────────────
hdr "2. FILE CHECK"
for f in main.py add_session.py check_sessions.py requirements.txt; do
  [ -f "$IG_DIR/$f" ] && ok "$f" || fail "$f TIDAK ADA"
done

# config.json (parse dengan python3 system, bukan venv)
if [ -f "$IG_DIR/config.json" ]; then
  ok "config.json ada"
  if python3 -m json.tool "$IG_DIR/config.json" > /dev/null 2>&1; then
    ok "config.json valid JSON"
    BOT_COUNT=$(python3 -c "import json; d=json.load(open('$IG_DIR/config.json')); print(len(d.get('scraper_accounts',[])))")
    TGT_COUNT=$(python3 -c "import json; d=json.load(open('$IG_DIR/config.json')); print(len(d.get('targets',[])))")
    ok "scraper_accounts: $BOT_COUNT | targets: $TGT_COUNT"
  else
    fail "config.json JSON INVALID"
  fi
else
  fail "config.json TIDAK ADA"
fi

# Session files
hdr "3. SESSION FILES"
if [ -f "$IG_DIR/config.json" ]; then
  SESSION_COUNT=0
  while IFS= read -r sfile; do
    [ -z "$sfile" ] && continue
    FPATH="$IG_DIR/$sfile"
    if [ -f "$FPATH" ]; then
      SZ=$(wc -c < "$FPATH")
      ok "$sfile (${SZ}B)"
      SESSION_COUNT=$((SESSION_COUNT+1))
    else
      fail "$sfile TIDAK ADA → login ulang via dashboard"
    fi
  done < <(python3 -c "
import json; d=json.load(open('$IG_DIR/config.json'))
[print(a.get('session_file','')) for a in d.get('scraper_accounts',[])]
" 2>/dev/null)
  [ "$SESSION_COUNT" -eq 0 ] && warn "Belum ada session — login via 📸 IG di dashboard"
fi

# pip packages — gunakan pip list (cepat, tidak import)
hdr "4. PYTHON PACKAGES"
PIP="$IG_DIR/venv/bin/pip"
if [ -f "$PIP" ]; then
  PIP_LIST=$("$PIP" list --format=columns 2>/dev/null)
  echo "$PIP_LIST" | grep -qi instagrapi && ok "instagrapi installed" \
    || fail "instagrapi TIDAK TERINSTALL → $PIP install -r $IG_DIR/requirements.txt"
  echo "$PIP_LIST" | grep -qi requests    && ok "requests installed" \
    || fail "requests TIDAK TERINSTALL"
else
  fail "pip tidak ditemukan di $PIP"
fi

# ── 5. GO API ─────────────────────────────────────────────────────────────────
hdr "5. GO API"
CODE=$(curl -sm5 -o /dev/null -w "%{http_code}" "$BASE_URL/health")
if [ "$CODE" = "200" ]; then
  ok "Go server UP ($BASE_URL)"
else
  fail "Go server DOWN — HTTP $CODE"
  echo -e "\n${RED}Skip API tests${RESET}"
  hdr "SUMMARY"; echo -e "${RED}${BOLD}✗ $FAIL masalah${RESET}"; exit 1
fi

# /api/ig/config
RESP=$(curl -sm5 "$BASE_URL/api/ig/config?api_key=$API_KEY")
CODE=$(curl -sm5 -o /dev/null -w "%{http_code}" "$BASE_URL/api/ig/config?api_key=$API_KEY")
[ "$CODE" = "200" ] && ok "GET /api/ig/config → 200" || fail "GET /api/ig/config → $CODE"

# /api/ig/status
RESP=$(curl -sm5 "$BASE_URL/api/ig/status?api_key=$API_KEY")
CODE=$(curl -sm5 -o /dev/null -w "%{http_code}" "$BASE_URL/api/ig/status?api_key=$API_KEY")
if [ "$CODE" = "200" ]; then
  ok "GET /api/ig/status → 200"
  echo "$RESP" | python3 -c "
import json,sys
for a in json.load(sys.stdin):
    icon = {'ok':'🟢','expired':'🟡','missing':'🔴'}.get(a['status'],'⚪')
    print(f'     {icon} @{a[\"username\"]} [{a[\"status\"]}] {a[\"message\"]}')
" 2>/dev/null
else
  fail "GET /api/ig/status → $CODE ($RESP)"
fi

# /api/ig/add-session path check via Go log (kirim kosong, expect Python error bukan 500 path error)
RESP=$(curl -sm10 -X POST "$BASE_URL/api/ig/add-session?api_key=$API_KEY" \
  -H "Content-Type: application/json" -d '[]')
CODE=$(curl -sm10 -o /dev/null -w "%{http_code}" -X POST "$BASE_URL/api/ig/add-session?api_key=$API_KEY" \
  -H "Content-Type: application/json" -d '[]')
if echo "$RESP" | grep -q "no such file or directory"; then
  fail "POST /api/ig/add-session → PATH ERROR: $RESP"
  warn "Set IG_SCRAPER_DIR=$IG_DIR sebelum jalankan lokerwa"
elif [ "$CODE" = "200" ]; then
  ok "POST /api/ig/add-session → 200 (Python reachable)"
else
  warn "POST /api/ig/add-session → $CODE: $RESP"
fi

# ── SUMMARY ───────────────────────────────────────────────────────────────────
hdr "SUMMARY"
if [ "$FAIL" -eq 0 ]; then
  echo -e "${GREEN}${BOLD}  ✓ Semua OK — siap deploy!${RESET}"
else
  echo -e "${RED}${BOLD}  ✗ $FAIL masalah ditemukan${RESET}"
  echo ""
  echo "  Tips deploy:"
  echo "    export IG_SCRAPER_DIR=/path/to/ig_scraper"
  echo "    cd /path/to/ig_scraper && venv/bin/pip install -r requirements.txt"
fi
echo ""
