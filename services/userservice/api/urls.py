from django.urls import path
from . import views

urlpatterns = [
    path('health/', views.health, name='health'),
    path('users/', views.users_list, name='users-list'),
    path('users/<int:user_id>/', views.user_detail, name='user-detail'),
]
