from django.urls import path
from django.views.generic import RedirectView
from . import views

urlpatterns = [
    path("", views.home, name="home"),
    path("loker/", views.job_list, name="job_list"),
    path("loker/<slug:job_slug>/<str:job_id>/", views.job_detail, name="job_detail"),
    path("jobs/<str:job_id>/", views.job_detail_legacy, name="job_detail_legacy"),
    path("tentang/", views.about, name="about"),
    path("media/<path:path>", views.media_proxy, name="media_proxy"),
]
