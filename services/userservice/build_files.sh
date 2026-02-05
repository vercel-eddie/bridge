#!/bin/bash

# Build script for Vercel deployment
pip install -r requirements.txt
python manage.py collectstatic --noinput
