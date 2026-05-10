import re
from datetime import datetime, timezone, timedelta
from django import template
from django.utils.text import slugify
from django.utils.timezone import now as tz_now

register = template.Library()

# ── Card palette (soft pastel, no harsh colors) ──────────────────────────────
_PALETTES = [
    {"bg": "#f8fafc", "accent": "#e2e8f0"},   # cool white
    {"bg": "#f5f3ff", "accent": "#ede9fe"},   # lavender
    {"bg": "#eff6ff", "accent": "#dbeafe"},   # baby blue
    {"bg": "#fff7ed", "accent": "#fed7aa"},   # warm cream
    {"bg": "#f0fdf4", "accent": "#bbf7d0"},   # mint
    {"bg": "#fdf4ff", "accent": "#f5d0fe"},   # blush
    {"bg": "#f0f9ff", "accent": "#bae6fd"},   # sky
    {"bg": "#fefce8", "accent": "#fef08a"},   # cream yellow
]


@register.filter
def card_palette(index):
    p = _PALETTES[int(index) % len(_PALETTES)]
    return f"background:{p['bg']};"


@register.filter
def initial_style(index):
    p = _PALETTES[int(index) % len(_PALETTES)]
    return f"background:{p['accent']};"


# ── Smart title ───────────────────────────────────────────────────────────────
@register.filter
def smart_title(job):
    if job.get("title"):
        return job["title"]
    raw = (job.get("raw_text") or "").strip()
    import re
    for line in raw.splitlines():
        line = re.sub(r"^[\s*_#•\-\d.]+", "", line).strip()
        if len(line) > 6:
            return line[:72]
    return "Lowongan Kerja"


@register.filter
def job_slug(job):
    """Return a URL-safe slug from the job title, e.g. 'kasir-semarang'."""
    title = job.get("title") or ""
    if not title:
        raw = (job.get("raw_text") or "").strip()
        for line in raw.splitlines():
            line = re.sub(r"^[\s*_#•\-\d.]+", "", line).strip()
            if len(line) > 6:
                title = line[:60]
                break
    return slugify(title) or "lowongan-kerja"


# ── Body preview ──────────────────────────────────────────────────────────────
@register.filter
def short_body(job, length=150):
    import re
    text = (job.get("raw_text") or "").strip()
    text = re.sub(r"[\*\_\#]+", "", text)
    clean = " ".join(text.split())
    return clean[:length] + ("…" if len(clean) > length else "")


# ── Expired check ─────────────────────────────────────────────────────────────
@register.filter
def is_expired(posted_at, days=30):
    if not posted_at:
        return False
    try:
        if isinstance(posted_at, str):
            dt = datetime.fromisoformat(posted_at.replace("Z", "+00:00"))
        else:
            dt = posted_at
        return (datetime.now(timezone.utc) - dt) > timedelta(days=int(days))
    except Exception:
        return False


# ── Relative time ─────────────────────────────────────────────────────────────
@register.filter
def relative_time(posted_at):
    if not posted_at:
        return ""
    try:
        if isinstance(posted_at, str):
            dt = datetime.fromisoformat(posted_at.replace("Z", "+00:00"))
        else:
            dt = posted_at
        diff = datetime.now(timezone.utc) - dt
        if diff < timedelta(minutes=1):
            return "baru saja"
        if diff < timedelta(hours=1):
            return f"{int(diff.total_seconds()//60)} menit lalu"
        if diff < timedelta(days=1):
            return f"{int(diff.total_seconds()//3600)} jam lalu"
        if diff < timedelta(days=7):
            return f"{diff.days} hari lalu"
        return dt.strftime("%d %b %Y")
    except Exception:
        return str(posted_at)


# ── Status / Type labels (no emoji) ──────────────────────────────────────────
@register.filter
def status_label(status):
    return {"raw": "Baru", "review": "Review", "valid": "Terverifikasi"}.get(status, status)


@register.filter
def status_style(status):
    return {
        "raw":    "color:#64748b;background:#f1f5f9;border-color:#e2e8f0",
        "review": "color:#b45309;background:#fffbeb;border-color:#fde68a",
        "valid":  "color:#059669;background:#f0fdf4;border-color:#bbf7d0",
    }.get(status, "color:#64748b;background:#f1f5f9;border-color:#e2e8f0")


@register.filter
def type_label(msg_type):
    return {"text": "Teks", "image": "Gambar"}.get(msg_type, msg_type)


@register.filter
def type_style(msg_type):
    return {
        "text":  "color:#2563eb;background:#eff6ff;border-color:#bfdbfe",
        "image": "color:#7c3aed;background:#f5f3ff;border-color:#ddd6fe",
    }.get(msg_type, "color:#64748b;background:#f8fafc;border-color:#e2e8f0")


# ── Avatar initial & color ────────────────────────────────────────────────────
@register.filter
def avatar_letter(job):
    name = job.get("company") or job.get("group_name") or job.get("sender_name") or "L"
    return name.strip()[0].upper()


# ── Detect smart badges from raw text ────────────────────────────────────────
_BADGE_RULES = [
    (["urgent", "urgently", "dibutuhkan segera", "asap", "segera"],
     "Urgent", "color:#dc2626;background:#fff1f2;border-color:#fecdd3"),
    (["remote", "wfh", "work from home"],
     "Remote", "color:#059669;background:#f0fdf4;border-color:#bbf7d0"),
    (["hybrid"],
     "Hybrid", "color:#7c3aed;background:#f5f3ff;border-color:#ddd6fe"),
    (["full time", "fulltime", "full-time"],
     "Full Time", "color:#2563eb;background:#eff6ff;border-color:#bfdbfe"),
    (["part time", "parttime", "part-time"],
     "Part Time", "color:#d97706;background:#fffbeb;border-color:#fde68a"),
    (["magang", "internship", "intern"],
     "Magang", "color:#db2777;background:#fdf2f8;border-color:#fbcfe8"),
    (["freelance"],
     "Freelance", "color:#0891b2;background:#ecfeff;border-color:#a5f3fc"),
]


@register.filter
def detect_badges(job):
    corpus = " ".join([
        (job.get("raw_text") or ""),
        (job.get("title") or ""),
        (job.get("work_hours") or ""),
    ]).lower()
    found = []
    for keywords, label, style in _BADGE_RULES:
        if any(k in corpus for k in keywords):
            found.append({"label": label, "style": style})
            if len(found) >= 3:
                break
    return found


# ── Extract fallback contact from raw_text ────────────────────────────────────
_PHONE_RE = re.compile(
    r'(?:wa\.me/|whatsapp\.com/send\?phone=|'      # wa.me links
    r'wa\s*:\s*|WA\s*:\s*|wa\.?)\s*'               # "WA: " prefixes
    r'(\+?62\d{8,13}|0\d{8,12})'                   # +62.../62.../08...
    r'|'
    r'(\+62\d{8,13}|0\d{8,12})'                    # plain phone anywhere
    , re.IGNORECASE
)

def _normalize_phone(raw: str) -> str:
    d = re.sub(r'\D', '', raw)
    if d.startswith('0'):
        d = '62' + d[1:]
    elif not d.startswith('62'):
        d = '62' + d
    return d


@register.filter
def extract_contact(job):
    """Return dict with wa_url/phone or email from job contact or raw_text."""
    contact = job.get('contact') or ''
    ctype = (job.get('contact_type') or '').lower()

    if contact:
        if ctype == 'email' or '@' in contact:
            return {'type': 'email', 'label': contact,
                    'url': f'mailto:{contact}', 'source': 'field'}
        phone = _normalize_phone(contact)
        if len(re.sub(r'\D', '', phone)) >= 9:
            return {'type': 'wa', 'phone': phone,
                    'wa_url': f'https://wa.me/{phone}',
                    'label': contact, 'source': 'field'}

    text = (job.get('raw_text') or '') + ' ' + (job.get('caption') or '')
    for m in _PHONE_RE.finditer(text):
        raw = m.group(1) or m.group(2) or ''
        if not raw:
            continue
        digits = re.sub(r'\D', '', raw)
        if len(digits) < 9:
            continue
        phone = _normalize_phone(raw)
        return {'type': 'wa', 'phone': phone,
                'wa_url': f'https://wa.me/{phone}',
                'label': raw, 'source': 'raw_text'}

    # Last resort: sender_phone from WhatsApp metadata (resolved from LID)
    sender_phone = re.sub(r'\D', '', job.get('sender_phone') or '')
    if len(sender_phone) >= 9:
        phone = _normalize_phone(sender_phone)
        return {'type': 'wa', 'phone': phone,
                'wa_url': f'https://wa.me/{phone}',
                'label': sender_phone, 'source': 'sender'}

    return None


# ── Pagination ────────────────────────────────────────────────────────────────
@register.filter
def slice_pages(total_pages, current_page):
    pages, prev = [], None
    for p in range(1, total_pages + 1):
        if p == 1 or p == total_pages or abs(p - current_page) <= 2:
            if prev and p - prev > 1:
                pages.append("...")
            pages.append(p)
            prev = p
    return pages
