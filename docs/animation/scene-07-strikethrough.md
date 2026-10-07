# Scene 07 — Old World Struck Through

**Time range**: 17.500s – 21.000s  
**Frame range**: F1050 – F1260 (210 frames total)  
**GSAP position**: `tl.add(scene07(), 17.500)`

## Visual Description

The terminal shrinks and moves to the left. On the right a semi-transparent "old world" ghost UI appears:  
6 dashboards, 6 credit cards, 47 API key cells.  
Red strikethrough lines sweep across them one by one, struck-through elements turn gray, and finally everything breaks into fragments and disperses.

## Ghost UI Layout

```
Left (30% width)      Right (70% width)
┌─────────────┐       ┌──────────────────────────────────────────┐
│             │       │ 🖥 Dashboard 1  🖥 Dashboard 2  🖥 Dashboard 3 │
│  Terminal   │       │ 🖥 Dashboard 4  🖥 Dashboard 5  🖥 Dashboard 6 │
│  (shrunk)   │       │                                          │
│             │       │ 💳 Card 1  💳 Card 2  💳 Card 3           │
│             │       │ 💳 Card 4  💳 Card 5  💳 Card 6           │
│             │       │                                          │
│             │       │ 🔑🔑🔑🔑🔑🔑🔑🔑🔑🔑🔑🔑🔑🔑🔑🔑  (47 cells)│
└─────────────┘       └──────────────────────────────────────────┘
```

## Ghost UI Style

- Overall: `opacity: 0.6, filter: saturate(0.3) sepia(0.2)` → desaturated with a slight red tint
- Dashboard card: `80px × 60px`, gray border, simulating a thumbnail
- Credit card: `100px × 60px`, with the last-4-digits card number format
- API key cell: `56px × 24px`, monospace font, `sk-xxxx-...` truncated

## Frame-by-Frame Event Table

| Frame | Time | Event | Parameters |
|----|------|------|------|
| F1050 | 17.500s | Scene starts | |
| F1050 | 17.500s | Terminal shrinks + moves left | scale: 1→0.65, x: 0→-30%, duration: 0.4s, ease: power2.inOut |
| F1050 | 17.500s | Ghost UI fade-in starts | opacity: 0→0.6, duration: 0.6s |
| F1086 | 18.100s | Ghost UI fully visible | |
| **F1086** | **18.100s** | **Dashboard 1 strikethrough starts** | width: 0→100%, duration: 0.3s, color: #EF4444 |
| F1086 | 18.100s | Dashboard 1 desaturate (struck through) | filter: grayscale(1), opacity: 0.3, duration: 0.2s, delay: 0.3s |
| **F1104** | **18.400s** | **Dashboard 2 strikethrough starts** | +300ms |
| **F1122** | **18.700s** | **Dashboard 3 strikethrough starts** | +300ms |
| **F1140** | **19.000s** | **Dashboard 4 strikethrough starts** | +300ms |
| **F1158** | **19.300s** | **Dashboard 5 strikethrough starts** | +300ms |
| **F1176** | **19.600s** | **Dashboard 6 strikethrough starts** | +300ms |
| F1194 | 19.900s | Dashboard 6 complete → credit cards start | |
| **F1176** | **19.600s** | **Credit Card 1 strikethrough starts** (simultaneous with Dashboard 6) | duration: 0.15s |
| **F1185** | **19.750s** | **Credit Card 2** | +150ms (faster) |
| **F1194** | **19.900s** | **Credit Card 3** | +150ms |
| **F1203** | **20.050s** | **Credit Card 4** | +150ms |
| **F1212** | **20.200s** | **Credit Card 5** | +150ms |
| **F1221** | **20.350s** | **Credit Card 6** | +150ms |
| **F1206** | **20.100s** | **API key fast cascade starts** (47 cells) | |
| F1206 | 20.100s | 47 cells struck through quickly (cascade covers everything within 400ms) | stagger: 400ms/47 ≈ 8.5ms each |
| F1230 | 20.500s | The last API key is struck through | |
| **F1230** | **20.500s** | **Fragment + fade out starts** | |
| F1230 | 20.500s | All ghost UI elements: transform shatter (scale + rotate, random) | duration: 0.5s |
| F1230 | 20.500s | opacity: 0.6→0 | duration: 0.5s, ease: power2.in |
| F1260 | 21.000s | All elements fully dispersed | |
| F1260 | 21.000s | Scene 07 END | → enter Scene 08 |

## Strikethrough Implementation

```tsx
// wrapper for each ghost element
<div className="relative inline-block" ref={el}>
  <div className="content">{/* thumbnail content */}</div>
  <div
    ref={strikeRef}
    className="absolute top-1/2 left-0 h-0.5 bg-red-500"
    style={{ width: 0, transformOrigin: 'left center' }}
  />
</div>
```

```ts
// GSAP strikethrough sequence
dashboards.forEach((item, i) => {
  const t = 0.6 + i * 0.3  // starts after 0.6s, 300ms apart
  tl.to(item.strikeEl, { width: '100%', duration: 0.3 }, t)
  tl.to(item.el, { filter: 'grayscale(1)', opacity: 0.3, duration: 0.2 }, t + 0.3)
})

cards.forEach((item, i) => {
  const t = 2.1 + i * 0.15
  tl.to(item.strikeEl, { width: '100%', duration: 0.15 }, t)
  tl.to(item.el, { filter: 'grayscale(1)', opacity: 0.3, duration: 0.15 }, t + 0.15)
})

// API keys cascade
apiKeys.forEach((item, i) => {
  const t = 2.6 + i * 0.0085  // 400ms / 47 = 8.5ms each
  tl.to(item.strikeEl, { width: '100%', duration: 0.08 }, t)
})
```

## Fragment Effect

```ts
// shatter: each element scatters randomly
ghostEls.forEach((el, i) => {
  const angle = (i / ghostEls.length) * 360
  const distance = 30 + Math.random() * 50
  const dx = Math.cos(angle * Math.PI / 180) * distance
  const dy = Math.sin(angle * Math.PI / 180) * distance

  tl.to(el, {
    x: `+=${dx}`,
    y: `+=${dy}`,
    rotation: (Math.random() - 0.5) * 30,
    scale: 0.5,
    opacity: 0,
    duration: 0.5,
    ease: 'power2.in'
  }, 3.0) // 20.5s global
})
```

## Transition Exit

Once all ghost elements have dispersed, the screen is cleared and the final statement text of Scene 08 appears.
