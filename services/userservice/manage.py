#!/usr/bin/env python
"""Django's command-line utility for administrative tasks."""
import os
import sys


def main():
    """Run administrative tasks."""
    os.environ.setdefault('DJANGO_SETTINGS_MODULE', 'userservice.settings')
    os.environ.setdefault('DJANGO_RUNSERVER_PORT', '3000')
    try:
        from django.core.management import execute_from_command_line
    except ImportError as exc:
        raise ImportError(
            "Couldn't import Django. Are you sure it's installed and "
            "available on your PYTHONPATH environment variable? Did you "
            "forget to activate a virtual environment?"
        ) from exc

    # Default runserver to port 3000
    if len(sys.argv) == 2 and sys.argv[1] == 'runserver':
        sys.argv.append('0.0.0.0:3000')

    execute_from_command_line(sys.argv)


if __name__ == '__main__':
    main()
