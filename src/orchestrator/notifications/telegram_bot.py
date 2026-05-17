"""Telegram bot for remote control and notifications.

Implements long-polling connection to the Telegram Bot API with commands
for task management, approval handling, and system control. Access is
restricted to configured Telegram user IDs.

The bot runs as part of the orchestrator process (same systemd service)
and communicates with internal services via direct function calls, not HTTP.

Requirements: 13.1, 13.3, 13.4, 13.5
"""

from __future__ import annotations

import logging
from typing import Any, Protocol

from telegram import InlineKeyboardButton, InlineKeyboardMarkup, Update
from telegram.ext import (
    Application,
    CallbackQueryHandler,
    CommandHandler,
    ContextTypes,
    MessageHandler,
    filters,
)

logger = logging.getLogger(__name__)


# ---------------------------------------------------------------------------
# Service protocols — satisfied by internal orchestrator services
# ---------------------------------------------------------------------------


class ITaskService(Protocol):
    """Protocol for task query operations (satisfied by TaskRepository)."""

    async def get_task(self, task_id: str) -> dict[str, Any] | None:
        """Get task details by ID."""
        ...

    async def list_active_tasks(self) -> list[dict[str, Any]]:
        """List tasks in non-terminal states."""
        ...

    async def cancel_task(self, task_id: str, actor: str) -> bool:
        """Cancel a task. Returns True if successful."""
        ...

    async def create_task(
        self,
        description: str,
        *,
        assigned_agent: str | None = None,
        plan_only: bool = False,
        entrypoint: str = "telegram",
    ) -> dict[str, Any]:
        """Create and orchestrate a task from Telegram."""
        ...


class IApprovalService(Protocol):
    """Protocol for approval operations."""

    async def approve(self, approval_id: str, operator: str) -> bool:
        """Approve a pending approval. Returns True if successful."""
        ...

    async def reject(self, approval_id: str, operator: str) -> bool:
        """Reject a pending approval. Returns True if successful."""
        ...

    async def list_pending(self) -> list[dict[str, Any]]:
        """List pending approvals."""
        ...


class ISafeModeService(Protocol):
    """Protocol for safe mode toggle."""

    @property
    def enabled(self) -> bool: ...

    @enabled.setter
    def enabled(self, value: bool) -> None: ...


class ISystemStatusService(Protocol):
    """Protocol for system status queries."""

    async def get_status(self) -> dict[str, Any]:
        """Get system overview: active tasks, queue depths, worker status."""
        ...


class IChatService(Protocol):
    """Protocol for direct chat with local models."""

    async def chat(self, target: str, prompt: str) -> str:
        """Send a prompt to a named local model target and return its reply."""
        ...


class IServerOpsService(Protocol):
    """Protocol for controlled server inspection actions."""

    async def run(self, action: str, argument: str | None = None) -> str:
        """Execute a supported server action and return a user-facing result."""
        ...


# ---------------------------------------------------------------------------
# Access restriction filter
# ---------------------------------------------------------------------------


class AllowedUsersFilter(filters.BaseFilter):
    """Filter that only allows messages from configured user IDs."""

    def __init__(self, allowed_user_ids: list[int]) -> None:
        super().__init__()
        self._allowed_ids = frozenset(allowed_user_ids)

    def filter(self, message: Any) -> bool:
        """Return True if the message sender is in the allowed list."""
        if message is None or message.from_user is None:
            return False
        return message.from_user.id in self._allowed_ids


# ---------------------------------------------------------------------------
# Telegram Bot
# ---------------------------------------------------------------------------


class TelegramBot:
    """Telegram bot with long-polling for remote orchestrator control.

    Provides commands for task management, approval handling, and system
    control. Access is restricted to configured Telegram user IDs.

    Usage:
        bot = TelegramBot(
            token="bot-token",
            allowed_user_ids=[123456789],
            task_service=task_svc,
            approval_service=approval_svc,
            safe_mode_service=safe_mode,
            status_service=status_svc,
        )
        await bot.start()
        # ... later ...
        await bot.stop()
    """

    def __init__(
        self,
        token: str,
        allowed_user_ids: list[int],
        task_service: ITaskService | None = None,
        approval_service: IApprovalService | None = None,
        safe_mode_service: ISafeModeService | None = None,
        status_service: ISystemStatusService | None = None,
        chat_service: IChatService | None = None,
        server_ops_service: IServerOpsService | None = None,
    ) -> None:
        """Initialize the Telegram bot.

        Args:
            token: Telegram Bot API token (from vault).
            allowed_user_ids: List of Telegram user IDs allowed to interact.
            task_service: Service for task queries and cancellation.
            approval_service: Service for approval resolution.
            safe_mode_service: Service for safe mode toggle.
            status_service: Service for system status queries.
        """
        self._token = token
        self._allowed_user_ids = allowed_user_ids
        self._task_service = task_service
        self._approval_service = approval_service
        self._safe_mode_service = safe_mode_service
        self._status_service = status_service
        self._chat_service = chat_service
        self._server_ops_service = server_ops_service
        self._app: Application | None = None
        self._user_filter = AllowedUsersFilter(allowed_user_ids)

    @property
    def application(self) -> Application | None:
        """The underlying python-telegram-bot Application (for testing)."""
        return self._app

    def _is_user_allowed(self, user_id: int | None) -> bool:
        """Check if a user ID is in the allowed list."""
        if user_id is None:
            return False
        return user_id in frozenset(self._allowed_user_ids)

    async def start(self) -> None:
        """Build and start the bot application with long-polling."""
        if not self._token:
            logger.warning("Telegram bot token not configured, skipping bot startup")
            return

        self._app = (
            Application.builder()
            .token(self._token)
            .build()
        )

        # Register command handlers (access-restricted)
        self._app.add_handler(CommandHandler("status", self._cmd_status))
        self._app.add_handler(CommandHandler("tasks", self._cmd_tasks))
        self._app.add_handler(CommandHandler("task", self._cmd_task))
        self._app.add_handler(CommandHandler("approve", self._cmd_approve))
        self._app.add_handler(CommandHandler("reject", self._cmd_reject))
        self._app.add_handler(CommandHandler("cancel", self._cmd_cancel))
        self._app.add_handler(CommandHandler("safe", self._cmd_safe))
        self._app.add_handler(CommandHandler("run", self._cmd_run))
        self._app.add_handler(CommandHandler("plan", self._cmd_plan))
        self._app.add_handler(CommandHandler("approvals", self._cmd_approvals))
        self._app.add_handler(CommandHandler("coder", self._cmd_coder))
        self._app.add_handler(CommandHandler("planner", self._cmd_planner))
        self._app.add_handler(CommandHandler("help", self._cmd_help))
        self._app.add_handler(CommandHandler("server", self._cmd_server))

        # Register callback query handler for inline buttons
        self._app.add_handler(CallbackQueryHandler(self._callback_handler))
        self._app.add_handler(
            MessageHandler(
                filters.TEXT & ~filters.COMMAND,
                self._text_fallback_handler,
            )
        )

        await self._app.initialize()
        await self._app.start()
        await self._app.updater.start_polling(drop_pending_updates=True)

        logger.info(
            "Telegram bot started with long-polling",
            extra={"allowed_users": self._allowed_user_ids},
        )

    async def stop(self) -> None:
        """Stop the bot and clean up resources."""
        if self._app is None:
            return

        if self._app.updater and self._app.updater.running:
            await self._app.updater.stop()
        await self._app.stop()
        await self._app.shutdown()
        self._app = None
        logger.info("Telegram bot stopped")

    # ------------------------------------------------------------------
    # Notification methods (called by other modules)
    # ------------------------------------------------------------------

    async def send_approval_request(
        self,
        approval_id: str,
        task_id: str,
        action_type: str,
        target_resource: str,
        timeout_seconds: int,
    ) -> None:
        """Send an approval request with inline approve/reject buttons.

        Args:
            approval_id: UUID of the approval request.
            task_id: UUID of the task requiring approval.
            action_type: Type of action needing approval.
            target_resource: The resource the action targets.
            timeout_seconds: Seconds before the approval times out.
        """
        if self._app is None or not self._allowed_user_ids:
            logger.warning("Cannot send approval request: bot not running or no users")
            return

        text = (
            "🔐 *Approval Required*\n\n"
            f"*Task:* `{task_id}`\n"
            f"*Action:* {_escape_md(action_type)}\n"
            f"*Target:* {_escape_md(target_resource)}\n"
            f"*Timeout:* {timeout_seconds}s\n\n"
            "Please approve or reject this action:"
        )

        keyboard = InlineKeyboardMarkup(
            [
                [
                    InlineKeyboardButton(
                        "✅ Approve",
                        callback_data=f"approve:{approval_id}",
                    ),
                    InlineKeyboardButton(
                        "❌ Reject",
                        callback_data=f"reject:{approval_id}",
                    ),
                ]
            ]
        )

        for user_id in self._allowed_user_ids:
            try:
                await self._app.bot.send_message(
                    chat_id=user_id,
                    text=text,
                    parse_mode="Markdown",
                    reply_markup=keyboard,
                )
            except Exception as exc:
                logger.error(
                    "Failed to send approval request to user",
                    extra={"user_id": user_id, "error": str(exc)},
                )

    async def send_message(self, text: str, parse_mode: str = "Markdown") -> None:
        """Send a plain text message to all allowed users.

        Args:
            text: Message text.
            parse_mode: Telegram parse mode (Markdown or HTML).
        """
        if self._app is None or not self._allowed_user_ids:
            return

        for user_id in self._allowed_user_ids:
            try:
                await self._app.bot.send_message(
                    chat_id=user_id,
                    text=text,
                    parse_mode=parse_mode,
                )
            except Exception as exc:
                logger.error(
                    "Failed to send message to user",
                    extra={"user_id": user_id, "error": str(exc)},
                )

    # ------------------------------------------------------------------
    # Command handlers
    # ------------------------------------------------------------------

    async def _cmd_status(
        self, update: Update, context: ContextTypes.DEFAULT_TYPE
    ) -> None:
        """Handle /status — system overview."""
        if not self._check_access(update):
            return

        if self._status_service is None:
            await update.message.reply_text("⚠️ Status service not available.")
            return

        try:
            status = await self._status_service.get_status()
            active_tasks = status.get("active_tasks", 0)
            queue_depths = status.get("queue_depths", {})
            workers = status.get("workers", {})

            queue_text = "\n".join(
                f"  • {agent}: {depth}" for agent, depth in queue_depths.items()
            ) or "  (empty)"

            worker_text = "\n".join(
                f"  • {wid}: {info.get('status', 'unknown')}"
                for wid, info in workers.items()
            ) or "  (none)"

            safe_mode = "🟢 ON" if (
                self._safe_mode_service and self._safe_mode_service.enabled
            ) else "🔴 OFF"

            text = (
                "📊 *System Status*\n\n"
                f"*Active Tasks:* {active_tasks}\n"
                f"*Safe Mode:* {safe_mode}\n\n"
                f"*Queue Depths:*\n{queue_text}\n\n"
                f"*Workers:*\n{worker_text}"
            )
            await update.message.reply_text(text, parse_mode="Markdown")
        except Exception as exc:
            logger.error("Error in /status command", extra={"error": str(exc)})
            await update.message.reply_text(f"❌ Error: {exc}")

    async def _cmd_tasks(
        self, update: Update, context: ContextTypes.DEFAULT_TYPE
    ) -> None:
        """Handle /tasks — list active (non-terminal) tasks."""
        if not self._check_access(update):
            return

        if self._task_service is None:
            await update.message.reply_text("⚠️ Task service not available.")
            return

        try:
            tasks = await self._task_service.list_active_tasks()
            if not tasks:
                await update.message.reply_text("✅ No active tasks.")
                return

            lines = ["📋 *Active Tasks*\n"]
            for task in tasks[:20]:  # Limit to 20 for readability
                tid = task.get("id", "?")
                state = task.get("state", "?")
                desc = task.get("description", "")[:50]
                agent = task.get("assigned_agent", "unassigned")
                lines.append(f"• `{tid}` [{state}] ({agent})\n  {_escape_md(desc)}")

            if len(tasks) > 20:
                lines.append(f"\n_...and {len(tasks) - 20} more_")

            await update.message.reply_text("\n".join(lines), parse_mode="Markdown")
        except Exception as exc:
            logger.error("Error in /tasks command", extra={"error": str(exc)})
            await update.message.reply_text(f"❌ Error: {exc}")

    async def _cmd_task(
        self, update: Update, context: ContextTypes.DEFAULT_TYPE
    ) -> None:
        """Handle /task <id> — task detail."""
        if not self._check_access(update):
            return

        if self._task_service is None:
            await update.message.reply_text("⚠️ Task service not available.")
            return

        args = context.args
        if not args:
            await update.message.reply_text("Usage: /task <task\\_id>")
            return

        task_id = args[0]
        try:
            task = await self._task_service.get_task(task_id)
            if task is None:
                await update.message.reply_text(f"❌ Task `{task_id}` not found.")
                return

            state = task.get("state", "?")
            agent = task.get("assigned_agent", "unassigned")
            desc = task.get("description", "")[:200]
            created = task.get("created_at", "?")
            started = task.get("started_at", "-")
            completed = task.get("completed_at", "-")

            text = (
                f"📝 *Task Detail*\n\n"
                f"*ID:* `{task_id}`\n"
                f"*State:* {state}\n"
                f"*Agent:* {agent}\n"
                f"*Created:* {created}\n"
                f"*Started:* {started}\n"
                f"*Completed:* {completed}\n\n"
                f"*Description:*\n{_escape_md(desc)}"
            )
            await update.message.reply_text(text, parse_mode="Markdown")
        except Exception as exc:
            logger.error("Error in /task command", extra={"error": str(exc)})
            await update.message.reply_text(f"❌ Error: {exc}")

    async def _cmd_approve(
        self, update: Update, context: ContextTypes.DEFAULT_TYPE
    ) -> None:
        """Handle /approve <id> — approve pending approval."""
        if not self._check_access(update):
            return

        if self._approval_service is None:
            await update.message.reply_text("⚠️ Approval service not available.")
            return

        args = context.args
        if not args:
            await update.message.reply_text("Usage: /approve <approval\\_id>")
            return

        approval_id = args[0]
        operator = f"telegram:{update.message.from_user.id}"

        try:
            success = await self._approval_service.approve(approval_id, operator)
            if success:
                await update.message.reply_text(
                    f"✅ Approval `{approval_id}` approved."
                )
            else:
                await update.message.reply_text(
                    f"⚠️ Could not approve `{approval_id}`. It may already be resolved."
                )
        except Exception as exc:
            logger.error("Error in /approve command", extra={"error": str(exc)})
            await update.message.reply_text(f"❌ Error: {exc}")

    async def _cmd_reject(
        self, update: Update, context: ContextTypes.DEFAULT_TYPE
    ) -> None:
        """Handle /reject <id> — reject pending approval."""
        if not self._check_access(update):
            return

        if self._approval_service is None:
            await update.message.reply_text("⚠️ Approval service not available.")
            return

        args = context.args
        if not args:
            await update.message.reply_text("Usage: /reject <approval\\_id>")
            return

        approval_id = args[0]
        operator = f"telegram:{update.message.from_user.id}"

        try:
            success = await self._approval_service.reject(approval_id, operator)
            if success:
                await update.message.reply_text(
                    f"✅ Approval `{approval_id}` rejected."
                )
            else:
                await update.message.reply_text(
                    f"⚠️ Could not reject `{approval_id}`. It may already be resolved."
                )
        except Exception as exc:
            logger.error("Error in /reject command", extra={"error": str(exc)})
            await update.message.reply_text(f"❌ Error: {exc}")

    async def _cmd_cancel(
        self, update: Update, context: ContextTypes.DEFAULT_TYPE
    ) -> None:
        """Handle /cancel <id> — cancel a task."""
        if not self._check_access(update):
            return

        if self._task_service is None:
            await update.message.reply_text("⚠️ Task service not available.")
            return

        args = context.args
        if not args:
            await update.message.reply_text("Usage: /cancel <task\\_id>")
            return

        task_id = args[0]
        actor = f"telegram:{update.message.from_user.id}"

        try:
            success = await self._task_service.cancel_task(task_id, actor)
            if success:
                await update.message.reply_text(f"✅ Task `{task_id}` cancelled.")
            else:
                await update.message.reply_text(
                    f"⚠️ Could not cancel `{task_id}`. "
                    "It may be in a non-cancellable state."
                )
        except Exception as exc:
            logger.error("Error in /cancel command", extra={"error": str(exc)})
            await update.message.reply_text(f"❌ Error: {exc}")

    async def _cmd_safe(
        self, update: Update, context: ContextTypes.DEFAULT_TYPE
    ) -> None:
        """Handle /safe — toggle safe mode."""
        if not self._check_access(update):
            return

        if self._safe_mode_service is None:
            await update.message.reply_text("⚠️ Safe mode service not available.")
            return

        try:
            current = self._safe_mode_service.enabled
            self._safe_mode_service.enabled = not current
            new_state = "🟢 ON" if self._safe_mode_service.enabled else "🔴 OFF"
            await update.message.reply_text(f"🔒 Safe mode toggled: {new_state}")
        except Exception as exc:
            logger.error("Error in /safe command", extra={"error": str(exc)})
            await update.message.reply_text(f"❌ Error: {exc}")

    async def _cmd_run(
        self, update: Update, context: ContextTypes.DEFAULT_TYPE
    ) -> None:
        """Handle /run <goal> — create an autonomous root task."""
        await self._create_orchestrated_task(update, context, plan_only=False)

    async def _cmd_plan(
        self, update: Update, context: ContextTypes.DEFAULT_TYPE
    ) -> None:
        """Handle /plan <goal> — create a plan-only root task."""
        await self._create_orchestrated_task(update, context, plan_only=True)

    async def _cmd_approvals(
        self, update: Update, context: ContextTypes.DEFAULT_TYPE
    ) -> None:
        """Handle /approvals — list pending approvals."""
        if not self._check_access(update):
            return

        if self._approval_service is None:
            await update.message.reply_text("⚠️ Approval service not available.")
            return

        try:
            approvals = await self._approval_service.list_pending()
            if not approvals:
                await update.message.reply_text("✅ No hay aprobaciones pendientes.")
                return

            lines = ["🔐 *Pending Approvals*\n"]
            for approval in approvals[:20]:
                lines.append(
                    f"• `{approval['id']}` task=`{approval['task_id']}` "
                    f"{_escape_md(str(approval['target_resource']))}"
                )
            await update.message.reply_text("\n".join(lines), parse_mode="Markdown")
        except Exception as exc:
            logger.error("Error in /approvals command", extra={"error": str(exc)})
            await update.message.reply_text(f"❌ Error: {exc}")

    async def _cmd_coder(
        self, update: Update, context: ContextTypes.DEFAULT_TYPE
    ) -> None:
        """Handle /coder <prompt> — direct chat with the coder model."""
        await self._run_chat_command(update, context, "coder")

    async def _cmd_planner(
        self, update: Update, context: ContextTypes.DEFAULT_TYPE
    ) -> None:
        """Handle /planner <prompt> — direct chat with the planner model."""
        await self._run_chat_command(update, context, "planner")

    async def _cmd_help(
        self, update: Update, context: ContextTypes.DEFAULT_TYPE
    ) -> None:
        """Handle /help — list supported commands."""
        if not self._check_access(update):
            return

        help_text = (
            "Comandos disponibles:\n"
            "/status\n"
            "/tasks\n"
            "/task <task_id>\n"
            "/cancel <task_id>\n"
            "/approve <approval_id>\n"
            "/reject <approval_id>\n"
            "/approvals\n"
            "/safe\n"
            "/run <objetivo>\n"
            "/plan <objetivo>\n"
            "/coder <mensaje>\n"
            "/planner <mensaje>\n"
            "/server status\n"
            "/server services\n"
            "/server disk"
        )
        await update.message.reply_text(help_text)

    async def _run_chat_command(
        self,
        update: Update,
        context: ContextTypes.DEFAULT_TYPE,
        target: str,
    ) -> None:
        """Execute a direct chat request against a local llama.cpp target."""
        if not self._check_access(update):
            return

        if self._chat_service is None:
            await update.message.reply_text("⚠️ Chat service not available.")
            return

        prompt = " ".join(context.args).strip()
        if not prompt:
            await update.message.reply_text(f"Usage: /{target} <mensaje>")
            return

        await update.message.reply_text(f"⏳ Consultando {target}...")

        try:
            response = await self._chat_service.chat(target, prompt)
            await update.message.reply_text(response[:4000])
        except Exception as exc:
            logger.error(
                "Error in chat command",
                extra={"target": target, "error": str(exc)},
            )
            await update.message.reply_text(f"❌ Error: {exc}")

    async def _create_orchestrated_task(
        self,
        update: Update,
        context: ContextTypes.DEFAULT_TYPE,
        *,
        plan_only: bool,
    ) -> None:
        if not self._check_access(update):
            return

        if self._task_service is None:
            await update.message.reply_text("⚠️ Task service not available.")
            return

        prompt = " ".join(context.args).strip()
        if not prompt:
            usage = "/plan <objetivo>" if plan_only else "/run <objetivo>"
            await update.message.reply_text(f"Uso: {usage}")
            return

        verb = "plan" if plan_only else "run"
        await update.message.reply_text(f"⏳ Lanzando {verb}...")
        try:
            task = await self._task_service.create_task(
                prompt,
                assigned_agent="planner",
                plan_only=plan_only,
                entrypoint="telegram",
            )
            text = (
                f"✅ Task creada\n"
                f"id=`{task.get('id')}`\n"
                f"state={task.get('state')}\n"
                f"agent={task.get('assigned_agent')}"
            )
            await update.message.reply_text(text, parse_mode="Markdown")
        except Exception as exc:
            logger.error(
                "Error creating orchestrated task",
                extra={"plan_only": plan_only, "error": str(exc)},
            )
            await update.message.reply_text(f"❌ Error: {exc}")

    async def _cmd_server(
        self, update: Update, context: ContextTypes.DEFAULT_TYPE
    ) -> None:
        """Handle /server <action> [arg] — controlled host inspection."""
        if not self._check_access(update):
            return

        if self._server_ops_service is None:
            await update.message.reply_text("⚠️ Server ops service not available.")
            return

        if not context.args:
            await update.message.reply_text(
                "Uso: /server <status|services|disk|logs|restart>"
            )
            return

        action = context.args[0].strip().lower()
        argument = " ".join(context.args[1:]).strip() or None

        try:
            response = await self._server_ops_service.run(action, argument)
            await update.message.reply_text(response[:4000])
        except Exception as exc:
            logger.error(
                "Error in server command",
                extra={"action": action, "argument": argument, "error": str(exc)},
            )
            await update.message.reply_text(f"❌ Error: {exc}")

    # ------------------------------------------------------------------
    # Callback query handler (inline buttons)
    # ------------------------------------------------------------------

    async def _callback_handler(
        self, update: Update, context: ContextTypes.DEFAULT_TYPE
    ) -> None:
        """Handle inline button presses (approve/reject)."""
        query = update.callback_query
        if query is None:
            return

        # Check access for callback queries
        user_id = query.from_user.id if query.from_user else None
        if not self._is_user_allowed(user_id):
            await query.answer("⛔ Access denied.", show_alert=True)
            return

        await query.answer()

        data = query.data or ""
        if ":" not in data:
            await query.edit_message_text("❌ Invalid callback data.")
            return

        action, approval_id = data.split(":", 1)
        operator = f"telegram:{user_id}"

        if self._approval_service is None:
            await query.edit_message_text("⚠️ Approval service not available.")
            return

        try:
            if action == "approve":
                success = await self._approval_service.approve(approval_id, operator)
                if success:
                    await query.edit_message_text(
                        f"✅ Approval `{approval_id}` *approved* by user {user_id}.",
                        parse_mode="Markdown",
                    )
                else:
                    await query.edit_message_text(
                        f"⚠️ Could not approve `{approval_id}`. Already resolved."
                    )
            elif action == "reject":
                success = await self._approval_service.reject(approval_id, operator)
                if success:
                    await query.edit_message_text(
                        f"✅ Approval `{approval_id}` *rejected* by user {user_id}.",
                        parse_mode="Markdown",
                    )
                else:
                    await query.edit_message_text(
                        f"⚠️ Could not reject `{approval_id}`. Already resolved."
                    )
            else:
                await query.edit_message_text(f"❌ Unknown action: {action}")
        except Exception as exc:
            logger.error(
                "Error handling callback",
                extra={"action": action, "approval_id": approval_id, "error": str(exc)},
            )
            await query.edit_message_text(f"❌ Error: {exc}")

    # ------------------------------------------------------------------
    # Access control
    # ------------------------------------------------------------------

    def _check_access(self, update: Update) -> bool:
        """Check if the user is allowed to use the bot.

        If access is denied, sends a denial message and returns False.
        """
        if update.message is None or update.message.from_user is None:
            return False

        user_id = update.message.from_user.id
        if not self._is_user_allowed(user_id):
            logger.warning(
                "Unauthorized Telegram access attempt",
                extra={"user_id": user_id},
            )
            # Silently ignore unauthorized users (don't reveal bot existence)
            return False

        return True

    async def _text_fallback_handler(
        self, update: Update, context: ContextTypes.DEFAULT_TYPE
    ) -> None:
        """Guide users who send plain text instead of a supported command."""
        if not self._check_access(update):
            return

        await update.message.reply_text(
            "Usa /coder <mensaje> o /planner <mensaje>. "
            "El bot no soporta chat libre todavia."
        )


# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------


def _escape_md(text: str) -> str:
    """Escape special Markdown characters for Telegram messages."""
    special_chars = ["_", "*", "[", "]", "(", ")", "~", "`", ">", "#", "+", "-", "=", "|", "{", "}", ".", "!"]
    for char in special_chars:
        text = text.replace(char, f"\\{char}")
    return text
