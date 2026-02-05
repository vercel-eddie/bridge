"""URL configuration for userservice project."""
from django.urls import path, include

urlpatterns = [
    path('api/', include('api.urls')),
]
