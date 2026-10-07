# Scene 03 — Logos Absorbed into the Server Box

**Time range**: 7.000s – 9.000s  
**Frame range**: F420 – F540 (120 frames total)  
**GSAP position**: `tl.add(scene03(), 7.000)`

## Visual Description

The server box "absorbs" the 22 logos one after another. Each logo first scales up and then vanishes; at the moment it vanishes, the corresponding spot on the box flashes.
After everything is absorbed, the box turns from a wireframe into a solid and does one bounce.

## Frame-by-Frame Event Table

Each logo takes 200ms to be absorbed (scale up 100ms + scale to 0 100ms)  
Stagger: 70ms / logo  
22 logos × 70ms = 1540ms total

| Frame | Time | Event | Parameters |
|----|------|------|------|
| F420 | 7.000s | Scene starts | |
| **F420** | **7.000s** | **Logo 1 absorption starts** | scale 1.0→1.2, duration: 0.1s |
| F426 | 7.100s | Logo 1: scale 1.2→0 | duration: 0.1s, ease: power2.in |
| F426 | 7.100s | Box panel flash #1 (white glow) | opacity 0→1→0, duration: 0.05s |
| **F424** | **7.067s** | **Logo 2 absorption starts** (+70ms stagger) | |
| F430 | 7.167s | Logo 2 vanishes + box flash | |
| **F428** | **7.133s** | **Logo 3 absorption starts** | |
| F434 | 7.233s | Logo 3 vanishes | |
| **F432** | **7.200s** | **Logo 4 absorption starts** | |
| … | … | (one every 70ms, 22 in total) | |
| **F508** | **8.467s** | **Logo 22 absorption starts** (7.000 + 21×0.070 = 8.470s) | |
| F514 | 8.567s | Logo 22 vanishes, last box flash | |
| **F510** | **8.500s** | **Box solidification starts** (wireframe → frosted aluminum alloy) | |
| F510 | 8.500s | box opacity 0.4→1 | duration: 0.3s |
| F510 | 8.500s | box material: border becomes solid, add box-shadow | duration: 0.3s |
| F528 | 8.800s | **Box bounce starts** | scale 1.0→1.04, duration: 0.15s, ease: power2.out |
| F537 | 8.950s | Box bounce rebound | scale 1.04→1.0, duration: 0.15s, ease: elastic.out(1, 0.3) |
| F540 | 9.000s | Scene 03 END | → enter Scene 04 |

## Absorb Animation GSAP

```ts
function scene03_logoAbsorb() {
  const tl = gsap.timeline()

  logos.forEach((logo, i) => {
    const delay = i * 0.07 // 70ms stagger

    // scale up
    tl.to(logoEls[i], { scale: 1.2, duration: 0.1, ease: 'power1.out' }, delay)
    // scale to 0 (absorbed)
    tl.to(logoEls[i], { scale: 0, duration: 0.1, ease: 'power2.in' }, delay + 0.1)
    // box flash
    tl.to(boxPanelEl, {
      boxShadow: 'inset 0 0 20px rgba(255,255,255,0.8)',
      duration: 0.025,
      yoyo: true,
      repeat: 1
    }, delay + 0.1)
  })

  // Box materialize (starts at 1.5s local = 8.500s global)
  tl.to(serverBoxEl, {
    opacity: 1,
    '--border-color': 'oklch(0.7 0.15 250)',
    boxShadow: '0 0 40px rgba(99,102,241,0.3), 0 20px 60px rgba(0,0,0,0.5)',
    duration: 0.3
  }, 1.5)

  // Bounce
  tl.to(serverBoxEl, { scale: 1.04, duration: 0.15, ease: 'power2.out' }, 1.8)
  tl.to(serverBoxEl, { scale: 1.0, duration: 0.15, ease: 'elastic.out(1, 0.3)' }, 1.95)

  return tl
}
```

## Server Box CSS 3D Structure

```tsx
<div className="server-box" style={{ transformStyle: 'preserve-3d', perspective: '800px' }}>
  <div className="face front" />
  <div className="face back" />
  <div className="face top" />
  <div className="face bottom" />
  <div className="face left" />
  <div className="face right" />
  {/* LED strip (front)*/}
  <div className="led-strip face-front-overlay">
    {[0,1,2,3,4,5].map(i => <div key={i} className="led" data-index={i} />)}
  </div>
</div>
```

```css
.server-box {
  width: 200px;
  height: 120px;
  position: relative;
  transform: rotateX(-15deg) rotateY(25deg);
}
.face {
  position: absolute;
  background: linear-gradient(135deg, #1a1a2e 0%, #16213e 100%);
  border: 1px solid rgba(99,102,241,0.3);
}
.face.front  { width: 200px; height: 120px; transform: translateZ(40px); }
.face.back   { width: 200px; height: 120px; transform: translateZ(-40px) rotateY(180deg); }
.face.top    { width: 200px; height: 80px;  transform: rotateX(90deg) translateZ(40px); }
.face.bottom { width: 200px; height: 80px;  transform: rotateX(-90deg) translateZ(80px); }
.face.left   { width: 80px;  height: 120px; transform: rotateY(-90deg) translateZ(0); }
.face.right  { width: 80px;  height: 120px; transform: rotateY(90deg) translateZ(200px); }
```

## Transition Exit

After the box bounce completes, the LED sequence of Scene 04 starts.
