import json

from django.http import JsonResponse
from django.views.decorators.http import require_http_methods
from django.views.decorators.csrf import csrf_exempt
from django.core.exceptions import ValidationError
from django.db import IntegrityError

from .models import User


def health(request):
    """Health check endpoint."""
    return JsonResponse({'status': 'ok'})


@csrf_exempt
@require_http_methods(['GET', 'POST'])
def users_list(request):
    """List all users or create a new user."""
    if request.method == 'GET':
        users = User.objects.all()
        return JsonResponse({
            'users': [user.to_dict() for user in users]
        })

    elif request.method == 'POST':
        try:
            data = json.loads(request.body)
        except json.JSONDecodeError:
            return JsonResponse({'error': 'Invalid JSON'}, status=400)

        required_fields = ['email', 'username']
        for field in required_fields:
            if field not in data:
                return JsonResponse({'error': f'Missing required field: {field}'}, status=400)

        try:
            user = User.objects.create(
                email=data['email'],
                username=data['username'],
                first_name=data.get('first_name', ''),
                last_name=data.get('last_name', ''),
            )
            return JsonResponse(user.to_dict(), status=201)
        except IntegrityError as e:
            if 'email' in str(e):
                return JsonResponse({'error': 'Email already exists'}, status=409)
            elif 'username' in str(e):
                return JsonResponse({'error': 'Username already exists'}, status=409)
            return JsonResponse({'error': 'User already exists'}, status=409)
        except ValidationError as e:
            return JsonResponse({'error': str(e)}, status=400)


@csrf_exempt
@require_http_methods(['GET', 'PUT', 'DELETE'])
def user_detail(request, user_id):
    """Get, update, or delete a specific user."""
    try:
        user = User.objects.get(id=user_id)
    except User.DoesNotExist:
        return JsonResponse({'error': 'User not found'}, status=404)

    if request.method == 'GET':
        return JsonResponse(user.to_dict())

    elif request.method == 'PUT':
        try:
            data = json.loads(request.body)
        except json.JSONDecodeError:
            return JsonResponse({'error': 'Invalid JSON'}, status=400)

        if 'email' in data:
            user.email = data['email']
        if 'username' in data:
            user.username = data['username']
        if 'first_name' in data:
            user.first_name = data['first_name']
        if 'last_name' in data:
            user.last_name = data['last_name']
        if 'is_active' in data:
            user.is_active = data['is_active']

        try:
            user.save()
            return JsonResponse(user.to_dict())
        except IntegrityError as e:
            if 'email' in str(e):
                return JsonResponse({'error': 'Email already exists'}, status=409)
            elif 'username' in str(e):
                return JsonResponse({'error': 'Username already exists'}, status=409)
            return JsonResponse({'error': 'Update failed'}, status=409)

    elif request.method == 'DELETE':
        user.delete()
        return JsonResponse({'message': 'User deleted'}, status=200)
