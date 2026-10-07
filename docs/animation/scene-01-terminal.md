# Scene 01 — Terminal Input

**Time range**: 1.500s – 3.500s  
**Frame range**: F90 – F210 (120 frames total)  
**GSAP position**: `tl.add(scene01(), 1.500)`

## Visual Description

The terminal panel takes the lower half of the screen; the editor shrinks to the upper half.  
The prompt cursor blinks, a Chinese command starts being typed, and a spinner appears after Enter.

## Input Text

```
> Help me deploy this project
```

Chinese characters: 8 in total  
Rate: 80ms / character  
Total typing time: 640ms

## Frame-by-Frame Event Table

| Frame | Time | Event | Parameters |
|----|------|------|------|
| F90 | 1.500s | Scene starts, ">" prompt appears, cursor blinks | cursor blink: 500ms interval |
| F96 | 1.600s | Typing starts, typing delay 100ms | |
| F96 | 1.600s | Character 1 appears | char 1 |
| F101 | 1.680s | Character 2 appears | char 2, +80ms |
| F106 | 1.760s | Character 3 appears | char 3 |
| F110 | 1.840s | Character 4 appears | char 4 |
| F115 | 1.920s | Character 5 appears | char 5 |
| F120 | 2.000s | Character 6 appears | char 6 |
| F125 | 2.080s | Character 7 appears | char 7 |
| F130 | 2.160s | Character 8 appears → typing done | char 8 |
| F130 | 2.160s | Cursor stops (stops blinking, stays visible) | |
| F154 | 2.567s | 0.4s pause ends (anticipation), Enter pressed | pause: 400ms |
| F154 | 2.567s | Cursor disappears (opacity 0, instant) | |
| F158 | 2.633s | "◆ Analyzing..." text appears (fade in, 100ms) | |
| F158 | 2.633s | Spinner starts rotating | rotate: 0→360, duration: 0.8s, ease: none, repeat: -1 |
| F210 | 3.500s | Zoom-out starts (scene 02 transition) | editor+terminal scale+blur |
| F210 | 3.500s | Scene 01 END | → enter Scene 02 |

## Spinner Details

```tsx
<span className="spinner" ref={spinnerRef}>◆</span>
```

```ts
gsap.to(spinnerRef.current, {
  rotation: 360,
  duration: 0.8,
  ease: 'none',
  repeat: -1,
  transformOrigin: '50% 50%'
})
```

## Zoom-out Transition

```ts
// starts at t=3.500s (GSAP position 0 relative to scene01 = 2.000s local)
tl.to([editorEl, terminalEl], {
  scale: 0.4,
  opacity: 0.2,
  filter: 'blur(8px)',
  duration: 0.8,
  ease: 'power2.in',
  transformOrigin: 'center center'
}, 2.000) // local time: 3.500 - 1.500 = 2.000
```

## Component Structure

```tsx
<div ref={terminalRef} className="terminal font-mono text-sm">
  <span className="text-muted-foreground">&gt; </span>
  <span ref={inputTextRef} />
  <span ref={cursorRef} className="cursor">█</span>
  <div ref={analyzingRef} style={{ opacity: 0 }} className="mt-1">
    <span ref={spinnerRef} className="inline-block text-primary">◆</span>
    <span className="text-muted-foreground ml-2">Analyzing...</span>
  </div>
</div>
```

## Transition Exit

After the zoom-out completes, the black background fades in, the server box wireframe appears, and Scene 02 begins.
