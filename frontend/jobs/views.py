from datetime import date, timedelta

from django.conf import settings
from django.http import HttpResponse
from django.shortcuts import render, redirect
import requests as _req

from . import services
from .services import APIError


def media_proxy(request, path):
    try:
        r = _req.get(f"{settings.GO_API_URL}/media/{path}", timeout=5, stream=True)
        content_type = r.headers.get("Content-Type", "image/jpeg")
        return HttpResponse(r.content, content_type=content_type)
    except Exception:
        return HttpResponse(status=404)


def _api_error(request, message):
    return render(request, "error.html", {"message": message}, status=503)


def home(request):
    total_jobs = 0
    today_jobs = 0
    recent_jobs = []
    try:
        result = services.get_jobs(page=1, limit=6, status="valid")
        total_jobs = result.get("total", 0)
        recent_jobs = result.get("jobs", [])
    except APIError:
        pass
    try:
        today_jobs = services.get_jobs(page=1, limit=1, status="valid", date_from=str(date.today())).get("total", 0)
    except APIError:
        pass
    return render(request, "home.html", {
        "total_jobs": total_jobs,
        "today_jobs": today_jobs,
        "recent_jobs": recent_jobs,
    })


def job_list(request):
    page = max(1, int(request.GET.get("page", 1)))
    status = "valid"  # public list always shows verified jobs only
    msg_type = request.GET.get("type", "")
    group = request.GET.get("group", "")
    query = request.GET.get("q", "")
    sort = request.GET.get("sort", "newest")
    date_filter = request.GET.get("date", "")  # "today" | "week" | ""

    date_from = None
    if date_filter == "today":
        date_from = str(date.today())
    elif date_filter == "week":
        date_from = str(date.today() - timedelta(days=7))

    limit = settings.JOBS_PER_PAGE
    try:
        data = services.get_jobs(
            page=page,
            limit=limit,
            status=status,
            msg_type=msg_type or None,
            group=group or None,
            search=query or None,
            sort=sort if sort in ("newest", "oldest") else "newest",
            date_from=date_from,
        )
        enabled_groups = services.get_enabled_groups()
    except APIError as e:
        return _api_error(request, str(e))

    jobs = data.get("jobs") or []
    total = data.get("total", 0)
    total_pages = max(1, (total + limit - 1) // limit)

    return render(request, "jobs/list.html", {
        "jobs": jobs,
        "total": total,
        "page": page,
        "total_pages": total_pages,
        "has_prev": page > 1,
        "has_next": page < total_pages,
        "enabled_groups": enabled_groups,
        "current_status": "",  # always valid, no status filter exposed
        "current_type": msg_type,
        "current_group": group,
        "current_query": query,
        "current_sort": sort,
        "current_date": date_filter,
        "per_page": limit,
    })


def about(request):
    return render(request, "about.html")


def job_detail(request, job_id, job_slug=None):
    try:
        job = services.get_job(job_id)
    except APIError as e:
        return _api_error(request, str(e))
    if not job:
        return redirect("job_list")
    return render(request, "jobs/detail.html", {"job": job})


def job_detail_legacy(request, job_id):
    """Redirect old /jobs/{id}/ URLs to new SEO-friendly URL."""
    try:
        job = services.get_job(job_id)
    except APIError:
        return redirect("job_list")
    if not job:
        return redirect("job_list")
    from django.utils.text import slugify
    import re
    title = job.get("title") or ""
    if not title:
        raw = (job.get("raw_text") or "").strip()
        for line in raw.splitlines():
            line = re.sub(r"^[\s*_#•\-\d.]+", "", line).strip()
            if len(line) > 6:
                title = line[:60]
                break
    slug = slugify(title) or "lowongan-kerja"
    return redirect("job_detail", job_slug=slug, job_id=job_id, permanent=True)


def robots_txt(request):
    site = settings.SITE_URL
    content = f"""User-agent: *
Allow: /
Disallow: /board
Disallow: /api-test
Disallow: /api/

Sitemap: {site}/sitemap.xml
"""
    return HttpResponse(content, content_type="text/plain")


def sitemap_xml(request):
    site = settings.SITE_URL
    try:
        data = services.get_jobs(page=1, limit=500, status="valid")
        jobs = data.get("jobs") or []
    except APIError:
        jobs = []

    urls = [
        {"loc": f"{site}/", "changefreq": "daily", "priority": "1.0"},
        {"loc": f"{site}/loker/", "changefreq": "hourly", "priority": "0.9"},
        {"loc": f"{site}/tentang/", "changefreq": "monthly", "priority": "0.5"},
    ]
    import re
    from django.utils.text import slugify as _slugify

    def _slug(job):
        title = job.get("title") or ""
        if not title:
            raw = (job.get("raw_text") or "").strip()
            for line in raw.splitlines():
                line = re.sub(r"^[\s*_#•\-\d.]+", "", line).strip()
                if len(line) > 6:
                    title = line[:60]
                    break
        return _slugify(title) or "lowongan-kerja"

    for job in jobs:
        posted = (job.get("posted_at") or "")[:10]
        urls.append({
            "loc": f"{site}/loker/{_slug(job)}/{job['id']}/",
            "lastmod": posted,
            "changefreq": "weekly",
            "priority": "0.7",
        })

    lines = ['<?xml version="1.0" encoding="UTF-8"?>',
             '<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">']
    for u in urls:
        lines.append("  <url>")
        lines.append(f"    <loc>{u['loc']}</loc>")
        if u.get("lastmod"):
            lines.append(f"    <lastmod>{u['lastmod']}</lastmod>")
        lines.append(f"    <changefreq>{u['changefreq']}</changefreq>")
        lines.append(f"    <priority>{u['priority']}</priority>")
        lines.append("  </url>")
    lines.append("</urlset>")

    return HttpResponse("\n".join(lines), content_type="application/xml")
