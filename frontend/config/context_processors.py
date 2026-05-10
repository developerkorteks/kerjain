from django.conf import settings


def globals(request):
    return {
        "GO_API_URL": settings.GO_API_URL,
        "SITE_URL": settings.SITE_URL,
        "UMAMI_URL": settings.UMAMI_URL,
        "UMAMI_WEBSITE_ID": settings.UMAMI_WEBSITE_ID,
    }
