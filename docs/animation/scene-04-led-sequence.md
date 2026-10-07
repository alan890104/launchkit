# Scene 04 — LED Indicator Sequence

**Time range**: 9.000s – 11.000s  
**Frame range**: F540 – F660 (120 frames total)  
**GSAP position**: `tl.add(scene04(), 9.000)`

## Visual Description

The front of the server box has a row of 6 LED indicators.  
Amber lights turn on in a domino pattern (processing) → the right-hand ones go straight to green → all the amber ones turn green → synchronized pulse.  
There is a heat-haze effect on top of the box, and the fan pattern rotates slowly.

## LED Color Spec

| State | Color | Hex | Box Shadow |
|------|------|-----|------------|
| Off | Dark gray | `#1f1f1f` | none |
| Processing (amber) | Amber | `#F59E0B` | `0 0 8px #F59E0B, 0 0 20px rgba(245,158,11,0.4)` |
| Done (green) | Bright green | `#10B981` | `0 0 8px #10B981, 0 0 20px rgba(16,185,129,0.4)` |

## Frame-by-Frame Event Table

| Frame | Time | Event | Parameters |
|----|------|------|------|
| F540 | 9.000s | Scene starts, all LEDs dark (#1f1f1f) | |
| **F546** | **9.100s** | **LED[0] → amber** (+100ms) | duration: 0.08s |
| F546 | 9.100s | LED[0] pulse #1 ON | brightness: 100% |
| F549 | 9.150s | LED[0] pulse #1 OFF | brightness: 60%, duration: 0.05s |
| F552 | 9.200s | LED[0] pulse #2 ON | brightness: 100%, duration: 0.05s |
| F555 | 9.250s | LED[0] pulse #2 OFF → stays lit | brightness: 80% |
| **F552** | **9.200s** | **LED[1] → amber** (LED[0] +100ms) | duration: 0.08s |
| F552 | 9.200s | LED[1] pulse #1 ON/OFF (same sequence as LED[0], offset 100ms) | |
| **F558** | **9.300s** | **LED[2] → amber** (LED[1] +100ms) | duration: 0.08s |
| **F564** | **9.400s** | **LED[3] → green** (straight to green, skipping amber) | duration: 0.08s |
| **F570** | **9.500s** | **LED[4] → green** (LED[3] +100ms) | duration: 0.08s |
| **F576** | **9.600s** | **LED[5] → green** (LED[4] +100ms) | duration: 0.08s |
| **F606** | **10.100s** | **LED[0] amber→green** (processing→done) | duration: 0.15s, color morph |
| **F612** | **10.200s** | **LED[1] amber→green** | duration: 0.15s |
| **F618** | **10.300s** | **LED[2] amber→green** | duration: 0.15s |
| F618 | 10.300s | All 6 LEDs are green | |
| **F630** | **10.500s** | **Synchronized pulse starts** | whole row in sync |
| F630 | 10.500s | All LEDs brightness 100%→60% | duration: 0.2s |
| F642 | 10.700s | All LEDs brightness 60%→100% | duration: 0.3s |
| F660 | 11.000s | Scene 04 END | → enter Scene 05 |

## Accompanying Animations

### Heat Haze Effect (SVG filter)
```tsx
<svg className="absolute -top-8 left-1/2 -translate-x-1/2 w-24 h-8 pointer-events-none">
  <filter id="heat">
    <feTurbulence type="turbulence" baseFrequency="0.05 0.1"
      numOctaves="2" seed="2" ref={turbRef} />
    <feDisplacementMap in="SourceGraphic" scale="3" />
  </filter>
  <rect width="100%" height="100%" fill="rgba(255,255,255,0.03)" filter="url(#heat)" />
</svg>
```
GSAP animate `turbRef.current.baseFrequency` from `0.05 0.1` → `0.08 0.15` (looping, 2s)

### Fan Rotation
```tsx
<div className="fan-lines absolute" ref={fanRef}>
  {[0,1,2,3].map(i => (
    <div key={i} className="fan-blade" style={{ transform: `rotate(${i*45}deg)` }} />
  ))}
</div>
```
```ts
gsap.to(fanRef.current, { rotation: 360, duration: 3, ease: 'none', repeat: -1 })
```

## Global GSAP Implementation

```ts
function scene04_ledSequence() {
  const tl = gsap.timeline()
  const amber = '#F59E0B'
  const green = '#10B981'
  const amberGlow = '0 0 8px #F59E0B, 0 0 20px rgba(245,158,11,0.4)'
  const greenGlow  = '0 0 8px #10B981, 0 0 20px rgba(16,185,129,0.4)'

  // LED 0-2: amber domino
  ;[0, 1, 2].forEach((idx, i) => {
    const t = 0.1 + i * 0.1
    tl.to(ledEls[idx], { backgroundColor: amber, boxShadow: amberGlow, duration: 0.08 }, t)
    tl.to(ledEls[idx], { opacity: 0.6, duration: 0.05 }, t + 0.08)
    tl.to(ledEls[idx], { opacity: 1, duration: 0.05 }, t + 0.13)
    tl.to(ledEls[idx], { opacity: 0.6, duration: 0.05 }, t + 0.18)
    tl.to(ledEls[idx], { opacity: 0.8, duration: 0.05 }, t + 0.23)
  })

  // LED 3-5: straight to green
  ;[3, 4, 5].forEach((idx, i) => {
    tl.to(ledEls[idx], { backgroundColor: green, boxShadow: greenGlow, duration: 0.08 }, 0.4 + i * 0.1)
  })

  // LED 0-2: amber→green
  ;[0, 1, 2].forEach((idx, i) => {
    tl.to(ledEls[idx], { backgroundColor: green, boxShadow: greenGlow, duration: 0.15 }, 1.1 + i * 0.1)
  })

  // Synchronized pulse (whole row)
  tl.to(ledEls, { opacity: 0.6, duration: 0.2 }, 1.5)
  tl.to(ledEls, { opacity: 1.0, duration: 0.3 }, 1.7)

  // Fan + heat (background, repeat)
  tl.to(fanEl, { rotation: '+=360', duration: 3, ease: 'none', repeat: 1 }, 0)

  return tl
}
```

## Transition Exit

After the all-green LED pulse finishes, the Scene 05 service cards start shooting out from the top of the box.  
Transition foreshadowing: at 0.11s (around F546) a tiny card outline (scale 0.05, opacity 0.2) appears at the top of the box.
