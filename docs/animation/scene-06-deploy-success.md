# Scene 06 — Deploy Success Feedback

**Time range**: 15.000s – 17.500s  
**Frame range**: F900 – F1050 (150 frames total)  
**GSAP position**: `tl.add(scene06(), 15.000)`

## Visual Description

The topology graph recedes into the background (blur + scale down).  
The terminal slides up from below and prints the confirmation messages line by line; finally the URL lights up and flashes.

## Terminal Content Script

```
✓  Neon DB provisioned
✓  Cloud Run deployed
✓  Cloudflare DNS configured
✓  Email routing ready
✓  6 secrets injected

🚀 Deploy succeeded

→  https://myapp-abc123.launchkit.app
```

## Frame-by-Frame Event Table

Interval between lines: 300ms = 18 frames

| Frame | Time | Event | Parameters |
|----|------|------|------|
| F900 | 15.000s | Scene starts | |
| F900 | 15.000s | Topology graph blur continues (already started in scene 05) | filter: blur(3px→6px), duration: 0.4s |
| F900 | 15.000s | Terminal container slide-up starts | y: 60px→0, opacity: 0→1, duration: 0.4s, ease: power2.out |
| F924 | 15.400s | Terminal in place | |
| **F924** | **15.400s** | **Line 1 appears**: "✓  Neon DB provisioned" | opacity 0→1, duration: 0.2s, text color: primary |
| **F942** | **15.700s** | **Line 2 appears**: "✓  Cloud Run deployed" | +300ms |
| **F960** | **16.000s** | **Line 3 appears**: "✓  Cloudflare DNS configured" | +300ms |
| **F978** | **16.300s** | **Line 4 appears**: "✓  Email routing ready" | +300ms |
| **F996** | **16.600s** | **Line 5 appears**: "✓  6 secrets injected" | +300ms |
| F1014 | 16.900s | Empty line (visual pause 100ms) | |
| **F1020** | **17.000s** | **"🚀 Deploy succeeded" appears** | scale: 0.8→1.0, duration: 0.5s, ease: back.out(1.7) |
| F1020 | 17.000s | 🚀 text glow starts | text-shadow: 0 0 20px primary, duration: 0.3s |
| F1020 | 17.000s | **Terminal border → primary color flash** | boxShadow: 0 0 0 2px primary, duration: 0.1s |
| F1026 | 17.100s | Terminal border restored | boxShadow: 0 0 0 1px border/50, duration: 0.2s |
| F1038 | 17.300s | Empty line | |
| **F1044** | **17.400s** | **URL line appears**: "→  https://myapp-abc123.launchkit.app" | |
| F1044 | 17.400s | URL underline drawn from left to right | width: 0→100%, duration: 0.1s |
| F1044 | 17.400s | URL text in primary color, pulse starts (infinite) | opacity: 1→0.7→1, duration: 1.5s, repeat: -1 |
| F1050 | 17.500s | Scene 06 END | → enter Scene 07 |

## Text Color per Line

| Line | Color | Class |
|----|------|-------|
| `✓` check lines | primary color | `text-primary` |
| Service name after `✓` | muted-foreground | `text-muted-foreground` |
| `🚀 Deploy succeeded` | foreground + glow | `text-foreground` + text-shadow |
| `→` | amber/primary | `text-amber-400` |
| URL | primary, underline | `text-primary underline` |

## GSAP Implementation

```ts
function scene06_deploySuccess() {
  const tl = gsap.timeline()

  // Terminal slide-up
  tl.to(terminalEl, { y: 0, opacity: 1, duration: 0.4, ease: 'power2.out' }, 0)

  // Lines appear (every 300ms)
  const lines = [line1, line2, line3, line4, line5]
  lines.forEach((line, i) => {
    tl.to(line, { opacity: 1, duration: 0.2 }, 0.4 + i * 0.3)
  })

  // 🚀 Deploy succeeded
  tl.to(successLine, {
    scale: 1,
    opacity: 1,
    duration: 0.5,
    ease: 'back.out(1.7)'
  }, 2.0)
  tl.to(successLine, {
    textShadow: '0 0 20px oklch(var(--primary))',
    duration: 0.3
  }, 2.0)

  // Terminal border flash
  tl.to(terminalEl, {
    boxShadow: '0 0 0 2px oklch(var(--primary))',
    duration: 0.1
  }, 2.0)
  tl.to(terminalEl, {
    boxShadow: '0 0 0 1px oklch(var(--border) / 0.5)',
    duration: 0.2
  }, 2.1)

  // URL line
  tl.to(urlLineEl, { opacity: 1, duration: 0.1 }, 2.4)
  tl.to(urlUnderline, { width: '100%', duration: 0.1 }, 2.4)
  tl.to(urlLineEl, {
    opacity: 0.7,
    duration: 0.75,
    yoyo: true,
    repeat: -1
  }, 2.5)

  return tl
}
```

## Component Structure

```tsx
<div ref={terminalEl}
  className="terminal rounded-xl border border-border/50 bg-card/90 backdrop-blur p-5 font-mono text-xs"
  style={{ opacity: 0, transform: 'translateY(60px)' }}
>
  {checkLines.map((line, i) => (
    <div key={i} ref={lineRefs[i]} style={{ opacity: 0 }} className="mb-1">
      <span className="text-primary">✓</span>
      <span className="text-muted-foreground ml-2">{line}</span>
    </div>
  ))}
  <div ref={successLineRef} style={{ opacity: 0, scale: 0.8 }} className="mt-3 mb-1 text-sm font-medium">
    🚀 Deploy succeeded
  </div>
  <div ref={urlLineRef} style={{ opacity: 0 }} className="relative mt-1">
    <span className="text-amber-400">→</span>
    <span ref={urlTextRef} className="text-primary ml-2 relative">
      https://myapp-abc123.launchkit.app
      <span ref={urlUnderlineRef}
        className="absolute bottom-0 left-0 h-px bg-primary"
        style={{ width: 0 }}
      />
    </span>
  </div>
</div>
```

## Transition Exit

After the URL pulse starts, the ghost UI of Scene 07 fades in from the right and the terminal shrinks and moves to the left.
