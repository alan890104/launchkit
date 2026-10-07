# Scene 00 — IDE Wrap-up (full-screen VS Code + Screen Studio camera)

**Time range**: 0.000s – 3.500s (extended; the original 1.5s was not enough)
**Frame range**: F0 – F210 (210 frames total)
**GSAP position**: `tl.add(scene00(), 0.000)`

> Scene 00 is extended to 3.5s; the timelines of the following scenes all shift back accordingly.
> See the updated index.md.

---

## Visual Layout: Full VS Code Simulation

```
┌─────────────────────────────────────────────────────────────────┐
│ ● ● ●   my-startup — VS Code                                    │ ← title bar
├──┬──────────────────┬────────────────────────────────────────────┤
│  │ EXPLORER         │  app.py                          ×         │ ← tab bar
│⬜│                  ├────────────────────────────────────────────┤
│⬜│ ▼ BACKEND        │  1  from sqlalchemy import...             │
│⬜│   📄 app.py      │  2  from sqlalchemy.orm import...         │
│⬜│   📄 models.py   │  3  import redis                           │
│  │   📄 requirements│  4                                         │
│  │                  │  5  # Database connection                  │
│  │ ▶ FRONTEND       │  6  engine = create_engine(               │
│  │   📄 App.tsx     │  7      "postgresql://user:pass@..."       │
│  │   📄 index.tsx   │  8  )                                      │
│  │                  │  9  Session = sessionmaker(bind=engine)    │
│  │                  │ 10                                         │
│  │                  │ 11  # Redis cache                          │
│  │                  │ 12  cache = redis.Redis(                   │
│  │                  │ 13      host="localhost", port=6379        │
│  │                  │ 14  )                                      │
├──┴──────────────────┴────────────────────────────────────────────┤
│ TERMINAL                                                         │ ← terminal panel
│ >  █                                                             │
└─────────────────────────────────────────────────────────────────┘
```

---

## Three Animation Steps (Screen Studio Camera Push-in)

### Step 1: Zoom In → SQLAlchemy + Redis (0.000s – 1.200s)

**Camera effect**:
- The whole VS Code container gets a `scale + translateY` GSAP transform
- transformOrigin: center of the code area (aimed at lines 6–14)
- scale: 1.0 → 1.8, with translateY centering the target lines
- duration: 0.9s, ease: `power2.inOut` (Screen Studio feel)

**Highlight effect** (starts after the zoom in completes, at t=0.9s):
- Other lines: opacity 1.0 → 0.35 (dim, duration 0.3s)
- Target lines (6–9 SQLAlchemy, 12–14 Redis): keep opacity 1.0
- Target line background: `background: rgba(99,102,241,0.12)`, `border-left: 2px solid #6366F1` fades in (duration 0.3s)
- At the same time: the key identifiers of SQLAlchemy and Redis get a text glow (`text-shadow: 0 0 8px currentColor`)

**Hold**: from t=0.9s keep the zoomed-in state for 0.6s

| Time | Event |
|------|------|
| 0.000s | Full VS Code visible, cursor blinking at the end of line 9 |
| 0.000s | Camera zoom-in starts (scale 1→1.8, translateY adjusts) |
| 0.900s | Zoom-in complete, hold |
| 0.900s | Dim the other lines (opacity→0.35, 300ms) |
| 0.900s | Highlight the target lines (bg + border + glow, 300ms) |
| 1.200s | Step 1 complete, enter Step 2 |

---

### Step 2: Zoom Out (1.200s – 2.000s)

**Camera effect**:
- Highlight fades out first (duration 0.2s) → all lines return to opacity 1.0
- Then scale 1.8 → 1.0, translateY → 0
- duration: 0.6s, ease: `power2.inOut`
- Hold 0.2s at full view

| Time | Event |
|------|------|
| 1.200s | Highlight fade out (0.2s) |
| 1.200s | All lines opacity → 1.0 (0.2s) |
| 1.400s | Camera zoom-out starts (scale 1.8→1.0, 0.6s) |
| 2.000s | Full view restored, hold 0.2s |

---

### Step 3: Zoom In → Terminal Cursor (2.000s – 3.500s)

**Camera effect**:
- transformOrigin switches to the bottom of the screen (terminal position)
- scale: 1.0 → 1.5, translateY moves down to center the terminal
- duration: 0.7s, ease: `power2.inOut`

**Terminal highlight**:
- A faint glow border (primary color) appears above the Terminal panel
- The Terminal cursor `█` starts blinking (1s blink)

**Hold + transition**:
- t=2.7s: zoom in complete, terminal cursor blinking
- t=3.0s: game → Scene 01 takes over, the terminal starts typing (zoom is held, Scene 01 starts from this state)

| Time | Event |
|------|------|
| 2.000s | transformOrigin switches to bottom |
| 2.000s | Camera zoom-in on terminal starts (scale 1→1.5, 0.7s) |
| 2.700s | Zoom-in complete, terminal cursor blinking |
| 2.700s | Terminal border glow fade in (primary, 0.3s) |
| 3.500s | Scene 00 END → Scene 01 takes over (zoom state inherited) |

---

## Python Code Content (Full)

```python
from sqlalchemy import create_engine, Column, Integer, String
from sqlalchemy.orm import sessionmaker, DeclarativeBase
import redis

# ── Database connection ───────────────────────────
engine = create_engine(
    "postgresql://admin:secret@localhost/mydb",
    pool_size=10,
    echo=False,
)
Session = sessionmaker(bind=engine)

# ── Redis cache ───────────────────────────────────
cache = redis.Redis(
    host="localhost",
    port=6379,
    decode_responses=True,
)
```

**Highlight target lines**: the whole `create_engine(...)` call (lines 6–9) + the whole `redis.Redis(...)` call (lines 13–16)

---

## VS Code Sidebar File Tree

```
▼ MY-STARTUP
  ▼ backend
      🐍 app.py          ← currently open, highlighted
      🐍 models.py
      📄 requirements.txt
  ▶ frontend
      ⚛ App.tsx
      ⚛ index.tsx
      📄 package.json
```

---

## Implementation Notes

- **Camera zoom**: the whole `.vscode-container` div gets `gsap.to(containerEl, { scale, y, duration, ease })`, with transformOrigin switched dynamically via `gsap.set`
- **Highlight**: the target-line wrapper divs have a `ref`, and GSAP controls background-color and border-left-width
- **Dim**: an array of the non-target-line wrapper divs, `gsap.to(nonTargetLines, { opacity: 0.35 })`
- **Scene 01 inheritance**: when Scene 00 ends, scale=1.5 and the terminal is centered; Scene 01 starts typing directly in this transform state and zooms out only after typing is done (Scene 02 takes over the zoom out + black screen)
