import os
import logging

from fastapi import FastAPI, Request
from fastapi.responses import JSONResponse
from telegram import Update, Bot
from telegram.ext import Application, CommandHandler, MessageHandler, filters

logging.basicConfig(level=logging.INFO)
logger = logging.getLogger(__name__)

BOT_TOKEN = os.environ.get("BOT_TOKEN", "")
WEBHOOK_URL = os.environ.get("WEBHOOK_URL", "")

app = FastAPI()
bot_app: Application | None = None


async def start_command(update: Update, context):
    await update.message.reply_text(
        "Hello! I'm a LaunchKit POC bot. Send me any message and I'll echo it back."
    )


async def echo(update: Update, context):
    await update.message.reply_text(f"Echo: {update.message.text}")


@app.on_event("startup")
async def on_startup():
    global bot_app
    if not BOT_TOKEN:
        logger.warning("BOT_TOKEN not set, bot will not work")
        return
    bot_app = Application.builder().token(BOT_TOKEN).build()
    bot_app.add_handler(CommandHandler("start", start_command))
    bot_app.add_handler(MessageHandler(filters.TEXT & ~filters.COMMAND, echo))
    await bot_app.initialize()
    logger.info("Bot initialized")


@app.post("/webhook")
async def webhook(request: Request):
    if not bot_app:
        return JSONResponse({"error": "bot not initialized"}, status_code=503)
    data = await request.json()
    update = Update.de_json(data, bot_app.bot)
    await bot_app.process_update(update)
    return JSONResponse({"ok": True})


@app.get("/health")
async def health():
    return {"status": "ok", "bot_configured": bool(BOT_TOKEN), "version": "v2"}


if __name__ == "__main__":
    import uvicorn
    uvicorn.run(app, host="0.0.0.0", port=int(os.environ.get("PORT", "8080")))
