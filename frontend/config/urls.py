from django.urls import path, include
from django.shortcuts import render
from jobs import views as job_views


def handler404(request, exception=None):
    return render(request, "404.html", status=404)


urlpatterns = [
    path("robots.txt", job_views.robots_txt),
    path("sitemap.xml", job_views.sitemap_xml),
    path("", include("jobs.urls")),
]

handler404 = handler404
