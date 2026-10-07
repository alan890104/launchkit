import os
from contextlib import asynccontextmanager

import asyncpg
import redis.asyncio as redis
from fastapi import FastAPI
from fastapi.middleware.cors import CORSMiddleware
from pydantic import BaseModel

pool: asyncpg.Pool | None = None
redis_client: redis.Redis | None = None


@asynccontextmanager
async def lifespan(app: FastAPI):
    global pool, redis_client
    database_url = os.environ.get("DATABASE_URL")
    redis_url = os.environ.get("REDIS_URL")
    if database_url:
        pool = await asyncpg.create_pool(database_url)
        await pool.execute("""
            CREATE TABLE IF NOT EXISTS items (
                id SERIAL PRIMARY KEY,
                name TEXT NOT NULL,
                created_at TIMESTAMPTZ DEFAULT NOW()
            )
        """)
    if redis_url:
        redis_client = redis.from_url(redis_url)
    yield
    if pool:
        await pool.close()
    if redis_client:
        await redis_client.aclose()


app = FastAPI(lifespan=lifespan)

app.add_middleware(
    CORSMiddleware,
    allow_origins=[os.environ.get("FRONTEND_URL", "*")],
    allow_methods=["*"],
    allow_headers=["*"],
)


class ItemCreate(BaseModel):
    name: str


@app.get("/health")
async def health():
    return {"status": "ok"}


@app.get("/api/items")
async def list_items():
    if not pool:
        return {"items": [], "note": "no database configured"}
    # Try cache first
    if redis_client:
        cached = await redis_client.get("items_cache")
        if cached:
            import json
            return {"items": json.loads(cached), "source": "cache"}
    rows = await pool.fetch("SELECT id, name, created_at FROM items ORDER BY created_at DESC LIMIT 50")
    items = [{"id": r["id"], "name": r["name"], "created_at": str(r["created_at"])} for r in rows]
    if redis_client:
        import json
        await redis_client.set("items_cache", json.dumps(items), ex=30)
    return {"items": items, "source": "db"}


@app.post("/api/items", status_code=201)
async def create_item(item: ItemCreate):
    if not pool:
        return {"error": "no database configured"}
    row = await pool.fetchrow(
        "INSERT INTO items (name) VALUES ($1) RETURNING id, name, created_at", item.name
    )
    if redis_client:
        await redis_client.delete("items_cache")
    return {"id": row["id"], "name": row["name"], "created_at": str(row["created_at"])}


if __name__ == "__main__":
    import uvicorn
    uvicorn.run(app, host="0.0.0.0", port=int(os.environ.get("PORT", "8080")))
