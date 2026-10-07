'use client'

import { useEffect, useRef } from 'react'
import gsap from 'gsap'
import {
  siReact, siNpm,
  siGooglecloud, siCloudflare, siPostgresql, siDocker,
  siUpstash, siResend, siGithub, siStripe, siDatadog, siMongodb, siVercel,
  siSentry, siAuth0, siSupabase, siPlanetscale, siDigitalocean,
  siRedis, siRailway, siKubernetes, siTerraform, siHetzner,
  siGooglecloudstorage, siDotenv,
} from 'simple-icons'

// ─── Scene 02 logo data ───────────────────────────────────────────────────────
type LogoInfo = { id: string; path: string; color: string; size: number; wave: 1 | 2 | 3 }
const LOGOS: LogoInfo[] = [
  // Wave 1 — 48px, four corners
  { id: 'cloudrun',     path: siGooglecloud.path,  color: '#4285F4', size: 48, wave: 1 },
  { id: 'cloudflare',   path: siCloudflare.path,   color: '#F48120', size: 48, wave: 1 },
  { id: 'postgres',     path: siPostgresql.path,   color: '#336791', size: 48, wave: 1 },
  { id: 'docker',       path: siDocker.path,       color: '#2496ED', size: 48, wave: 1 },
  // Wave 2 — 36px, edge midpoints + extras
  { id: 'upstash',      path: siUpstash.path,      color: '#00E9A3', size: 36, wave: 2 },
  { id: 'resend',       path: siResend.path,       color: '#EEEEEE', size: 36, wave: 2 },
  { id: 'github',       path: siGithub.path,       color: '#EEEEEE', size: 36, wave: 2 },
  { id: 'stripe',       path: siStripe.path,       color: '#635BFF', size: 36, wave: 2 },
  { id: 'datadog',      path: siDatadog.path,      color: '#632CA6', size: 36, wave: 2 },
  { id: 'mongodb',      path: siMongodb.path,      color: '#47A248', size: 36, wave: 2 },
  { id: 'vercel',       path: siVercel.path,       color: '#EEEEEE', size: 36, wave: 2 },
  // Wave 3 — 28px, random edges
  { id: 'sentry',       path: siSentry.path,       color: '#9B8FBF', size: 28, wave: 3 },
  { id: 'auth0',        path: siAuth0.path,        color: '#EB5424', size: 28, wave: 3 },
  { id: 'supabase',     path: siSupabase.path,     color: '#3FCF8E', size: 28, wave: 3 },
  { id: 'planetscale',  path: siPlanetscale.path,  color: '#EEEEEE', size: 28, wave: 3 },
  { id: 'digitalocean', path: siDigitalocean.path, color: '#0080FF', size: 28, wave: 3 },
  { id: 'redis',        path: siRedis.path,        color: '#FF4438', size: 28, wave: 3 },
  { id: 'railway',      path: siRailway.path,      color: '#EEEEEE', size: 28, wave: 3 },
  { id: 'kubernetes',   path: siKubernetes.path,   color: '#326CE5', size: 28, wave: 3 },
  { id: 'terraform',    path: siTerraform.path,    color: '#7B42BC', size: 28, wave: 3 },
  { id: 'hetzner',      path: siHetzner.path,      color: '#D50C2D', size: 28, wave: 3 },
]

// ─── Scene 05 service grid data ──────────────────────────────────────────────
// Row-major order: [row][col], (0,0) = top-left
// Center = index 4 [row1, col1]
type GridCard = { id: string; label: string; sub: string; color: string; iconPath: string }
const GRID_CARDS: GridCard[] = [
  { id: 'storage',    label: 'Storage',   sub: 'GCS Bucket',        color: '#FBBC04', iconPath: siGooglecloudstorage.path }, // [0,0]
  { id: 'postgres',   label: 'Database',  sub: 'PostgreSQL / Neon', color: '#00E599', iconPath: siPostgresql.path        }, // [0,1]
  { id: 'cloudflare', label: 'CDN / DNS', sub: 'Cloudflare',        color: '#F48120', iconPath: siCloudflare.path        }, // [0,2]
  { id: 'email',      label: 'Email',     sub: 'Resend',            color: '#9CA3AF', iconPath: siResend.path            }, // [1,0]
  { id: 'backend',    label: 'Cloud Run', sub: 'us-central1',       color: '#4285F4', iconPath: siGooglecloud.path       }, // [1,1] ← CENTER
  { id: 'secrets',    label: 'Secrets',   sub: '6 env vars',        color: '#EF4444', iconPath: siDotenv.path            }, // [1,2]
  { id: 'github',     label: 'CI / CD',   sub: 'GitHub Actions',    color: '#E8E8E8', iconPath: siGithub.path            }, // [2,0]
  { id: 'redis',      label: 'Cache',     sub: 'Upstash Redis',     color: '#00E9A3', iconPath: siUpstash.path           }, // [2,1]
  { id: 'domain',     label: 'Domain',    sub: 'myapp.launchkit.io',color: '#6366F1', iconPath: siVercel.path            }, // [2,2]
]


function PythonIcon({ size = 13 }: { size?: number }) {
  // Python logo: top half blue, bottom half yellow — two-path official colors
  return (
    <svg viewBox="0 0 24 24" width={size} height={size} style={{ flexShrink: 0 }} aria-hidden="true">
      <path fill="#3776AB" d="M11.914 0C5.82 0 6.2 2.656 6.2 2.656l.007 2.752h5.814v.826H3.9S0 5.789 0 11.969c0 6.18 3.403 5.959 3.403 5.959h2.03v-2.867s-.109-3.403 3.35-3.403h5.769s3.24.052 3.24-3.13V3.23S18.28 0 11.914 0zm-3.21 1.867a1.044 1.044 0 1 1 0 2.088 1.044 1.044 0 0 1 0-2.088z"/>
      <path fill="#FFD43B" d="M12.086 24c6.094 0 5.714-2.656 5.714-2.656l-.007-2.752H12v-.826h8.1S24 18.211 24 12.031c0-6.18-3.403-5.959-3.403-5.959h-2.03v2.867s.109 3.403-3.35 3.403H9.448s-3.24-.052-3.24 3.13V20.77S5.72 24 12.086 24zm3.21-1.867a1.044 1.044 0 1 1 0-2.088 1.044 1.044 0 0 1 0 2.088z"/>
    </svg>
  )
}
function ReactIcon({ size = 13 }: { size?: number }) {
  return (
    <svg viewBox="0 0 24 24" width={size} height={size} style={{ fill: `#${siReact.hex}`, flexShrink: 0 }}>
      <path d={siReact.path} />
    </svg>
  )
}
function NpmIcon({ size = 13 }: { size?: number }) {
  return (
    <svg viewBox="0 0 24 24" width={size} height={size} style={{ fill: `#${siNpm.hex}`, flexShrink: 0 }}>
      <path d={siNpm.path} />
    </svg>
  )
}

// ─── Python code definition ───────────────────────────────────────────────────
// Each segment: [text, color]
type Seg = [string, string]
type Line = Seg[]

const C = {
  kw:    '#C586C0',  // keyword: from, import, True, False
  fn:    '#DCDCAA',  // function name
  str:   '#CE9178',  // string literal
  num:   '#B5CEA8',  // number
  cmt:   '#6A9955',  // comment
  cls:   '#4EC9B0',  // class name
  mod:   '#9CDCFE',  // module / variable
  punc:  '#D4D4D4',  // punctuation / default
}

// Highlight target line indices (0-based)
const HL_DB    = [4, 5, 6, 7, 8, 9]     // comment + create_engine block (lines 5–10)
const HL_REDIS = [12, 13, 14, 15, 16]   // comment + redis.Redis block (lines 13–17)

const LINES: Line[] = [
  // 0: from sqlalchemy import create_engine, Column, Integer
  [[`from `,C.kw],[`sqlalchemy`,C.mod],[` import `,C.kw],[`create_engine`,C.fn],[`, `,C.punc],[`Column`,C.cls],[`, `,C.punc],[`Integer`,C.cls]],
  // 1: from sqlalchemy.orm import sessionmaker, DeclarativeBase
  [[`from `,C.kw],[`sqlalchemy.orm`,C.mod],[` import `,C.kw],[`sessionmaker`,C.fn],[`, `,C.punc],[`DeclarativeBase`,C.cls]],
  // 2: import redis
  [[`import `,C.kw],[`redis`,C.mod]],
  // 3: blank
  [[``,C.punc]],
  // 4: # ── Database connection
  [[`# ── Database connection ─────────────────────────────`,C.cmt]],
  // 5: engine = create_engine(
  [[`engine`,C.mod],[` = `,C.punc],[`create_engine`,C.fn],[`(`,C.punc]],
  // 6:     "postgresql://admin:secret@localhost/mydb",
  [[`    `,C.punc],[`"postgresql://admin:secret@localhost/mydb"`,C.str],[`,`,C.punc]],
  // 7:     pool_size=10,
  [[`    `,C.punc],[`pool_size`,C.mod],[`=`,C.punc],[`10`,C.num],[`,`,C.punc]],
  // 8:     echo=False,
  [[`    `,C.punc],[`echo`,C.mod],[`=`,C.punc],[`False`,C.kw],[`,`,C.punc]],
  // 9: )
  [[`)`,C.punc]],
  // 10: Session = sessionmaker(bind=engine)
  [[`Session`,C.cls],[` = `,C.punc],[`sessionmaker`,C.fn],[`(bind=engine)`,C.punc]],
  // 11: blank
  [[``,C.punc]],
  // 12: # ── Redis cache
  [[`# ── Redis cache ─────────────────────────────────────`,C.cmt]],
  // 13: cache = redis.Redis(
  [[`cache`,C.mod],[` = `,C.punc],[`redis`,C.mod],[`.`,C.punc],[`Redis`,C.fn],[`(`,C.punc]],
  // 14:     host="localhost", port=6379,
  [[`    `,C.punc],[`host`,C.mod],[`=`,C.punc],[`"localhost"`,C.str],[`, `,C.punc],[`port`,C.mod],[`=`,C.punc],[`6379`,C.num],[`,`,C.punc]],
  // 15:     decode_responses=True,
  [[`    `,C.punc],[`decode_responses`,C.mod],[`=`,C.punc],[`True`,C.kw],[`,`,C.punc]],
  // 16: )
  [[`)`,C.punc]],
]

// ─── File tree data ───────────────────────────────────────────────────────────
type TreeNode = {
  indent: number
  chevron?: string
  label: string
  color: string
  active?: boolean
  fileIcon?: 'python' | 'react' | 'npm' | 'text'
}

const TREE: TreeNode[] = [
  { indent: 0, chevron: '▼', label: 'my-startup',        color: '#CCCCCC' },
  { indent: 1, chevron: '▼', label: 'backend',           color: '#CCCCCC' },
  { indent: 2,               label: 'app.py',             color: '#FFFFFF', active: true, fileIcon: 'python' },
  { indent: 2,               label: 'models.py',          color: '#CCCCCC', fileIcon: 'python' },
  { indent: 2,               label: 'requirements.txt',   color: '#CCCCCC', fileIcon: 'python' },
  { indent: 1, chevron: '▶', label: 'frontend',          color: '#CCCCCC' },
  { indent: 2,               label: 'App.tsx',            color: '#CCCCCC', fileIcon: 'react' },
  { indent: 2,               label: 'index.tsx',          color: '#CCCCCC', fileIcon: 'react' },
  { indent: 2,               label: 'package.json',       color: '#CCCCCC', fileIcon: 'npm' },
]

function TreeIcon({ type }: { type?: TreeNode['fileIcon'] }) {
  if (type === 'python') return <PythonIcon size={13} />
  if (type === 'react')  return <ReactIcon  size={13} />
  if (type === 'npm')    return <NpmIcon    size={13} />
  if (type === 'text')   return <span style={{ width: 13, height: 13, display: 'inline-block', color: '#888', fontSize: 11, lineHeight: '13px' }}>📄</span>
  return null
}

// ─── Claude Code ASCII-art header ────────────────────────────────────────────
// Exact output of print_banner.py — 3 lines, no more no less
// lineHeight === fontSize so block chars tile with zero inter-row gap
function ClaudeCodeHeader() {
  const coral = '#C87257'
  const rows = [
    { art: ' ▐▛███▜▌',  info: <><span style={{ color: '#FFFFFF', fontWeight: 700 }}>Claude Code</span><span style={{ color: '#999' }}> v2.1.92</span></> },
    { art: '▝▜█████▛▘', info: null },
    { art: '  ▘▘ ▝▝',   info: <span style={{ color: '#666' }}>Sonnet 4.6 with high effort · Claude Max</span> },
  ]
  return (
    <div style={{ fontFamily: '"Fira Code", Menlo, monospace', fontSize: 13, lineHeight: '13px', marginBottom: 8 }}>
      {rows.map(({ art, info }, i) => (
        <div key={i} style={{ display: 'flex', alignItems: 'center' }}>
          <span style={{ color: coral, width: '10ch', whiteSpace: 'pre', flexShrink: 0 }}>{art}</span>
          <span style={{ marginLeft: 8 }}>{info}</span>
        </div>
      ))}
    </div>
  )
}

// ─── Component ───────────────────────────────────────────────────────────────
export default function HeroAnimationSequence() {
  const containerRef   = useRef<HTMLDivElement>(null)
  const vsCodeRef      = useRef<HTMLDivElement>(null)
  const codeInnerRef   = useRef<HTMLDivElement>(null)   // ← only this scrolls
  const lineRefs       = useRef<(HTMLDivElement | null)[]>([])
  const termPanelRef   = useRef<HTMLDivElement>(null)
  const inputTextRef    = useRef<HTMLSpanElement>(null)
  const cursorBlinkRef  = useRef<HTMLSpanElement>(null)
  const thinkingRowRef  = useRef<HTMLDivElement>(null)
  const thinkingSymRef  = useRef<HTMLSpanElement>(null)
  const flashOverlayRef = useRef<HTMLDivElement>(null)
  const stageRef        = useRef<HTMLDivElement>(null)
  const serverBoxRef    = useRef<HTMLDivElement>(null)
  const logoRefs        = useRef<(HTMLDivElement | null)[]>([])
  const ledRefs         = useRef<(SVGCircleElement | null)[]>([])
  const gridRef         = useRef<HTMLDivElement>(null)
  const gridCardRefs    = useRef<(HTMLDivElement | null)[]>([])
  // Scene 06 — deploy output in Claude Code terminal + cursor
  const thinkingIndicatorRef = useRef<HTMLDivElement>(null)  // the ✢ Wibbling row
  const deployOutputRef      = useRef<HTMLDivElement>(null)
  const deployOutLineRefs    = useRef<(HTMLDivElement | null)[]>([])
  const urlOutputRef         = useRef<HTMLSpanElement>(null)
  const urlUnderlineRef      = useRef<HTMLSpanElement>(null)
  const mouseCursorRef       = useRef<HTMLDivElement>(null)
  // Scene 08 — browser window
  const browserWindowRef = useRef<HTMLDivElement>(null)
  const phrase1Ref     = useRef<HTMLSpanElement>(null)
  const phrase2Ref     = useRef<HTMLSpanElement>(null)
  const phrase3Ref     = useRef<HTMLSpanElement>(null)
  const sep1Ref        = useRef<HTMLSpanElement>(null)
  const sep2Ref        = useRef<HTMLSpanElement>(null)
  const logoRef08      = useRef<HTMLDivElement>(null)
  const ctaRef08       = useRef<HTMLDivElement>(null)

  useEffect(() => {
    let masterTl: gsap.core.Timeline

    const ctx = gsap.context(() => {
      masterTl = gsap.timeline({ repeat: -1, repeatDelay: 2, paused: true })

      masterTl
        .add(buildScene00(), 0.000)
        .add(buildScene01(), 7.000)
        .add(buildScene03(), 9.000)   // Scene 02 removed
        .add(buildScene04(), 12.700)  // immediately after scene03
        .add(buildScene05(), 14.500)  // immediately after scene04
        .add(buildScene06(), 17.500)  // zoom-out → Claude Code output + cursor click
        .add(buildScene08(), 23.000)  // final resolution (scene07 removed)
    }, containerRef)

    const io = new IntersectionObserver(([entry]) => {
      if (entry.isIntersecting) masterTl?.play()
      else masterTl?.pause()
    }, { threshold: 0.1 })

    if (containerRef.current) io.observe(containerRef.current)

    return () => { io.disconnect(); ctx.revert() }
  }, [])

  // ── Scene 00: VS Code + Screen Studio camera zoom ─────────────────────────
  function buildScene00() {
    const tl        = gsap.timeline()
    const vscode    = vsCodeRef.current
    const codeInner = codeInnerRef.current
    const lines     = lineRefs.current
    if (!vscode || !codeInner) return tl

    // Element groups
    const dbEls    = HL_DB.map(i => lines[i]).filter(Boolean)    as HTMLDivElement[]
    const rdEls    = HL_REDIS.map(i => lines[i]).filter(Boolean) as HTMLDivElement[]
    const allIdxs  = LINES.map((_, i) => i)
    const nonDbEls = allIdxs.filter(i => !HL_DB.includes(i)).map(i => lines[i]).filter(Boolean)    as HTMLDivElement[]
    const nonRdEls = allIdxs.filter(i => !HL_REDIS.includes(i)).map(i => lines[i]).filter(Boolean) as HTMLDivElement[]
    const allEls   = allIdxs.map(i => lines[i]).filter(Boolean) as HTMLDivElement[]

    // Scroll values to center each block vertically in the zoomed viewport
    // Editor visible height @ scale 1.75 ≈ 149px → center at 74px from editor top
    // Block center = padding(8) + line_center * 21
    // DB center (lines 4–9, mid=6.5):  8 + 6.5×21 = 144.5px → scroll = -(144.5-74) = -71
    // Redis center (lines 12–16, mid=14): 8 + 14×21 = 302px  → scroll = -(302-74) = -228
    const SCROLL_DB    = -71
    const SCROLL_REDIS = -228

    const HL_STYLE  = { backgroundColor: 'rgba(99,102,241,0.13)', borderLeftColor: 'rgba(99,102,241,0.85)' }
    const CLR_STYLE = { backgroundColor: 'transparent', borderLeftColor: 'transparent' }

    // ── Loop reset (t=0): restore everything that previous scenes touched ─────
    tl.set(vscode, { opacity: 1, scale: 1, filter: 'none', transformOrigin: '50% 45%' }, 0)
    if (stageRef.current)  tl.set(stageRef.current, { opacity: 0 }, 0)
    if (serverBoxRef.current) tl.set(serverBoxRef.current, { opacity: 1, scale: 1, filter: 'drop-shadow(0 0 18px rgba(245,158,11,0.25))' }, 0)
    // Scene 06 / 08 resets
    if (thinkingIndicatorRef.current) tl.set(thinkingIndicatorRef.current, { opacity: 1, display: 'block' }, 0)
    // deployOutputRef: hide via display:none only — DO NOT set GSAP opacity (would block lines)
    if (mouseCursorRef.current)       tl.set(mouseCursorRef.current,       { opacity: 0, x: 0, y: 0 }, 0)
    if (browserWindowRef.current)     tl.set(browserWindowRef.current,     { opacity: 0, scale: 1 }, 0)
    tl.call(() => {
      if (inputTextRef.current)   inputTextRef.current.textContent = ''
      if (cursorBlinkRef.current) {
        cursorBlinkRef.current.style.animationPlayState = 'running'
        cursorBlinkRef.current.style.opacity = '1'
      }
      if (thinkingRowRef.current) {
        thinkingRowRef.current.style.display = 'none'
        thinkingRowRef.current.style.opacity = '0'
      }
      // deployOutputRef hides via display:none so its container opacity stays clean for scene06
      if (deployOutputRef.current) deployOutputRef.current.style.display = 'none'
    }, [], 0)

    // ── Step 1: Zoom in + scroll → Postgres / SQLAlchemy centered ────────────
    tl.to(vscode,    { scale: 1.75, duration: 1.5, ease: 'power2.inOut' }, 0.3)
    tl.to(codeInner, { y: SCROLL_DB, duration: 1.5, ease: 'power2.inOut' }, 0.3)

    // Highlight ONLY DB lines, dim everything else
    tl.to(nonDbEls, { opacity: 0.2, duration: 0.35 }, 1.8)
    tl.to(dbEls,    { ...HL_STYLE,  duration: 0.35 }, 1.8)

    // Hold DB highlighted (1.4s visible)

    // ── Step 2: Scroll → Redis centered, swap highlight ───────────────────────
    // Un-highlight DB, dim it
    tl.to(dbEls, { ...CLR_STYLE, opacity: 0.2, duration: 0.4 }, 3.2)
    // Scroll to Redis
    tl.to(codeInner, { y: SCROLL_REDIS, duration: 1.0, ease: 'power2.inOut' }, 3.2)
    // Highlight ONLY Redis (non-redis lines stay dim)
    tl.to(nonRdEls, { opacity: 0.2, duration: 0.35 }, 3.6)
    tl.to(rdEls,    { ...HL_STYLE, opacity: 1, duration: 0.35 }, 3.6)

    // Hold Redis highlighted (1.4s visible)

    // ── Step 3: Remove all highlights + zoom out + scroll reset ───────────────
    tl.to(allEls,    { ...CLR_STYLE, opacity: 1, duration: 0.35 }, 5.4)
    tl.to(vscode,    { scale: 1, duration: 1.1, ease: 'power2.inOut' }, 5.6)
    tl.to(codeInner, { y: 0,    duration: 1.1, ease: 'power2.inOut' }, 5.6)

    // Hold full view (t=6.7s → 7.2s)

    // ── Step 4: Zoom into terminal (mascot + cursor fully visible) ───────────
    // Terminal panel occupies the bottom ~160px of the 500px component.
    // Panel center ≈ 500 - 80 = 420px from top → 420/500 = 84%
    // Scale 2.0 around that origin: visible window = 500/2.0 = 250px
    // centered at 420px → shows from ~295px to 500px (full terminal + tiny sliver of editor)
    tl.set(vscode, { transformOrigin: '0% 100%' }, 7.2)
    tl.to(vscode,  { scale: 2.0, duration: 1.0, ease: 'power2.inOut' }, 7.2)

    if (termPanelRef.current) {
      tl.to(termPanelRef.current, { boxShadow: '0 -1px 0 0 rgba(99,102,241,0.8)', duration: 0.4 }, 8.2)
    }

    return tl
  }

  // ── Scene 01: Claude Code terminal typing ─────────────────────────────────
  function buildScene01() {
    const tl        = gsap.timeline()
    const inputEl   = inputTextRef.current
    const cursorEl  = cursorBlinkRef.current
    if (!inputEl || !cursorEl) return tl

    const thinkingEl = thinkingRowRef.current
    const thinkingSymEl = thinkingSymRef.current
    const text = 'Deploy this'

    // Reset state at scene start
    tl.call(() => {
      inputEl.textContent = ''
      cursorEl.style.animationPlayState = 'running'
      cursorEl.style.opacity = '1'
      if (thinkingEl) {
        thinkingEl.style.display = 'none'
        thinkingEl.style.opacity = '0'
      }
    }, [], 0)

    // Type one character every 80ms, 100ms initial delay
    text.split('').forEach((char, i) => {
      tl.call(() => { inputEl.textContent += char }, [], 0.1 + i * 0.08)
    })

    // Typing done: cursor stops blinking, holds visible
    const typingDone = 0.1 + (text.length - 1) * 0.08 + 0.08
    tl.call(() => { cursorEl.style.animationPlayState = 'paused' }, [], typingDone)

    // 0.4s anticipation → Enter: cursor disappears
    const enterTime = typingDone + 0.4
    tl.set(cursorEl, { opacity: 0 }, enterTime)

    // Show the after-Enter block: flip display then fade in
    if (thinkingEl) {
      tl.call(() => { thinkingEl.style.display = 'block' }, [], enterTime + 0.04)
      tl.to(thinkingEl, { opacity: 1, duration: 0.15 }, enterTime + 0.05)
    }

    // ✢ slowly rotates
    if (thinkingSymEl) {
      tl.to(thinkingSymEl, {
        rotation: 360 * 3,
        duration: 2.0,
        ease: 'none',
        transformOrigin: '50% 50%',
      }, enterTime + 0.05)
    }

    return tl
  }
  // ── Scene 03: punch-through → server box → logo absorption ──────────────
  function buildScene03() {
    const tl        = gsap.timeline()
    const vscode    = vsCodeRef.current
    const flash     = flashOverlayRef.current
    const stage     = stageRef.current
    const sBox      = serverBoxRef.current
    const container = containerRef.current
    if (!vscode || !flash || !stage || !container) return tl

    const W  = container.clientWidth
    const H  = 500
    const CX = W / 2
    const CY = H / 2

    // ── Transition: reuse Scene 00's transformOrigin '0% 100%' so the camera stays continuous ──────────
    // Scene 00 ends at scale=2.0, origin='0% 100%'; keep pushing in here without changing the origin
    tl.to(vscode, { scale: 7, filter: 'blur(28px)', duration: 0.7, ease: 'power2.in' }, 0)
    tl.to(flash, { opacity: 1, duration: 0.10 }, 0.7)
    tl.to(flash, { opacity: 0, duration: 0.18 }, 0.80)
    tl.set(vscode, { opacity: 0 }, 0.75)
    tl.set(stage,  { opacity: 1 }, 0.75)

    // ── Server box appears ───────────────────────────────────────────────────────
    if (sBox) tl.to(sBox, { opacity: 1, duration: 0.35 }, 1.0)

    // ── Logos appear from all directions and get absorbed ───────────────────────────────────────────
    // Each logo is spread evenly around an ellipse centered on the server box
    // Fade in, then get absorbed right away: scale up a bit -> scale to 0 and vanish, box flashes white
    const RX = 200, RY = 145   // ellipse radii (where logos appear)
    const STAGGER = 0.07        // 70ms per logo

    LOGOS.forEach((logo, i) => {
      const el = logoRefs.current[i]
      if (!el) return

      const angle   = (i / LOGOS.length) * Math.PI * 2 - Math.PI / 2
      const startX  = CX + RX * Math.cos(angle) - logo.size / 2
      const startY  = CY + RY * Math.sin(angle) - logo.size / 2
      const t0      = 1.4 + i * STAGGER

      // Appear
      tl.set(el, { x: startX, y: startY, scale: 1, opacity: 0,
        filter: `drop-shadow(0 0 6px ${logo.color})` }, t0)
      tl.to(el, { opacity: 1, duration: 0.08 }, t0)

      // Hold briefly, then get absorbed
      tl.to(el, { scale: 1.25, duration: 0.08, ease: 'power1.out' }, t0 + 0.12)
      tl.to(el, {
        scale: 0, opacity: 0,
        x: CX - logo.size / 2,
        y: CY - logo.size / 2,
        duration: 0.14, ease: 'power2.in',
      }, t0 + 0.20)

      // Box flashes white
      if (sBox) {
        tl.to(sBox, { filter: 'drop-shadow(0 0 22px rgba(255,255,255,0.9))', duration: 0.03 }, t0 + 0.20)
        tl.to(sBox, { filter: 'drop-shadow(0 0 18px rgba(245,158,11,0.25))', duration: 0.12 }, t0 + 0.23)
      }
    })

    // ── After everything is absorbed: box bounce ─────────────────────────────────────────────
    const allDone = 1.4 + (LOGOS.length - 1) * STAGGER + 0.34
    if (sBox) {
      tl.to(sBox, { scale: 1.06, duration: 0.14, ease: 'power2.out' }, allDone)
      tl.to(sBox, { scale: 1.00, duration: 0.22, ease: 'elastic.out(1, 0.4)' }, allDone + 0.14)
    }

    return tl
  }
  // ── Scene 04: LED indicator sequence ─────────────────────────────────────────────
  function buildScene04() {
    const tl   = gsap.timeline()
    const leds = ledRefs.current
    if (leds.every(l => !l)) return tl

    const amber     = '#F59E0B'
    const green     = '#10B981'
    const dim       = '#1a1a18'
    const amberGlow = 'drop-shadow(0 0 4px #F59E0B) drop-shadow(0 0 10px rgba(245,158,11,0.5))'
    const greenGlow = 'drop-shadow(0 0 4px #10B981) drop-shadow(0 0 10px rgba(16,185,129,0.5))'

    // Reset all LEDs to dim at scene start
    leds.forEach(el => { if (el) tl.set(el, { fill: dim, opacity: 1, filter: 'none' }, 0) })

    // LED 0-2: amber domino (processing) — starts as soon as the scene enters
    ;[0, 1, 2].forEach((idx, i) => {
      const el = leds[idx]
      if (!el) return
      const t = i * 0.1   // 0s, 0.1s, 0.2s — no pre-delay
      tl.to(el, { fill: amber, filter: amberGlow, duration: 0.08 }, t)
      tl.to(el, { opacity: 0.5, duration: 0.05 }, t + 0.08)
      tl.to(el, { opacity: 1.0, duration: 0.05 }, t + 0.13)
      tl.to(el, { opacity: 0.6, duration: 0.05 }, t + 0.18)
      tl.to(el, { opacity: 0.85, duration: 0.06 }, t + 0.23)
    })

    // LED 3-5: go straight to green
    ;[3, 4, 5].forEach((idx, i) => {
      const el = leds[idx]
      if (!el) return
      tl.to(el, { fill: green, filter: greenGlow, duration: 0.08 }, 0.3 + i * 0.1)
    })

    // LED 0-2: amber → green
    ;[0, 1, 2].forEach((idx, i) => {
      const el = leds[idx]
      if (!el) return
      tl.to(el, { fill: green, filter: greenGlow, opacity: 1, duration: 0.15 }, 0.8 + i * 0.1)
    })

    // Whole row pulses in sync
    const allLeds = leds.filter(Boolean) as SVGCircleElement[]
    tl.to(allLeds, { opacity: 0.35, duration: 0.20, ease: 'power1.in' }, 1.2)
    tl.to(allLeds, { opacity: 1.00, duration: 0.30, ease: 'power1.out' }, 1.40)

    return tl
  }
  function buildScene05() {
    const tl     = gsap.timeline()
    const sBox   = serverBoxRef.current
    const gcards = gridCardRefs.current

    const W   = containerRef.current?.clientWidth ?? 800
    const H   = 500
    const PAD = 14   // padding around the full 9-card grid
    const GAP = 10   // gap between cards

    // FIXED card size — same at every stage (no resizing)
    const CW = Math.floor((W - 2 * PAD - 2 * GAP) / 3)
    const CH = Math.floor((H - 2 * PAD - 2 * GAP) / 3)

    // Top-left position of a card whose center is at (stage_center + dx, stage_center + dy)
    const px = (col: number) => W / 2 + (col - 1) * (CW + GAP) - CW / 2
    const py = (row: number) => H / 2 + (row - 1) * (CH + GAP) - CH / 2

    // ── Reset: all cards hidden at their final grid positions ─────────────────
    GRID_CARDS.forEach((_, i) => {
      const el = gcards[i]
      if (!el) return
      tl.set(el, {
        x: px(i % 3), y: py(Math.floor(i / 3)),
        width: CW, height: CH,
        scale: 0, opacity: 0, transformOrigin: 'center center',
      }, 0)
    })

    // ── Server box fades out ─────────────────────────────────────────────────
    if (sBox) tl.to(sBox, { opacity: 0, duration: 0.30, ease: 'power2.in' }, 0)

    // ── Stage 1 (t=0.3): center card springs in ──────────────────────────────
    tl.to(gcards[4], { scale: 1, opacity: 1, duration: 0.55, ease: 'elastic.out(1, 0.45)' }, 0.3)

    // ── Stage 2 (t=0.95): top + bottom grow from center (cell division ↑↓) ───
    // Cards start at center's y-position and spring to their row positions
    const t2 = 0.95
    ;[1, 7].forEach((idx) => {
      const row = Math.floor(idx / 3)   // 0 or 2
      const el  = gcards[idx]
      if (!el) return
      tl.set(el, { x: px(1), y: py(1), scale: 0.2, opacity: 0 }, t2)
      tl.to(el, { x: px(1), y: py(row), scale: 1, opacity: 1, duration: 0.65, ease: 'elastic.out(1, 0.42)' }, t2)
    })

    // ── Stage 3 (t=1.85): left + right columns grow from center (←  →) ───────
    // Each card starts at the center column's x-position and springs sideways
    const t3 = 1.85
    // Left column (indices 0, 3, 6): start at col-1 x, spring to col-0 x
    ;[0, 3, 6].forEach((idx, ii) => {
      const row = Math.floor(idx / 3)
      const el  = gcards[idx]
      if (!el) return
      tl.set(el, { x: px(1), y: py(row), scale: 0.2, opacity: 0 }, t3)
      tl.to(el, { x: px(0), scale: 1, opacity: 1, duration: 0.65, ease: 'elastic.out(1, 0.42)' }, t3 + ii * 0.06)
    })
    // Right column (indices 2, 5, 8): start at col-1 x, spring to col-2 x
    ;[2, 5, 8].forEach((idx, ii) => {
      const row = Math.floor(idx / 3)
      const el  = gcards[idx]
      if (!el) return
      tl.set(el, { x: px(1), y: py(row), scale: 0.2, opacity: 0 }, t3)
      tl.to(el, { x: px(2), scale: 1, opacity: 1, duration: 0.65, ease: 'elastic.out(1, 0.42)' }, t3 + ii * 0.06)
    })

    return tl
  }
  function buildScene06() {
    const tl          = gsap.timeline()
    const vscode      = vsCodeRef.current
    const flash       = flashOverlayRef.current
    const stage       = stageRef.current
    const thinking    = thinkingIndicatorRef.current
    const deployOut   = deployOutputRef.current
    const outLines    = deployOutLineRefs.current
    const urlUnder    = urlUnderlineRef.current
    const cursor      = mouseCursorRef.current
    const termPanel   = termPanelRef.current
    const browser     = browserWindowRef.current
    if (!vscode || !flash || !stage) return tl

    // ── Reset at t=0 ─────────────────────────────────────────────────────────
    outLines.forEach(el => { if (el) tl.set(el, { opacity: 0 }, 0) })
    if (urlUnder) tl.set(urlUnder, { width: 0 }, 0)

    // ── Option A: Stage shrinks and fades out while vsCode fades in at normal scale (cross-dissolve)────────────────
    // Reset Stage's transformOrigin to the center so the shrink animation collapses toward the center
    tl.set(stage, { transformOrigin: '50% 50%', scale: 1, opacity: 1 }, 0)
    // Reset vsCode to its normal state but invisible
    tl.set(vscode, { opacity: 0, scale: 0.95, filter: 'none', transformOrigin: '50% 50%' }, 0)

    // Stage (9 cards) shrinks and fades out
    tl.to(stage, { scale: 0.88, opacity: 0, duration: 0.55, ease: 'power2.in' }, 0)

    // vsCode fades in slightly later (0.15s overlap)
    tl.to(vscode, { opacity: 1, scale: 1, duration: 0.55, ease: 'power2.out' }, 0.15)

    // Terminal border glow (left by scene 00) fades away as we settle back
    if (termPanel) tl.to(termPanel, { boxShadow: 'none', duration: 0.3 }, 0.8)

    // ── At zoom-out landing: fade out thinking indicator, show deploy output ──
    // Fade out ✢ Wibbling... row
    if (thinking) tl.to(thinking, { opacity: 0, duration: 0.25 }, 1.1)
    // Collapse the thinking row so it no longer takes up vertical space
    if (thinking) tl.set(thinking, { display: 'none' }, 1.36)

    // Show deployOutput container (GSAP set handles display; no opacity override needed)
    if (deployOut) tl.set(deployOut, { display: 'block' }, 1.2)

    // 5 check lines — stagger 280ms
    for (let i = 0; i < 5; i++) {
      const el = outLines[i]
      if (el) tl.to(el, { opacity: 1, duration: 0.18 }, 1.3 + i * 0.28)
    }

    // 🚀 Deploy success — spring pop
    const successEl = outLines[5]
    if (successEl) {
      tl.set(successEl, { scale: 0.8 }, 0)
      tl.to(successEl, { scale: 1, opacity: 1, duration: 0.4, ease: 'back.out(1.7)' }, 2.75)
      tl.to(successEl, { textShadow: '0 0 18px rgba(245,158,11,0.65)', duration: 0.25 }, 2.75)
    }

    // URL line — appears with underline draw
    const urlLineEl = outLines[6]
    if (urlLineEl) {
      tl.to(urlLineEl, { opacity: 1, duration: 0.15 }, 3.25)
      if (urlUnder) tl.to(urlUnder, { width: '100%', duration: 0.22 }, 3.25)
    }

    // ── Mouse cursor: glides to URL and clicks ────────────────────────────────
    if (cursor) {
      tl.set(cursor, { x: 580, y: 300, opacity: 0, scale: 1 }, 0)
      tl.to(cursor, { opacity: 1, duration: 0.18 }, 3.6)

      // Compute URL position at runtime (URL element is in the live DOM at this point)
      tl.call(() => {
        const urlEl   = urlOutputRef.current
        const contEl  = containerRef.current
        if (!urlEl || !contEl || !cursor) return
        const urlRect  = urlEl.getBoundingClientRect()
        const contRect = contEl.getBoundingClientRect()
        // Aim for the first quarter of the URL text, vertically centred on the line
        const tx = urlRect.left - contRect.left + 18
        const ty = urlRect.top  - contRect.top  + urlRect.height / 2 - 6
        gsap.to(cursor, { x: tx, y: ty, duration: 0.75, ease: 'power2.inOut' })
      }, [], 3.7)

      // Click — runs after the 0.75s move completes (3.7 + 0.75 = 4.45)
      tl.to(cursor, { scale: 0.78, duration: 0.06 }, 4.47)
      tl.to(cursor, { scale: 1.00, duration: 0.08 }, 4.53)
      tl.to(cursor, { opacity: 0, duration: 0.22 }, 4.85)
    }

    // ── Browser window pops out from the link position ────────────────────────────────────────────
    if (browser) {
      // Init: use the URL element's position as the transformOrigin, tiny scale
      tl.set(browser, { opacity: 0, scale: 0.08 }, 0)
      // Set transformOrigin dynamically (center of the URL link)
      tl.call(() => {
        const urlEl  = urlOutputRef.current
        const contEl = containerRef.current
        if (!urlEl || !contEl) return
        const urlRect  = urlEl.getBoundingClientRect()
        const contRect = contEl.getBoundingClientRect()
        const ox = ((urlRect.left - contRect.left + urlRect.width  / 2) / contRect.width  * 100).toFixed(1) + '%'
        const oy = ((urlRect.top  - contRect.top  + urlRect.height / 2) / contRect.height * 100).toFixed(1) + '%'
        gsap.set(browser, { transformOrigin: `${ox} ${oy}` })
      }, [], 4.55)
      // Pop open — back.out for a springy feel
      tl.to(browser, { opacity: 1, scale: 1, duration: 0.55, ease: 'back.out(1.5)' }, 4.60)
    }

    return tl
  }

  function buildScene08() {
    const tl      = gsap.timeline()
    const browser = browserWindowRef.current
    const p1 = phrase1Ref.current, p2 = phrase2Ref.current, p3 = phrase3Ref.current
    const s1 = sep1Ref.current,    s2 = sep2Ref.current
    const logo = logoRef08.current,  cta = ctaRef08.current

    // Reset phrase/logo/cta — browser window itself is already open from scene06
    ;[p1, p2, p3].forEach(el => { if (el) tl.set(el, { opacity: 0, y: 8 }, 0) })
    ;[s1, s2].forEach(el => { if (el) tl.set(el, { opacity: 0 }, 0) })
    if (logo) tl.set(logo, { opacity: 0, scale: 0 }, 0)
    if (cta)  tl.set(cta,  { opacity: 0, y: 8, boxShadow: '0 0 0 0 rgba(245,158,11,0)' }, 0)

    // browser is a direct child of containerRef — no stageRef opacity issues
    if (!browser) return tl

    // Phrases: stagger 0.5s
    if (p1) tl.to(p1, { opacity: 1, y: 0, duration: 0.4, ease: 'power2.out' }, 0.3)
    if (s1) tl.to(s1, { opacity: 1, duration: 0.2 }, 0.6)
    if (p2) tl.to(p2, { opacity: 1, y: 0, duration: 0.4, ease: 'power2.out' }, 0.8)
    if (s2) tl.to(s2, { opacity: 1, duration: 0.2 }, 1.1)
    if (p3) tl.to(p3, { opacity: 1, y: 0, duration: 0.4, ease: 'power2.out' }, 1.3)

    // LaunchKit logo
    if (logo) tl.to(logo, { scale: 1, opacity: 1, duration: 0.32, ease: 'back.out(1.7)' }, 1.85)

    // CTA button
    if (cta) {
      tl.to(cta, { opacity: 1, y: 0, duration: 0.3, ease: 'power2.out' }, 2.2)
      // Two attention pulses
      tl.to(cta, { boxShadow: '0 0 0 5px rgba(245,158,11,0.3)', duration: 0.2 }, 2.55)
      tl.to(cta, { boxShadow: '0 0 0 14px rgba(245,158,11,0)', duration: 0.25 }, 2.75)
      tl.to(cta, { boxShadow: '0 0 0 5px rgba(245,158,11,0.3)', duration: 0.2 }, 3.1)
      tl.to(cta, { boxShadow: '0 0 0 14px rgba(245,158,11,0)', duration: 0.25 }, 3.3)
    }

    return tl
  }

  // ── Render ────────────────────────────────────────────────────────────────
  return (
    <div
      ref={containerRef}
      className="relative w-full overflow-hidden rounded-xl shadow-2xl shadow-black/70 text-left"
      style={{ background: '#1E1E1E', height: 500, border: '1px solid #3C3C3C' }}
    >
      <div ref={vsCodeRef} className="flex flex-col h-full" style={{ transformOrigin: '50% 52%' }}>

        {/* ── Title bar ── */}
        <div className="flex items-center shrink-0" style={{ height: 28, background: '#3C3C3C' }}>
          <div className="flex items-center gap-1.5" style={{ paddingLeft: 14 }}>
            <div style={{ width: 12, height: 12, borderRadius: '50%', background: '#FF5F57' }} />
            <div style={{ width: 12, height: 12, borderRadius: '50%', background: '#FEBC2E' }} />
            <div style={{ width: 12, height: 12, borderRadius: '50%', background: '#28C840' }} />
          </div>
          <span style={{ color: '#CCCCCC', fontSize: 12, margin: '0 auto' }}>
            my-startup — app.py
          </span>
        </div>

        {/* ── Main: sidebar + editor ── */}
        <div className="flex flex-1 min-h-0">

          {/* Sidebar */}
          <div className="shrink-0 overflow-hidden" style={{ width: 200, background: '#252526', borderRight: '1px solid #1E1E1E' }}>
            {/* Sidebar header */}
            <div style={{ padding: '8px 12px 4px', fontSize: 10, color: '#BBBBBB', letterSpacing: '0.1em', fontFamily: 'system-ui, sans-serif' }}>
              EXPLORER
            </div>
            {/* File tree */}
            <div style={{ fontFamily: 'system-ui, -apple-system, sans-serif', fontSize: 12.5 }}>
              {TREE.map((node, i) => (
                <div
                  key={i}
                  style={{
                    display: 'flex',
                    alignItems: 'center',
                    gap: 5,
                    paddingLeft: 8 + node.indent * 12,
                    paddingRight: 8,
                    height: 22,
                    background: node.active ? 'rgba(255,255,255,0.10)' : 'transparent',
                    color: node.active ? '#FFFFFF' : node.color,
                    cursor: 'default',
                    userSelect: 'none',
                  }}
                >
                  {node.chevron && (
                    <span style={{ fontSize: 9, color: '#888', width: 10, flexShrink: 0 }}>{node.chevron}</span>
                  )}
                  <TreeIcon type={node.fileIcon} />
                  <span>{node.label}</span>
                </div>
              ))}
            </div>
          </div>

          {/* Editor */}
          <div className="flex-1 overflow-hidden flex flex-col" style={{ background: '#1E1E1E' }}>
            {/* Tab bar */}
            <div style={{ height: 35, background: '#252526', borderBottom: '1px solid #1E1E1E', display: 'flex', alignItems: 'stretch', flexShrink: 0 }}>
              <div style={{
                display: 'flex', alignItems: 'center', gap: 6, padding: '0 16px',
                background: '#1E1E1E', borderTop: '1px solid #6366F1',
                color: '#CCCCCC', fontSize: 12.5, fontFamily: 'system-ui, sans-serif',
              }}>
                <PythonIcon size={13} />
                <span>app.py</span>
                <span style={{ color: '#666', fontSize: 16, lineHeight: 1, marginLeft: 4 }}>×</span>
              </div>
            </div>

            {/* Breadcrumb */}
            <div style={{ height: 22, background: '#1E1E1E', borderBottom: '1px solid #252526', padding: '0 12px', display: 'flex', alignItems: 'center', flexShrink: 0 }}>
              <span style={{ fontSize: 11, color: '#888', fontFamily: 'system-ui, sans-serif' }}>backend &gt; app.py</span>
            </div>

            {/* Code — outer clips, inner scrolls via GSAP translateY */}
            <div className="flex-1 overflow-hidden" style={{ position: 'relative' }}>
              <div
                ref={codeInnerRef}
                style={{
                  padding: '8px 0',
                  fontFamily: '"Fira Code", "Cascadia Code", Menlo, "Courier New", monospace',
                  fontSize: 13,
                  lineHeight: '21px',
                  whiteSpace: 'pre',
                  willChange: 'transform',
                }}
              >
              {LINES.map((segs, i) => (
                <div
                  key={i}
                  ref={el => { lineRefs.current[i] = el }}
                  style={{
                    display: 'flex',
                    alignItems: 'center',
                    paddingLeft: 0,
                    paddingRight: 16,
                    borderLeft: '2px solid transparent',
                  }}
                >
                  {/* Line number */}
                  <span style={{
                    color: '#4E4E4E',
                    minWidth: 40,
                    textAlign: 'right',
                    paddingRight: 16,
                    paddingLeft: 8,
                    flexShrink: 0,
                    fontSize: 12,
                    userSelect: 'none',
                  }}>
                    {i + 1}
                  </span>
                  {/* Segments */}
                  <span>
                    {segs.map(([text, color], j) => (
                      <span key={j} style={{ color }}>{text}</span>
                    ))}
                  </span>
                </div>
              ))}
              </div>{/* end codeInnerRef */}
            </div>{/* end clip wrapper */}
          </div>
        </div>

        {/* ── Claude Code Terminal panel ── */}
        <div
          ref={termPanelRef}
          className="shrink-0"
          style={{ borderTop: '1px solid #3C3C3C' }}
        >
          {/* VS Code panel tab bar */}
          <div style={{
            display: 'flex', alignItems: 'center',
            background: '#252526', height: 30,
            borderBottom: '1px solid #1E1E1E',
            fontFamily: 'system-ui, sans-serif', fontSize: 11.5,
          }}>
            {['Problems', 'Output', 'Debug Console', 'Terminal', 'Ports'].map(tab => (
              <div key={tab} style={{
                padding: '0 14px', height: '100%',
                display: 'flex', alignItems: 'center',
                color: tab === 'Terminal' ? '#CCCCCC' : '#666',
                borderBottom: tab === 'Terminal' ? '1px solid #6366F1' : '1px solid transparent',
                cursor: 'default', userSelect: 'none',
              }}>
                {tab}
              </div>
            ))}
          </div>

          {/* Claude Code terminal body */}
          <div style={{ background: '#000', padding: '10px 14px 0' }}>

            {/* Claude Code header: ASCII art + info inline */}
            <ClaudeCodeHeader />

            {/* Separator */}
            <div style={{ borderTop: '1px solid #222', margin: '6px 0' }} />

            {/* Input prompt — typing happens here */}
            <div style={{
              fontFamily: '"Fira Code", Menlo, monospace',
              fontSize: 14, display: 'flex', alignItems: 'center', gap: 8,
              padding: '2px 0 4px',
            }}>
              <span style={{ color: '#FFFFFF' }}>❯</span>
              <span ref={inputTextRef} style={{ color: '#CCCCCC' }} />
              <span
                ref={cursorBlinkRef}
                className="cursor-blink"
                style={{
                  display: 'inline-block', width: 8, height: 16,
                  background: '#FFFFFF', verticalAlign: 'middle',
                }}
              />
            </div>

            {/* After-Enter block: shown after user hits Enter in scene01.
                Contains: thinking indicator (fades in scene01, fades out scene06)
                          deploy output (appears in scene06)
                          separator + empty prompt (always visible once shown) */}
            <div ref={thinkingRowRef} style={{ display: 'none', opacity: 0 }}>

              {/* ── Thinking indicator — fades out in scene 06 ── */}
              <div
                ref={thinkingIndicatorRef}
                style={{ fontFamily: '"Fira Code", Menlo, monospace', fontSize: 13, padding: '2px 0 2px' }}
              >
                <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
                  <span ref={thinkingSymRef} style={{ display: 'inline-block', color: '#C87257' }}>✢</span>
                  <span style={{ color: '#888' }}>Wibbling<span style={{ color: '#555' }}>… (thinking with high effort)</span></span>
                </div>
                <div style={{ color: '#444', fontSize: 11.5, marginTop: 2, paddingLeft: 22 }}>
                  ⎿  Tip: Use /theme to change the color theme
                </div>
              </div>

              {/* ── Deploy output — hidden until scene 06 ── */}
              <div
                ref={deployOutputRef}
                style={{ display: 'none', fontFamily: '"Fira Code", Menlo, monospace', fontSize: 12.5 }}
              >
                {['Neon DB provisioned', 'Cloud Run deployed', 'Cloudflare DNS configured', 'Email routing ready', '6 secrets injected'].map((text, i) => (
                  <div
                    key={i}
                    ref={el => { deployOutLineRefs.current[i] = el }}
                    style={{ opacity: 0, marginBottom: 5, display: 'flex', alignItems: 'center', gap: 9 }}
                  >
                    <span style={{ color: '#10B981', fontWeight: 700, flexShrink: 0 }}>✓</span>
                    <span style={{ color: '#9CA3AF' }}>{text}</span>
                  </div>
                ))}
                <div
                  ref={el => { deployOutLineRefs.current[5] = el }}
                  style={{ opacity: 0, marginTop: 10, marginBottom: 6, fontSize: 13.5, fontFamily: 'system-ui, sans-serif', fontWeight: 600, color: '#F9FAFB' }}
                >
                  🚀 Deploy complete
                </div>
                <div
                  ref={el => { deployOutLineRefs.current[6] = el }}
                  style={{ opacity: 0, display: 'flex', alignItems: 'center', gap: 7, marginBottom: 20 }}
                >
                  <span style={{ color: '#F59E0B', flexShrink: 0 }}>→</span>
                  <span ref={urlOutputRef} style={{ position: 'relative', color: '#F59E0B' }}>
                    https://myapp-abc123.launchkit.app
                    <span ref={urlUnderlineRef} style={{ position: 'absolute', bottom: 0, left: 0, height: 1, background: '#F59E0B', width: 0 }} />
                  </span>
                </div>
              </div>

              {/* ── Separator ── */}
              <div style={{ borderTop: '1px solid #333', margin: '6px 0' }} />

              {/* ── New empty prompt — always empty, one line ── */}
              <div style={{ fontFamily: '"Fira Code", Menlo, monospace', fontSize: 14, display: 'flex', alignItems: 'center', gap: 8, padding: '2px 0 4px' }}>
                <span style={{ color: '#FFFFFF' }}>❯</span>
                <span className="cursor-blink" style={{ display: 'inline-block', width: 8, height: 16, background: '#FFFFFF', verticalAlign: 'middle' }} />
              </div>

            </div>

            {/* Bottom separator */}
            <div style={{ borderTop: '1px solid #222' }} />

            {/* Status bar */}
            <div style={{
              fontFamily: '"Fira Code", Menlo, monospace',
              fontSize: 12, padding: '5px 0',
              color: '#C87257',
            }}>
              <span style={{ opacity: 0.6 }}>{'  ⏵⏵ '}</span>
              <span>bypass permissions </span>
              <span style={{ fontWeight: 600 }}>on</span>
              <span style={{ opacity: 0.6 }}> (shift+tab to cycle) · esc to interrupt</span>
            </div>

          </div>
        </div>

      </div>

      {/* ── Scene 02+ Stage ── */}
      <div
        ref={stageRef}
        style={{
          position: 'absolute', inset: 0,
          background: '#000', opacity: 0,
          pointerEvents: 'none',
        }}
      >
        {/* 3D Server box */}
        <div
          ref={serverBoxRef}
          style={{
            position: 'absolute',
            left: '50%', top: '50%',
            transform: 'translate(-50%, -50%)',
            opacity: 0,
            filter: 'drop-shadow(0 0 18px rgba(245,158,11,0.25))',
          }}
        >
          <svg viewBox="0 0 260 110" width="260" height="110" overflow="visible">
            {/* ── Top face ── */}
            <polygon points="40,0 240,0 200,22 0,22"
              fill="rgba(245,158,11,0.025)" stroke="rgba(245,158,11,0.45)" strokeWidth="1" />
            {/* Top face depth lines */}
            <line x1="80" y1="11" x2="40" y2="22"  stroke="rgba(245,158,11,0.12)" strokeWidth="0.5" strokeDasharray="2 4" />
            <line x1="160" y1="5.5" x2="120" y2="16.5" stroke="rgba(245,158,11,0.12)" strokeWidth="0.5" strokeDasharray="2 4" />

            {/* ── Right face ── */}
            <polygon points="200,22 240,0 240,65 200,87"
              fill="rgba(0,0,0,0.5)" stroke="rgba(245,158,11,0.35)" strokeWidth="1" />
            {/* Right face ribs */}
            <line x1="220" y1="11" x2="220" y2="76" stroke="rgba(245,158,11,0.08)" strokeWidth="0.5" />

            {/* ── Front face ── */}
            <polygon points="0,22 200,22 200,87 0,87"
              fill="rgba(12,10,8,0.97)" stroke="rgba(245,158,11,0.7)" strokeWidth="1" />

            {/* Front: inner bezel */}
            <polygon points="4,26 196,26 196,83 4,83"
              fill="none" stroke="rgba(245,158,11,0.12)" strokeWidth="0.5" />

            {/* ── Control panel (left ~60px) ── */}
            {/* Power button */}
            <circle cx="13" cy="54" r="6.5"
              fill="rgba(20,18,14,1)" stroke="rgba(245,158,11,0.5)" strokeWidth="1" />
            <circle cx="13" cy="54" r="2.5" fill="rgba(245,158,11,0.4)" />

            {/* LED: power (amber, slow pulse) */}
            <circle cx="29" cy="48" r="3.2" fill="#F59E0B"
              style={{ animation: 'led-power 2.4s ease-in-out infinite' }} />
            {/* LED: disk activity (green, fast blink) */}
            <circle cx="29" cy="59" r="3.2" fill="#22C55E"
              style={{ animation: 'led-disk 0.28s step-end infinite', animationDelay: '0.3s' }} />
            {/* LED: network (blue, medium pulse) */}
            <circle cx="40" cy="48" r="3.2" fill="#3B82F6"
              style={{ animation: 'led-net 1.1s ease-in-out infinite', animationDelay: '0.6s' }} />
            {/* LED: status (amber/dim — standby) */}
            <circle cx="40" cy="59" r="3.2" fill="rgba(245,158,11,0.25)" />

            {/* USB port */}
            <rect x="51" y="37" width="9" height="7" rx="1"
              fill="rgba(5,5,5,1)" stroke="rgba(245,158,11,0.3)" strokeWidth="0.6" />
            {/* Mini jack */}
            <rect x="51" y="56" width="5" height="8" rx="2"
              fill="rgba(5,5,5,1)" stroke="rgba(245,158,11,0.25)" strokeWidth="0.6" />

            {/* ── Drive bays (4×) ── */}
            {[63, 93, 123, 153].map((x, i) => (
              <g key={i}>
                <rect x={x} y="28" width="27" height="31" rx="1.5"
                  fill="rgba(6,5,4,1)" stroke="rgba(245,158,11,0.3)" strokeWidth="0.7" />
                {/* Bay activity LED */}
                <circle cx={x + 4} cy="32" r="1.8"
                  fill={i === 1 ? '#22C55E' : 'rgba(245,158,11,0.2)'}
                  style={i === 1 ? { animation: 'led-disk 0.35s step-end infinite', animationDelay: `${i * 0.12}s` } : undefined}
                />
                {/* Bay horizontal slot lines */}
                {[39, 44, 49, 54].map(y => (
                  <line key={y} x1={x + 3} y1={y} x2={x + 24} y2={y}
                    stroke="rgba(245,158,11,0.08)" strokeWidth="0.5" />
                ))}
              </g>
            ))}

            {/* ── Ventilation (right strip) ── */}
            {[30, 35, 40, 45, 50, 55, 60, 65, 70, 75].map(y => (
              <line key={y} x1="185" y1={y} x2="195" y2={y}
                stroke="rgba(245,158,11,0.18)" strokeWidth="1" strokeLinecap="round" />
            ))}

            {/* ── Status LED strip (6×, Scene 04 controlled) ── */}
            {[63, 87, 111, 135, 159, 183].map((cx, i) => (
              <circle
                key={i}
                ref={el => { ledRefs.current[i] = el }}
                cx={cx} cy={78} r="3.5"
                style={{ fill: '#1a1a18' }}
              />
            ))}

            {/* ── Model label ── */}
            <text x="100" y="86" textAnchor="middle"
              fill="rgba(245,158,11,0.15)" fontSize="5"
              fontFamily="'Fira Code', monospace" letterSpacing="0.1em">
              LAUNCHKIT-SRV-01
            </text>
          </svg>
        </div>

        {/* Provider logos */}
        {LOGOS.map((logo, i) => (
          <div
            key={logo.id}
            ref={el => { logoRefs.current[i] = el }}
            style={{
              position: 'absolute', top: 0, left: 0,
              width: logo.size, height: logo.size,
              opacity: 0,
            }}
          >
            <svg viewBox="0 0 24 24" width={logo.size} height={logo.size}>
              <path d={logo.path} fill={logo.color} />
            </svg>
          </div>
        ))}

        {/* ── Scene 05: Service grid ── */}
        {/* gridRef is just a 0-overhead positioning context over the stage */}
        <div
          ref={gridRef}
          style={{ position: 'absolute', inset: 0, pointerEvents: 'none' }}
        >
          {GRID_CARDS.map((card, i) => (
            <div
              key={card.id}
              ref={el => { gridCardRefs.current[i] = el }}
              style={{
                position: 'absolute', top: 0, left: 0,
                opacity: 0,
                borderRadius: 10,
                background: 'rgba(20, 22, 32, 0.96)',
                border: '1px solid rgba(255,255,255,0.08)',
                boxShadow: `0 4px 24px rgba(0,0,0,0.65), inset 0 0 0 1px ${card.color}18`,
                padding: '14px 14px 12px',
                display: 'flex',
                flexDirection: 'column',
                justifyContent: 'space-between',
              }}
            >
              {/* Top: icon badge + name */}
              <div style={{ display: 'flex', alignItems: 'flex-start', gap: 10 }}>
                <div style={{
                  width: 38, height: 38, borderRadius: 9,
                  background: `${card.color}1A`,
                  border: `1px solid ${card.color}44`,
                  display: 'flex', alignItems: 'center', justifyContent: 'center',
                  flexShrink: 0,
                }}>
                  <svg viewBox="0 0 24 24" width={20} height={20}>
                    <path d={card.iconPath} fill={card.color} />
                  </svg>
                </div>
                <div style={{ paddingTop: 1 }}>
                  <div style={{ fontSize: 13, fontWeight: 700, color: '#F9FAFB', fontFamily: 'system-ui, -apple-system, sans-serif', lineHeight: 1.25 }}>{card.label}</div>
                  <div style={{ fontSize: 9.5, color: '#6B7280', marginTop: 3, fontFamily: '"Fira Code", monospace' }}>{card.sub}</div>
                </div>
              </div>
              {/* Bottom: Online status */}
              <div style={{ display: 'flex', alignItems: 'center', gap: 5 }}>
                <svg viewBox="0 0 24 24" width={14} height={14} style={{ flexShrink: 0 }}>
                  <path d="M12 2C6.48 2 2 6.48 2 12s4.48 10 10 10 10-4.48 10-10S17.52 2 12 2zm-2 15l-5-5 1.41-1.41L10 14.17l7.59-7.59L19 8l-9 9z" fill="#10B981" />
                </svg>
                <span style={{ fontSize: 11, color: '#10B981', fontFamily: 'system-ui, sans-serif' }}>Online</span>
              </div>
            </div>
          ))}
        </div>


      </div>

      {/* ── Scene 08: Browser window (opens from URL click in scene06) ── */}
      <div
        ref={browserWindowRef}
        style={{
          position: 'absolute', inset: 0,
          opacity: 0,
          borderRadius: 10,
          overflow: 'hidden',
          background: '#1c1c1e',
          border: '1px solid #3C3C3C',
          pointerEvents: 'none',
          display: 'flex', flexDirection: 'column',
        }}
      >
        {/* Browser chrome — title bar */}
        <div style={{
          height: 44, background: '#2c2c2e', flexShrink: 0,
          display: 'flex', alignItems: 'center',
          gap: 10, padding: '0 14px',
          borderBottom: '1px solid #3a3a3a',
        }}>
          {/* Traffic lights */}
          <div style={{ display: 'flex', gap: 6, flexShrink: 0 }}>
            <div style={{ width: 12, height: 12, borderRadius: '50%', background: '#FF5F57' }} />
            <div style={{ width: 12, height: 12, borderRadius: '50%', background: '#FEBC2E' }} />
            <div style={{ width: 12, height: 12, borderRadius: '50%', background: '#28C840' }} />
          </div>
          {/* URL bar */}
          <div style={{
            flex: 1, maxWidth: 360, margin: '0 auto',
            background: '#3a3a3c', borderRadius: 6,
            padding: '5px 14px', fontSize: 11.5,
            color: '#F59E0B',
            fontFamily: '"Fira Code", "Consolas", monospace',
            textAlign: 'center', letterSpacing: '0.01em',
            whiteSpace: 'nowrap', overflow: 'hidden', textOverflow: 'ellipsis',
          }}>
            https://myapp-abc123.launchkit.app
          </div>
        </div>

        {/* Browser viewport — Scene 08 content */}
        <div style={{
          flex: 1, background: '#000',
          display: 'flex', alignItems: 'center', justifyContent: 'center',
          flexDirection: 'column', gap: 26,
        }}>
          {/* Tagline */}
          <div style={{
            display: 'flex', alignItems: 'center', gap: 14,
            fontFamily: 'var(--font-ibm-plex-serif), Georgia, serif',
            fontStyle: 'italic', fontWeight: 300,
            fontSize: 26, color: '#F59E0B',
          }}>
            <span ref={phrase1Ref} style={{ opacity: 0, display: 'inline-block' }}>1 dashboard</span>
            <span ref={sep1Ref} style={{ opacity: 0, color: 'rgba(245,158,11,0.4)' }}>·</span>
            <span ref={phrase2Ref} style={{ opacity: 0, display: 'inline-block' }}>1 credit card</span>
            <span ref={sep2Ref} style={{ opacity: 0, color: 'rgba(245,158,11,0.4)' }}>·</span>
            <span ref={phrase3Ref} style={{ opacity: 0, display: 'inline-block' }}>1 API key</span>
          </div>

          {/* LaunchKit logo */}
          <div ref={logoRef08} style={{ opacity: 0, display: 'flex', alignItems: 'center', gap: 9 }}>
            <div style={{
              width: 26, height: 26, borderRadius: 7,
              background: '#F59E0B',
              display: 'flex', alignItems: 'center', justifyContent: 'center',
            }}>
              <svg viewBox="0 0 24 24" width={14} height={14} fill="none"
                stroke="#1a1108" strokeWidth={2.8} strokeLinecap="round" strokeLinejoin="round">
                <path d="M13 2L3 14h9l-1 8 10-12h-9l1-8z" />
              </svg>
            </div>
            <span style={{
              fontSize: 20, fontWeight: 500, color: '#F9FAFB',
              fontFamily: 'var(--font-ibm-plex-serif), Georgia, serif',
              fontStyle: 'italic',
            }}>LaunchKit</span>
          </div>

          {/* CTA */}
          <div
            ref={ctaRef08}
            style={{
              opacity: 0,
              display: 'inline-flex', alignItems: 'center', gap: 8,
              background: '#F59E0B', color: '#1a1108',
              padding: '11px 28px', borderRadius: 8,
              fontSize: 14.5, fontWeight: 600,
              fontFamily: 'system-ui, -apple-system, sans-serif',
              cursor: 'pointer', userSelect: 'none',
            }}
          >
            Deploy now
            <svg viewBox="0 0 24 24" width={15} height={15} fill="none"
              stroke="currentColor" strokeWidth={2.5} strokeLinecap="round" strokeLinejoin="round">
              <path d="M5 12h14M12 5l7 7-7 7" />
            </svg>
          </div>
        </div>
      </div>

      {/* ── White flash overlay ── */}
      <div
        ref={flashOverlayRef}
        style={{
          position: 'absolute', inset: 0,
          background: '#fff', opacity: 0,
          pointerEvents: 'none',
        }}
      />

      {/* ── Scene 06: Mouse cursor ── */}
      <div
        ref={mouseCursorRef}
        style={{
          position: 'absolute', top: 0, left: 0,
          opacity: 0, pointerEvents: 'none', zIndex: 60,
        }}
      >
        <svg width="18" height="22" viewBox="0 0 18 22" fill="none">
          <path
            d="M1 1L1 17L5.2 12.6L8.2 19.5L10.1 18.7L7.1 11.8L14 11.8Z"
            fill="white" stroke="#333" strokeWidth="1.2" strokeLinejoin="round"
          />
        </svg>
      </div>

    </div>
  )
}
