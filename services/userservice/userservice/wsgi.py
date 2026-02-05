"""
WSGI config for userservice project.
"""
import os

from django.core.wsgi import get_wsgi_application

os.environ.setdefault('DJANGO_SETTINGS_MODULE', 'userservice.settings')

application = get_wsgi_application()

# Vercel requires the app to be named 'app'
app = application
