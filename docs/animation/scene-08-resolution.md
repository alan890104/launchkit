# Scene 08 — Final Statement

**Time range**: 21.000s – 24.000s  
**Frame range**: F1260 – F1440 (180 frames total)  
**GSAP position**: `tl.add(scene08(), 21.000)`

## Visual Description

A clean black screen, centered.  
Three short phrases appear in turn, the LaunchKit logo + CTA rise up, and after two attention pulses the sequence loops and replays.

## Final Copy

```
1 dashboard  ·  1 credit card  ·  1 API key
```

Font spec:
- Weight: `font-light` (300)
- Typeface: IBM Plex Serif (italic), or a monospace-feeling system font
- Color: primary (the LaunchKit main color)
- Size: `text-4xl md:text-5xl`

## Frame-by-Frame Event Table

| Frame | Time | Event | Parameters |
|----|------|------|------|
| F1260 | 21.000s | Scene starts, screen fully black | |
| **F1278** | **21.300s** | **"1 dashboard" fade-in** (+300ms) | opacity: 0→1, y: 8→0, duration: 0.4s, ease: power2.out |
| F1302 | 21.700s | "1 dashboard" fully visible | |
| **F1296** | **21.600s** | **"·" separator #1 fade-in** | opacity: 0→1, duration: 0.2s |
| **F1308** | **21.800s** | **"1 credit card" fade-in** | opacity: 0→1, y: 8→0, duration: 0.4s |
| **F1320** | **22.000s** | **"·" separator #2 fade-in** | opacity: 0→1, duration: 0.2s |
| **F1326** | **22.100s** | **"1 API key" fade-in** | opacity: 0→1, y: 8→0, duration: 0.4s |
| F1350 | 22.500s | All three phrases visible, text glow slightly stronger | text-shadow: 0 0 30px primary/40 |
| **F1350** | **22.500s** | **LaunchKit logo scale-in** | scale: 0→1, opacity: 0→1, duration: 0.3s, ease: back.out(1.7) |
| F1368 | 22.800s | Logo in place | |
| **F1380** | **23.000s** | **CTA "Deploy now →" fade-in** | opacity: 0→1, y: 8→0, duration: 0.3s |
| F1392 | 23.200s | CTA fully visible | |
| **F1392** | **23.200s** | **Attention pulse #1 starts** | |
| F1392 | 23.200s | CTA border glow expands: boxShadow 0→`0 0 0 4px primary/30` | duration: 0.2s |
| F1404 | 23.400s | Glow spreads outward, opacity 0.3→0 | duration: 0.2s |
| **F1422** | **23.700s** | **Attention pulse #2 starts** (+500ms) | |
| F1422 | 23.700s | boxShadow 0→`0 0 0 4px primary/30` | duration: 0.2s |
| F1434 | 23.900s | Glow opacity 0.3→0 | duration: 0.2s |
| F1440 | 24.000s | Scene 08 END → **loop back to Scene 00** | `tl.repeat(-1)` |

## Attention Pulse Implementation

```ts
function attentionPulse(el: Element, startTime: number) {
  tl.to(el, {
    boxShadow: '0 0 0 4px oklch(var(--primary) / 0.3)',
    duration: 0.2
  }, startTime)
  tl.to(el, {
    boxShadow: '0 0 0 12px oklch(var(--primary) / 0)',
    duration: 0.2
  }, startTime + 0.2)
}

attentionPulse(ctaEl, 2.2) // 23.2s global
attentionPulse(ctaEl, 2.7) // 23.7s global
```

## GSAP Implementation

```ts
function scene08_resolution() {
  const tl = gsap.timeline()

  // three phrases (stagger 0.5s apart)
  const phrases = [phrase1, phrase2, phrase3]
  phrases.forEach((el, i) => {
    tl.to(el, { opacity: 1, y: 0, duration: 0.4, ease: 'power2.out' }, 0.3 + i * 0.5)
  })

  // separator (between phrases 1 and 2)
  tl.to(sep1, { opacity: 1, duration: 0.2 }, 0.6)
  tl.to(sep2, { opacity: 1, duration: 0.2 }, 1.0)

  // Logo
  tl.to(logoEl, {
    scale: 1,
    opacity: 1,
    duration: 0.3,
    ease: 'back.out(1.7)'
  }, 1.5)

  // CTA
  tl.to(ctaEl, { opacity: 1, y: 0, duration: 0.3 }, 2.0)

  // Attention pulses
  attentionPulse(ctaEl, 2.2)
  attentionPulse(ctaEl, 2.7)

  return tl
}
```

## Component Structure

```tsx
<div className="flex flex-col items-center justify-center gap-8 text-center">
  {/* main copy */}
  <div className="flex items-center gap-4 text-4xl md:text-5xl font-light text-primary"
    style={{ fontFamily: 'var(--font-ibm-plex-serif)', fontStyle: 'italic' }}>
    <span ref={phrase1Ref} style={{ opacity: 0, transform: 'translateY(8px)' }}>
      1 dashboard
    </span>
    <span ref={sep1Ref} className="text-primary/40" style={{ opacity: 0 }}>·</span>
    <span ref={phrase2Ref} style={{ opacity: 0, transform: 'translateY(8px)' }}>
      1 credit card
    </span>
    <span ref={sep2Ref} className="text-primary/40" style={{ opacity: 0 }}>·</span>
    <span ref={phrase3Ref} style={{ opacity: 0, transform: 'translateY(8px)' }}>
      1 API key
    </span>
  </div>

  {/* Logo */}
  <div ref={logoRef} style={{ opacity: 0, scale: 0 }}
    className="flex items-center gap-2">
    <div className="h-6 w-6 rounded-md bg-primary flex items-center justify-center">
      <Zap className="h-3.5 w-3.5 text-primary-foreground" />
    </div>
    <span className="text-lg font-medium" style={{ fontFamily: 'var(--font-ibm-plex-serif)', fontStyle: 'italic' }}>
      LaunchKit
    </span>
  </div>

  {/* CTA */}
  <Link href="/login" ref={ctaRef} style={{ opacity: 0, transform: 'translateY(8px)' }}>
    <Button size="lg" className="rounded-lg gap-2 font-semibold px-8 min-h-[48px]">
      Deploy now <ArrowRight className="h-4 w-4" />
    </Button>
  </Link>
</div>
```

## Loop Logic

```ts
const masterTl = gsap.timeline({
  repeat: -1,      // loop forever
  repeatDelay: 1,  // pause 1s before each loop
})
// Resetting all elements to their initial state before each repeat is handled automatically by GSAP (set initialValues)
```
