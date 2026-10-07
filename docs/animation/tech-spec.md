# Tech Spec — Hero Animation

## Packages

```bash
cd dashboard && pnpm add gsap simple-icons
```

| Package | Purpose |
|------|------|
| `gsap` | Timeline-style sequencing, precise easing control |
| `simple-icons` | 2000+ brand SVG paths + official brand hex colors |

Three.js and Framer Motion are not needed.

## Architecture

```
src/components/HeroAnimationSequence.tsx   ← main component
src/app/globals.css                        ← add animation keyframes
src/app/page.tsx                           ← replace <ProductDemo /> → <HeroAnimationSequence />
```

## GSAP Timeline Skeleton

```ts
const tl = gsap.timeline({ repeat: -1, repeatDelay: 2 })

tl
  .add(scene00_ide(),         0.000)
  .add(scene01_terminal(),    1.500)
  .add(scene02_logoSummon(),  3.500)
  .add(scene03_logoAbsorb(),  7.000)
  .add(scene04_ledSequence(), 9.000)
  .add(scene05_cardEject(),   11.000)
  .add(scene06_deploySuccess(), 15.000)
  .add(scene07_strikethrough(), 17.500)
  .add(scene08_resolution(),  21.000)
```

## Technical Details

### Logo Flight
- Start: `gsap.set()` to coordinates outside the screen edge (computed dynamically from `getBoundingClientRect`)
- Path: `gsap.to()` with `ease: "power2.in"`, adding a `rotation: ±15` then `0` midway through the motion
- Brand color glow: `filter: drop-shadow(0 0 8px #<hex>)`

### Server Box (CSS 3D)
```css
.server-box {
  transform-style: preserve-3d;
  perspective: 800px;
}
/* 6 faces: front, back, top, bottom, left, right */
```

### Service Cards
- Eject: `gsap.to()` with a custom bezier path (arc trajectory)
- Flip: `gsap.to(card, { rotationY: 180, duration: 0.2 })`
- Float: CSS `@keyframes float-N { 0%,100%{transform:translateY(0)} 50%{transform:translateY(-4px)} }`, each card uses a different duration (2.1s / 2.4s / 2.7s...)

### Connection SVG
```tsx
<svg className="absolute inset-0 pointer-events-none">
  <path
    ref={pathRef}
    d="M cx cy L tx ty"  // computed dynamically
    stroke={brandHex}
    strokeWidth="1.5"
    strokeDasharray={length}
    strokeDashoffset={length}  // GSAP animate to 0
    opacity="0.6"
    filter="url(#glow)"
  />
</svg>
```

### Strikethrough
```tsx
// for each ghost UI element:
<div className="relative">
  <span>content</span>
  <span
    className="absolute left-0 top-1/2 h-0.5 bg-red-500"
    style={{ width: 0 }}  // GSAP animate to 100%
  />
</div>
```

### IntersectionObserver Control
```ts
useEffect(() => {
  const io = new IntersectionObserver(([entry]) => {
    if (entry.isIntersecting) tl.play()
    else tl.pause()
  })
  io.observe(containerRef.current)
  return () => io.disconnect()
}, [])
```

## Provider Logo List (simple-icons key)

| Wave | Icons |
|------|-------|
| Wave 1 (4 items, largest) | `siGooglecloud`, `siAmazonwebservices`, `siNeon`, `siCloudflare` |
| Wave 2 (7 items, medium) | `siUpstash`, `siResend`, `siGithub`, `siDocker`, `siGooglecloudstorage` (for GCS), `siStripe`, `siTwilio` |
| Wave 3 (11 items, small) | `siDatadog`, `siSentry`, `siAuth0`, `siMongodb`, `siSupabase`, `siPlanetscale`, `siVercel`, `siFlyio`, `siHeroku`, `siMicrosoftazure`, `siDigitalocean` |

> If simple-icons has no such key, use `siAmazonwebservices` as a placeholder and swap in a custom SVG later.
