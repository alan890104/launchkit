# Scene 05 — Service Card Ejection

**Time range**: 11.000s – 15.000s  
**Frame range**: F660 – F900 (240 frames total)  
**GSAP position**: `tl.add(scene05(), 11.000)`

## Visual Description

8 service cards pop out of the top of the server box one after another, fall along arcs to their own positions, and surround the central "Your Project" node.
After landing, each card flips 180° to show its front, then a glowing line is drawn from the card center to the node.

## Card Spec

| # | Service | Brand color | Top color block | Status text | Landing position (relative to center) |
|---|------|--------|----------|----------|---------------------|
| 1 | PostgreSQL / Neon | `#00E599` green | green | `neon.tech · Ready` | upper left (-160px, -120px) |
| 2 | Cloud Run | `#4285F4` blue | blue | `us-central1 · 2 vCPU` | upper right (+160px, -120px) |
| 3 | DNS / Cloudflare | `#F48120` orange | orange | `A record · propagated` | right (+220px, 0px) |
| 4 | Redis / Upstash | `#00E9A3` teal | teal | `Global · 256MB` | lower right (+160px, +120px) |
| 5 | S3 / GCS | `#FBBC04` yellow | yellow | `Bucket created` | bottom (0px, +160px) |
| 6 | Email / Resend | `#9CA3AF` gray | gray | `SMTP ready` | lower left (-160px, +120px) |
| 7 | Secrets | `#EF4444` red | red | `6 env vars injected` | left (-220px, 0px) |
| 8 | Domain | `#6366F1` purple | purple | `myapp.launchkit.app` | top (0px, -160px) |

Card size: 160px × 200px (w × h)  
Card style: frosted glass (`backdrop-blur-sm bg-card/80`) + 12px colored top block + logo icon + service name + status line

## Frame-by-Frame Event Table

Each card flies for 0.6s, stagger 0.4s, flip 0.2s, connection line 0.3s

| Frame | Time | Event | Parameters |
|----|------|------|------|
| F660 | 11.000s | Scene starts, the central "Your Project" node fades in | opacity 0→1, scale 0.8→1, duration: 0.3s |
| **F660** | **11.000s** | **Card 1 (Neon) launches** (from the top of the box, y=-10px) | |
| F660 | 11.000s | Card 1 arc flight starts | duration: 0.6s, ease: power2.out |
| F696 | 11.600s | Card 1 lands (-160px, -120px) | bounce: scale 1.1→1.0, 0.1s |
| F696 | 11.600s | Card 1 flip starts | rotationY: 0→180, duration: 0.2s |
| F708 | 11.800s | Card 1 flip complete (front facing out) | |
| F708 | 11.800s | Card 1 connection line starts (SVG path draw) | stroke-dashoffset: length→0, duration: 0.3s |
| F708 | 11.800s | Card 1 float animation starts (infinite) | amplitude: 4px, period: 2.1s |
| F726 | 12.100s | Card 1 connection line complete | |
| **F684** | **11.400s** | **Card 2 (Cloud Run) launches** | stagger +0.4s |
| F720 | 12.000s | Card 2 lands (+160px, -120px) | |
| F720 | 12.000s | Card 2 flip | |
| F732 | 12.200s | Card 2 flip complete | |
| F732 | 12.200s | Card 2 connection line starts | |
| F750 | 12.500s | Card 2 connection line complete | |
| **F708** | **11.800s** | **Card 3 (Cloudflare) launches** | stagger +0.4s |
| F744 | 12.400s | Card 3 lands (+220px, 0px) | |
| F744 | 12.400s | Card 3 flip | |
| F756 | 12.600s | Card 3 flip complete | |
| F756 | 12.600s | Card 3 connection line starts | |
| F774 | 12.900s | Card 3 connection line complete | |
| **F732** | **12.200s** | **Card 4 (Upstash) launches** | stagger +0.4s |
| F768 | 12.800s | Card 4 lands (+160px, +120px) | |
| F786 | 13.100s | Card 4 connection line complete | |
| **F756** | **12.600s** | **Card 5 (GCS) launches** | |
| F792 | 13.200s | Card 5 lands (0px, +160px) | |
| F810 | 13.500s | Card 5 connection line complete | |
| **F780** | **13.000s** | **Card 6 (Resend) launches** | |
| F816 | 13.600s | Card 6 lands (-160px, +120px) | |
| F834 | 13.900s | Card 6 connection line complete | |
| **F804** | **13.400s** | **Card 7 (Secrets) launches** | |
| F840 | 14.000s | Card 7 lands (-220px, 0px) | |
| F858 | 14.300s | Card 7 connection line complete | |
| **F828** | **13.800s** | **Card 8 (Domain) launches** | |
| F864 | 14.400s | Card 8 lands (0px, -160px) | |
| F876 | 14.600s | Card 8 flip complete | |
| F876 | 14.600s | Card 8 connection line starts | |
| F894 | 14.900s | Card 8 connection line complete, topology graph fully connected | |
| **F882** | **14.700s** | **Topology graph overall scale 1.0→0.85 starts** (making room for the terminal) | duration: 0.5s |
| **F882** | **14.700s** | **Topology graph blur 0→3px starts** | duration: 0.5s |
| F900 | 15.000s | Scene 05 END | → enter Scene 06 |

## Float Animation (different period per card)

```css
@keyframes float {
  0%, 100% { transform: translateY(0px); }
  50% { transform: translateY(-4px); }
}
/* a different duration per card, to avoid sync */
.card-1 { animation: float 2.1s ease-in-out infinite; }
.card-2 { animation: float 2.4s ease-in-out infinite; }
.card-3 { animation: float 2.7s ease-in-out infinite; }
.card-4 { animation: float 2.3s ease-in-out infinite; }
.card-5 { animation: float 2.6s ease-in-out infinite; }
.card-6 { animation: float 2.2s ease-in-out infinite; }
.card-7 { animation: float 2.5s ease-in-out infinite; }
.card-8 { animation: float 2.8s ease-in-out infinite; }
```

## Card Flight GSAP

```ts
function ejectCard(cardEl, targetX, targetY, delay) {
  const tl = gsap.timeline({ delay })

  // Arc flight (fly up first, then land on the target)
  const arcPeakY = -80 // height of the pop-out from the top of the box

  tl.set(cardEl, { x: 0, y: 0, rotationY: 180, opacity: 1, scale: 0.3 })
  tl.to(cardEl, {
    x: targetX,
    y: arcPeakY,
    scale: 1,
    duration: 0.3,
    ease: 'power2.out'
  })
  tl.to(cardEl, {
    y: targetY,
    duration: 0.3,
    ease: 'bounce.out'
  })

  // Flip
  tl.to(cardEl, { rotationY: 0, duration: 0.2, ease: 'power2.inOut' })

  return tl
}
```

## SVG Connection Lines

```tsx
<svg ref={svgRef} className="absolute inset-0 pointer-events-none overflow-visible">
  <defs>
    <filter id="line-glow">
      <feGaussianBlur stdDeviation="2" result="blur" />
      <feMerge><feMergeNode in="blur"/><feMergeNode in="SourceGraphic"/></feMerge>
    </filter>
  </defs>
  {connections.map(conn => (
    <line
      key={conn.id}
      ref={conn.ref}
      x1={conn.from.x} y1={conn.from.y}
      x2={conn.to.x} y2={conn.to.y}
      stroke={conn.color}
      strokeWidth="1.5"
      strokeOpacity="0.6"
      filter="url(#line-glow)"
      strokeDasharray={conn.length}
      strokeDashoffset={conn.length}  // GSAP animates to 0
    />
  ))}
</svg>
```

## Transition Exit

After the topology graph shrinks and blurs, the terminal slides up from below into Scene 06.
