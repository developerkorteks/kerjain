import requests
from django.conf import settings

_BASE = settings.GO_API_URL


class APIError(Exception):
    pass


def _get(path, **params):
    try:
        clean = {k: v for k, v in params.items() if v not in (None, "", 0)}
        r = requests.get(f"{_BASE}{path}", params=clean, timeout=5)
        r.raise_for_status()
        return r.json()
    except requests.exceptions.ConnectionError:
        raise APIError("Layanan sedang tidak tersedia. Silakan coba lagi nanti.")
    except requests.exceptions.Timeout:
        raise APIError("Layanan lambat merespons. Silakan coba lagi dalam beberapa saat.")
    except Exception:
        raise APIError("Terjadi kesalahan. Silakan coba lagi nanti.")


def get_jobs(page=1, limit=12, status=None, msg_type=None, group=None, search=None, role=None, sort=None, date_from=None):
    data = _get("/api/jobs", page=page, limit=limit, status=status, type=msg_type, group=group, q=search, role=role, sort=sort, date_from=date_from)
    if data is None:
        return {"total": 0, "page": page, "limit": limit, "jobs": []}
    return data


def get_job(job_id):
    return _get(f"/api/jobs/{job_id}")


def get_groups():
    data = _get("/api/groups")
    return data or []


def get_enabled_groups():
    return [g for g in get_groups() if g.get("enabled")]
