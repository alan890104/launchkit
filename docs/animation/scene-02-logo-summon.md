# Scene 02 — Provider Logo Summon

**Time range**: 9.000s – 13.500s (master timeline, including the transition)  
**GSAP position**: `tl.add(scene02(), 9.000)`

## Visual Description

### First half of the transition (camera passes through the screen)

Scene 01's `✢` spins faster, and the whole VS Code keeps scaling up + blurring — the camera keeps rushing forward, as if passing through the screen.  
At the peak of the blur, a white flash (0.15s).  
The flash fades: black background, the server box wireframe in the dead center.

**Narrative:** pressing Enter takes you through the screen into the world behind the terminal.

### Main part

Black background; the server box wireframe appears in the center of the screen.  
22 provider logos fly in from the edges on all sides in three waves, with brand-color glow, along arc trajectories.

## Provider Logo List (22 in total)

| Wave | Count | Logo | Brand color | Size | Start position |
|------|------|------|--------|------|----------|
| Wave 1 | 4 | Cloud Run (`siGooglecloud` blue `#4285F4`) | blue | 48px | outside the four corners |
| Wave 1 | | AWS S3 (`siAmazonwebservices` orange `#FF9900`) | orange | 48px | |
| Wave 1 | | Neon (`siNeon` green `#00E599`) | green | 48px | |
| Wave 1 | | Cloudflare (`siCloudflare` orange `#F48120`) | orange | 48px | |
| Wave 2 | 7 | Upstash (`siUpstash` green `#00E9A3`) | green | 36px | midpoints of the four edges |
| Wave 2 | | Resend (`siResend` white `#000000`) | off-white | 36px | |
| Wave 2 | | GitHub (`siGithub` white `#181717`) | white | 36px | |
| Wave 2 | | Docker (`siDocker` blue `#2496ED`) | blue | 36px | |
| Wave 2 | | Stripe (`siStripe` purple `#635BFF`) | purple | 36px | |
| Wave 2 | | Twilio (`siTwilio` red `#F22F46`) | red | 36px | |
| Wave 2 | | Datadog (`siDatadog` purple `#632CA6`) | purple | 36px | |
| Wave 3 | 11 | Sentry (`siSentry` purple `#362D59`) | purple | 28px | random edges |
| Wave 3 | | Auth0 (`siAuth0` orange `#EB5424`) | orange | 28px | |
| Wave 3 | | MongoDB (`siMongodb` green `#47A248`) | green | 28px | |
| Wave 3 | | Supabase (`siSupabase` green `#3FCF8E`) | green | 28px | |
| Wave 3 | | PlanetScale (`siPlanetscale` black `#000000`) | white | 28px | |
| Wave 3 | | Vercel (`siVercel` black `#000000`) | white | 28px | |
| Wave 3 | | Fly.io (`siFlyio` purple `#7B3FF2`) | purple | 28px | |
| Wave 3 | | Heroku (`siHeroku` purple `#430098`) | purple | 28px | |
| Wave 3 | | Azure (`siMicrosoftazure` blue `#0078D4`) | blue | 28px | |
| Wave 3 | | DigitalOcean (`siDigitalocean` blue `#0080FF`) | blue | 28px | |

## Frame-by-Frame Event Table

### Transition (0.0s – 1.2s, relative to the start of scene02)

| Time | Event | Parameters |
|------|------|------|
| 0.0s | `✢` rotation speed accelerates from the original slow speed | duration: 0.5s, rotation speed × 4 |
| 0.0s | vsCode scale keeps pushing in 2.0 → 5.0 | duration: 0.8s, ease: power2.in |
| 0.0s | vsCode blur 0 → 20px | duration: 0.8s, ease: power2.in |
| 0.8s | White flash overlay: opacity 0 → 1 | duration: 0.10s |
| 0.9s | White flash overlay: opacity 1 → 0 | duration: 0.15s |
| 0.9s | vsCode hidden instantly (opacity 0) | same as the flash peak |
| 0.9s | Black stage appears | |

### Main part (1.2s – 4.5s)

| Time | Event | Parameters |
|------|------|------|
| 1.2s | Server box wireframe opacity 0 → 0.5 | duration: 0.3s, primary color, neon feel |
| **F240** | **4.000s** | **Wave 1 launches (4 logos)** | |
| 1.5s | **Wave 1 launches (4 logos, 48px)** | |
| 1.5s | Cloud Run: flies in from outside the top-left corner | duration: 1.5s, ease: power2.in |
| 1.5s | AWS S3: flies in from outside the top-right corner | |
| 1.5s | Neon: flies in from outside the bottom-right corner | |
| 1.5s | Cloudflare: flies in from outside the bottom-left corner | |
| 1.5s | During flight rotation: ±15°, rotation→0 before arrival | glow: drop-shadow(0 0 12px #hex) |
| 1.8s | **Wave 2 launches (7 logos, 36px, stagger 50ms)** | |
| 1.8s | Upstash, Resend, GitHub, Docker, Stripe, Twilio, Datadog | duration: 1.4s each |
| 2.1s | **Wave 3 launches (11 logos, 28px, stagger 30ms)** | |
| 2.1s | Sentry, Auth0, MongoDB, Supabase, PlanetScale, Vercel, Fly.io, Heroku, Azure, DigitalOcean | duration: 1.3s each |
| 3.0s | Wave 1 reaches the edge of the server box | |
| 3.2s | Wave 2 all in place | |
| 3.6s | Wave 3 all in place | |
| 4.5s | All logos in place, waiting to be absorbed | Scene 02 END → Scene 03 |

## Flight Path Logic

```ts
function flyToBox(logoEl: Element, startX: number, startY: number, boxCenter: {x: number, y: number}) {
  // arc: midpoint offset by 30-60px (random, so that no two logo paths overlap)
  const midX = (startX + boxCenter.x) / 2 + (Math.random() - 0.5) * 80
  const midY = (startY + boxCenter.y) / 2 + (Math.random() - 0.5) * 80

  gsap.set(logoEl, { x: startX, y: startY, scale: 1, rotation: Math.random() * 20 - 10 })
  gsap.to(logoEl, {
    motionPath: {
      path: [{ x: startX, y: startY }, { x: midX, y: midY }, { x: boxCenter.x, y: boxCenter.y }],
      type: 'quadratic'
    },
    rotation: 0,
    duration: 1.5, // Wave 1; Wave 2=1.4s; Wave 3=1.3s
    ease: 'power2.in',
    filter: `drop-shadow(0 0 12px #${brandHex})`
  })
}
```

> If the MotionPath plugin is not used, use two gsap.to calls instead: fly to the midpoint first, then to the box.

## Component Structure

```tsx
<div ref={stageRef} className="relative w-full h-full bg-black overflow-hidden">
  {/* Server box wireframe */}
  <div ref={serverBoxRef} className="server-box-wireframe absolute inset-0 m-auto" style={{ opacity: 0 }} />

  {/* Logo sprites (dynamic position) */}
  {logos.map(logo => (
    <div key={logo.id} ref={logoRefs[logo.id]} className="logo-sprite absolute"
      style={{ opacity: 1, width: logo.size, height: logo.size }}>
      <svg viewBox="0 0 24 24" fill={`#${logo.hex}`}>
        <path d={logo.path} />
      </svg>
    </div>
  ))}
</div>
```

## Transition Exit

Once all logos reach the edge of the server box, Scene 03 starts the absorb animation.
