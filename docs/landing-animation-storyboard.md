# Landing Page Hero Animation Storyboard

Complete storyboard script for the homepage hero animation. Use this as the reference when implementing.

---

## Overall Animation Logic

```
IDE wrap-up focus
    ↓ (view zooms out, making room for the universe)
Terminal issues a command
    ↓ (the command triggers the "summon" visual metaphor)
20+ logos are pulled in by a magnetic field
    ↓ (convergence = LaunchKit as the single entry point)
Server box materializes and digests all vendors
    ↓ (processing = LED sequence, organic feel)
Service cards shoot out, the topology graph takes shape
    ↓ (output = real services are provisioned)
Terminal confirms line by line + URL lights up
    ↓ (pulling back from the "universe view" to the user's view)
The old world is struck through (contrast)
    ↓ (before/after impact)
1 dashboard · 1 credit card · 1 API key
```

The transition of every scene is based on **causal logic**: the command triggers the summon, the summon finishing triggers processing, processing finishing triggers the ejection, the ejection finishing triggers the confirmation.

---

## Detailed Storyboard Script

### Scene 0: IDE Wrap-up (0:00 – 1.5s)

**Visual**: dark code editor, the cursor blinks at the bottom right on the last line. Only the editor is on screen: quiet, focused.

**Animation details**:
- The last few characters are typed out with a typewriter effect (50ms/char)
- The cursor blinks two more times, then stops
- The user presses `Enter` — the cursor disappears, the editor sinks slightly (scale 0.98, 100ms)

**Transition logic**: as the editor sinks, the terminal panel slides up from the bottom of the screen (y: 40px → 0, ease-out-cubic, 300ms), like the VS Code split terminal sliding out.

---

### Scene 1: Terminal Input (1.5s – 3.5s)

**Visual**: the terminal takes the lower half of the screen and the editor shrinks into the upper half. The prompt blinks and typing starts.

**Animation details**:
```
> Help me deploy this project
```
- Each character is typewritten; Chinese is slightly slower (80ms/char) to mimic real typing rhythm
- After typing finishes, the cursor rests for another 0.4s (builds anticipation)
- Press Enter → the terminal shows one line: `◆ Analyzing...`, with a spinner

**Transition logic**: after the `Analyzing...` spinner completes one rotation, the screen starts to zoom out, and the editor + terminal shrink back into the background as blurred little squares. This shrink is the "raising of the viewpoint" that gives the next scene's sense of universe some context.

---

### Scene 2: Provider Logo Summon (3.5s – 7s)

**Visual**: black background, empty center. 20+ provider logos fly in from the edges on all sides.

**Wave design (key)**:

| Wave | Time offset | Logos | Size | Appear position |
|------|----------|------|------|----------|
| Wave 1 | +0.0s | Cloud Run, AWS S3, Neon, Cloudflare | Largest | 4 corners |
| Wave 2 | +0.3s | Upstash, Resend, GitHub, Artifact Registry, GCS | Medium | Midpoints of the four edges |
| Wave 3 | +0.6s | Stripe, Twilio, Datadog, Sentry, Auth0, MongoDB, Supabase, PlanetScale, Vercel, Fly.io, Heroku, Azure, Digital Ocean… | Smallest | Densely scattered |

**Logo flight animation**:
- The path is not a straight line but a slight arc (cubic bezier), as if pulled by a magnetic field
- During flight a slight rotation wobble of ±15°, rotation returns to 0 before landing
- Each logo has a `drop-shadow` glow in its brand color (GCP blue, AWS orange, Cloudflare orange, Neon green…)

**Logo destination**: a LaunchKit server box in the center of the screen whose outline has just appeared (only a faint wireframe at this point, like a hologram)

---

### Scene 3: Logos Absorbed into the Server Box (7s – 9s)

**Visual**: the logos reach the edge of the box and are "absorbed" one by one.

**Animation details**:
- When each logo arrives: scale 1.0 → 1.2 (0.1s) → 0 (0.1s), vanishing into the box
- At the moment of vanishing, the corresponding panel on the box has a brief glow (white flash, 50ms)
- After everything is absorbed, the box goes from a semi-transparent wireframe → fully solid rendering (material: frosted-aluminum feel, with slight ambient light reflection)
- The box does one slight bounce (scale 1.0 → 1.04 → 1.0, spring easing) to signal "fed full"

**Transition logic**: once the box bounce settles, the indicator lights start to light up.

---

### Scene 4: LED Sequence (9s – 11s)

**Visual**: the front of the box has a row of 6 LED indicators.

**LED script**:

1. All dark (0ms)
2. The leftmost light → lights up amber, pulses twice (processing)
3. Lights 2 and 3 light up in turn, also amber, like dominoes
4. The two rightmost lights: go straight to green (done)
5. The amber lights turn green one by one (left → right, 100ms interval)
6. Once all are green: the whole row does one synchronized pulse (brightness 100% → 60% → 100%)

**Accompanying animations**:
- A fine heat-haze effect on top of the box (simulated with an SVG turbulence filter)
- Faint rotating lines at the fan vent

**Transition logic**: the instant everything turns green, the outline of the service cards appears at the back of the box, as a precursor of them shooting out (very small scale, only a hint visible)

---

### Scene 5: Service Card Ejection (11s – 15s)

**Visual**: 8 polished service cards pop out of the top of the server box in turn, like physical products being fired out.

**Card design spec**:
- Size: about 160×200px (16:20 ratio)
- Style: frosted-glass card + colored top block (matching the service brand color) + service logo + service name + a status line

| Card | Brand color | Status text |
|------|--------|----------|
| PostgreSQL / Neon | 🟢 Green | `neon.tech · Ready` |
| Cloud Run | 🔵 Google blue | `us-central1 · 2 vCPU` |
| DNS / Cloudflare | 🟠 Orange | `A record · propagated` |
| Redis / Upstash | 🟣 Purple | `Global · 256MB` |
| S3 / GCS | 🟡 Yellow | `Bucket created` |
| Email / Resend | ⚪ White/gray | `SMTP ready` |
| Secrets | 🔴 Red | `6 env vars injected` |
| Domain | 🔵 Blue | `myapp.launchkit.app` |

**Ejection animation**:
- Each card comes out in a different direction (arc trajectory), then drops to its own position with a sense of gravity, surrounding the central "Your Project" node
- On landing, a slight bounce + the card flips 180° (showing its front)
- After landing, the cards have a slight hover float (infinite, 4px amplitude, a different period for each card to avoid a synchronized feel)

**Connection animation**:
- After each card settles, a thin glowing line is drawn from the card center → the "Your Project" node
- The line is an animated `stroke-dashoffset` effect (drawn from 0 to the end)
- The line color matches each service's brand color, at 60% opacity, with glow

**Transition logic**: the last connection is drawn → the whole topology graph does one slow, slight shrink (scale 1.0 → 0.85), making room at the bottom of the screen for the terminal.

---

### Scene 6: Deploy Success Feedback (15s – 17.5s)

**Visual**: the background topology graph shrinks and blurs back, and the terminal slides up again from below, bringing it into focus.

**Terminal text script** (300ms between lines):
```
✓  Neon DB provisioned
✓  Cloud Run deployed
✓  Cloudflare DNS configured
✓  Email routing ready
✓  6 secrets injected

🚀 Deploy succeeded

→  https://myapp-abc123.launchkit.app
```

**Key animations**:
- The `🚀 Deploy succeeded` line: slightly larger font, a 0.5s scale up (0.8 → 1.0) + text glow (primary color)
- The URL line: underline drawn from left to right (100ms), font color is the brand primary color, with a pulse
- The instant the URL appears: the border of the whole terminal frame goes from `border-border/50` → primary color, flashes once, then returns

---

### Scene 7: Old World Struck Through (17.5s – 21s)

**Visual**: the terminal shrinks, and the ghost UI of the old world appears on the right half of the screen — 6 dashboard thumbnails, 6 credit cards, 47 API key cells. Style: semi-transparent, desaturated, slightly red-tinted.

**Animation script**:
1. The ghost UI elements fade in first (like ghosts)
2. Red strikethrough lines sweep across each of them from left to right, like a pen stroke across paper
3. Order: dashboards first (0.2s interval) → credit cards → API key cells (dense, fast)
4. Each struck-through element immediately desaturates to fully gray + opacity drops to 30%
5. Finally, once everything is struck through, the whole batch does a "fragmented dispersal" (fragment into pixels → fade out)

---

### Scene 8: Final Statement (21s – 24s)

**Visual**: clean black screen, centered.

**Animation**:
```
1 dashboard  ·  1 credit card  ·  1 API key
```
- The three phrases fade in in turn; the `·` separator in between appears last
- Font: large, thin, monospace feel, in the LaunchKit brand primary color
- 0.5s after the three phrases finish appearing, the LaunchKit logo + CTA button (`Deploy now →`) appear
- The CTA button does one attention pulse (border glow spreads outward from the inside, twice)

---

## Exact Frame Reference (60fps)

| Scene | Start | End | Start frame | End frame |
|------|------|------|--------|--------|
| 00 IDE | 0.000s | 1.500s | F0 | F90 |
| 01 Terminal | 1.500s | 3.500s | F90 | F210 |
| 02 Logo Summon | 3.500s | 7.000s | F210 | F420 |
| 03 Logo Absorb | 7.000s | 9.000s | F420 | F540 |
| 04 LED Sequence | 9.000s | 11.000s | F540 | F660 |
| 05 Card Eject | 11.000s | 15.000s | F660 | F900 |
| 06 Deploy Success | 15.000s | 17.500s | F900 | F1050 |
| 07 Strikethrough | 17.500s | 21.000s | F1050 | F1260 |
| 08 Resolution | 21.000s | 24.000s | F1260 | F1440 |

> For per-frame events see `docs/animation/scene-0N-*.md`

## Implementation Priority

| Priority | Scene | Reason |
|--------|------|------|
| P0 | Scenes 2–5 (logos fly in + service cards eject) | Highest impact, the core differentiating visual |
| P1 | Scene 6 (Terminal deploy success) | The climax of the causal logic, the user's viewpoint returns |
| P2 | Scenes 7–8 (strikethrough + final statement) | Before/after contrast, strengthens recall |
| P3 | Scenes 0–1 (IDE + Terminal input) | Opening setup, can start with a simple version |

## Technical Notes

- Logo flight: CSS `@keyframes` + JS to compute the start coordinates dynamically (from outside the screen edge)
- Service cards: `framer-motion` layoutId for the flip, or pure CSS 3D transform
- Connection lines: SVG `<path>` + `stroke-dashoffset` animation
- Server box: Three.js for lightweight 3D, or CSS perspective + stacked divs to fake 3D
- The whole animation can be controlled with `IntersectionObserver`: it triggers only when scrolled into the viewport and resets when out of the viewport
