"""Notifications module - Telegram bot and notification routing.

Provides the INotificationService interface and NotificationRouter implementation
for delivering task notifications, approval requests, and system alerts.
"""

from orchestrator.notifications.interfaces import (
    INotificationService,
    Notification,
    NotificationType,
)
from orchestrator.notifications.router import NotificationRouter

__all__ = [
    "INotificationService",
    "Notification",
    "NotificationType",
    "NotificationRouter",
]

# TelegramBot is imported conditionally since it may not exist yet (task 11.1)
try:
    from orchestrator.notifications.telegram_bot import TelegramBot

    __all__.append("TelegramBot")
except ImportError:
    pass
