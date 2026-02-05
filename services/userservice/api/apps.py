import os
import sys

from django.apps import AppConfig


class ApiConfig(AppConfig):
    default_auto_field = 'django.db.models.BigAutoField'
    name = 'api'
    _migrated = False

    def ready(self):
        # Skip if already migrated, running migrations command, or in tests
        if (
            ApiConfig._migrated
            or 'migrate' in sys.argv
            or 'makemigrations' in sys.argv
            or 'test' in sys.argv
        ):
            return

        # Auto-migrate on startup
        if os.getenv('AUTO_MIGRATE', 'true').lower() == 'true':
            from django.core.management import call_command
            try:
                call_command('migrate', '--run-syncdb', verbosity=1)
                ApiConfig._migrated = True
            except Exception as e:
                print(f"Auto-migration failed: {e}")
