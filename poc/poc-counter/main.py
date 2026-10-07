import os
from contextlib import asynccontextmanager

import asyncpg
from fastapi import FastAPI
from fastapi.responses import HTMLResponse

pool = None


@asynccontextmanager
async def lifespan(app):
    global pool
    pool = await asyncpg.create_pool(os.environ["DATABASE_URL"])
    await pool.execute("""
        CREATE TABLE IF NOT EXISTS counters (
            name TEXT PRIMARY KEY,
            count INTEGER NOT NULL DEFAULT 0
        )
    """)
    yield
    await pool.close()


app = FastAPI(lifespan=lifespan)

HTML = """<!DOCTYPE html>
<html>
<head>
<title>LaunchKit Counter</title>
<style>
  body { font-family: system-ui, sans-serif; max-width: 600px; margin: 40px auto; padding: 0 20px; }
  h1 { color: #4f46e5; }
  input, button { padding: 8px 16px; font-size: 16px; }
  button { cursor: pointer; background: #4f46e5; color: white; border: none; border-radius: 4px; }
  button:hover { background: #4338ca; }
  .row { display: flex; justify-content: space-between; padding: 10px 0; border-bottom: 1px solid #e5e7eb; }
  .row .name { font-weight: 500; }
  .row .count { color: #4f46e5; font-weight: 700; font-size: 1.2em; }
  #msg { color: #16a34a; margin: 8px 0; min-height: 1.5em; }
</style>
</head>
<body>
<h1>LaunchKit Counter</h1>
<p>Enter a name and click +1. Data is stored in PostgreSQL. Reload the page to verify persistence.</p>
<div style="display:flex;gap:8px;margin:20px 0">
  <input id="name" placeholder="Your name" />
  <button onclick="inc()">+1</button>
</div>
<div id="msg"></div>
<div id="list"></div>
<script>
async function load() {
  const r = await fetch('/api/counters');
  const d = await r.json();
  document.getElementById('list').innerHTML = d.length === 0
    ? '<p style="color:#9ca3af">No entries yet.</p>'
    : d.map(x => `<div class="row"><span class="name">${x.name}</span><span class="count">${x.count}</span></div>`).join('');
}
async function inc() {
  const el = document.getElementById('name');
  const name = el.value.trim();
  if (!name) return;
  await fetch('/api/counters', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ name })
  });
  el.value = '';
  document.getElementById('msg').textContent = `${name} +1!`;
  setTimeout(() => document.getElementById('msg').textContent = '', 2000);
  load();
}
document.getElementById('name').addEventListener('keydown', e => { if (e.key === 'Enter') inc(); });
load();
</script>
</body>
</html>"""


@app.get("/", response_class=HTMLResponse)
async def index():
    return HTML


@app.get("/health")
async def health():
    try:
        await pool.fetchval("SELECT 1")
        return {"status": "healthy", "db": "connected"}
    except Exception as e:
        return {"status": "unhealthy", "db": str(e)}


@app.get("/api/counters")
async def get_counters():
    rows = await pool.fetch("SELECT name, count FROM counters ORDER BY count DESC")
    return [dict(r) for r in rows]


@app.post("/api/counters")
async def increment(data: dict):
    name = (data.get("name") or "").strip()
    if not name:
        return {"error": "name is required"}
    await pool.execute(
        """INSERT INTO counters (name, count) VALUES ($1, 1)
           ON CONFLICT (name) DO UPDATE SET count = counters.count + 1""",
        name,
    )
    return await get_counters()
