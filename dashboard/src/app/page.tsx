"use client";

import { useState, useEffect, useRef } from "react";
import Link from "next/link";
import {
  ArrowRight, Zap, CheckCircle, FolderOpen, Rocket, Globe,
  Cloud, Database, Shield, Layers,
} from "lucide-react";
import { Button } from "@/components/ui/button";
import HeroAnimationSequence from "@/components/HeroAnimationSequence";

export default function LandingPage() {
  return (
    <div className="flex flex-col min-h-screen bg-background text-foreground overflow-x-hidden">
      {/* Ambient blobs */}
      <div className="pointer-events-none fixed inset-0 overflow-hidden">
        <div className="animate-blob absolute -top-40 left-1/4 h-96 w-96 rounded-full bg-primary/8 blur-3xl" />
        <div className="animate-blob absolute top-1/3 right-1/4 h-80 w-80 rounded-full bg-primary/5 blur-3xl [animation-delay:10s]" />
      </div>

      {/* Nav */}
      <nav className="fixed top-0 inset-x-0 z-50 backdrop-blur-sm bg-background/60">
        <div className="max-w-6xl mx-auto h-14 flex items-center justify-between px-6">
          <div className="flex items-center gap-2">
            <div className="h-5 w-5 rounded-md bg-primary flex items-center justify-center">
              <Zap className="h-3 w-3 text-primary-foreground" />
            </div>
            <span className="text-base font-medium tracking-tight" style={{ fontFamily: "var(--font-ibm-plex-serif)", fontStyle: "italic" }}>
              LaunchKit
            </span>
          </div>
          <div className="flex items-center gap-3">
            <Link href="https://docs.launchkit.dev" className="text-sm text-muted-foreground hover:text-foreground transition-colors min-h-[44px] flex items-center px-2">
              Pricing
            </Link>
            <Link href="/login">
              <Button size="sm" className="cursor-pointer rounded-lg gap-1.5 font-medium min-h-[40px] px-4 bg-primary text-primary-foreground hover:opacity-90">
                Get Started <ArrowRight className="h-3.5 w-3.5" />
              </Button>
            </Link>
          </div>
        </div>
      </nav>

      {/* Section 1: Hero */}
      <section className="relative pt-32 pb-0 px-6 flex flex-col items-center text-center overflow-hidden">
        <div className="absolute inset-0 grid-bg grid-bg-fade pointer-events-none" />

        <div className="relative z-10 flex flex-col items-center gap-6 max-w-3xl mx-auto fade-up fade-up-delay-1">
          {/* Badge */}
          <div className="inline-flex items-center gap-2 rounded-full border border-primary/30 bg-primary/10 px-4 py-1.5 text-sm font-medium text-primary">
            <span className="relative flex h-2 w-2">
              <span className="animate-ping absolute inline-flex h-full w-full rounded-full bg-primary/75" />
              <span className="relative inline-flex rounded-full h-2 w-2 bg-primary" />
            </span>
            Cloud Run · Neon · Upstash · Cloudflare — the real thing
          </div>

          {/* H1 */}
          <h1 className="text-5xl md:text-7xl font-light tracking-[-0.03em] leading-[1.08]" style={{ fontFamily: "var(--font-ibm-plex-serif)" }}>
            The real cloud.
            <br />
            <span className="italic">One login.</span>
          </h1>

          {/* Subtext */}
          <p className="text-lg text-muted-foreground max-w-xl leading-relaxed">
            Not another platform with its own MinIO and fake Postgres. Your app runs on the infrastructure you&apos;d pick anyway — wired up, billed in one place, ready in minutes.
          </p>

          {/* CTAs */}
          <div className="flex flex-wrap items-center justify-center gap-3 mt-2">
            <Link href="/login">
              <Button size="lg" className="cursor-pointer rounded-lg gap-2 font-semibold px-6 min-h-[48px] bg-primary text-primary-foreground hover:opacity-90">
                Deploy now <ArrowRight className="h-4 w-4" />
              </Button>
            </Link>
            <a href="#how-it-works">
              <Button size="lg" variant="outline" className="cursor-pointer rounded-lg text-muted-foreground hover:text-foreground min-h-[48px] border-border/50">
                See how it works
              </Button>
            </a>
          </div>
        </div>

        {/* Hero animation */}
        <div className="relative z-10 mt-16 w-full max-w-4xl mx-auto fade-up fade-up-delay-2">
          <HeroAnimationSequence />
        </div>
      </section>

      {/* Section 2: The two bad choices + our answer */}
      <section className="py-32 px-6">
        <div className="max-w-5xl mx-auto">
          <div className="text-center mb-16 fade-up">
            <div className="inline-flex items-center rounded-full border border-primary/20 bg-primary/5 px-3 py-1 text-xs font-medium text-primary mb-6">
              Why we exist
            </div>
            <h2 className="text-3xl md:text-5xl font-light tracking-[-0.02em]" style={{ fontFamily: "var(--font-ibm-plex-serif)" }}>
              You shouldn&apos;t have to choose<br />
              <span className="italic">between simple and real.</span>
            </h2>
          </div>

          <div className="grid md:grid-cols-2 gap-6 mb-12">
            {/* Pain 1 */}
            <div className="fade-up rounded-xl border border-border/50 bg-card/60 p-8">
              <div className="h-10 w-10 rounded-lg bg-destructive/10 text-destructive flex items-center justify-center mb-5">
                <Layers className="h-5 w-5" />
              </div>
              <h3 className="text-lg font-medium mb-3" style={{ fontFamily: "var(--font-ibm-plex-serif)" }}>
                Platform lock-in
              </h3>
              <p className="text-sm text-muted-foreground leading-relaxed mb-4">
                Vercel, Bolt, Railway — they&apos;re convenient. They&apos;re also quietly replacing the infrastructure you&apos;d actually choose. MinIO instead of S3. Internal databases instead of Neon. Proprietary runtimes you can&apos;t take with you.
              </p>
              <p className="text-xs text-muted-foreground/60 font-mono">
                → Convenient today. Migration nightmare tomorrow.
              </p>
            </div>

            {/* Pain 2 */}
            <div className="fade-up rounded-xl border border-border/50 bg-card/60 p-8" style={{ animationDelay: "0.1s" }}>
              <div className="h-10 w-10 rounded-lg bg-destructive/10 text-destructive flex items-center justify-center mb-5">
                <Shield className="h-5 w-5" />
              </div>
              <h3 className="text-lg font-medium mb-3" style={{ fontFamily: "var(--font-ibm-plex-serif)" }}>
                Account sprawl
              </h3>
              <p className="text-sm text-muted-foreground leading-relaxed mb-4">
                The DIY alternative: GCP for compute, AWS for storage, PlanetScale for the DB, Resend for email, Namecheap for the domain, Cloudflare for the CDN. Six dashboards. Six credit cards. Forty-seven API keys.
              </p>
              <p className="text-xs text-muted-foreground/60 font-mono">
                → Before you&apos;ve written a line of business logic.
              </p>
            </div>
          </div>

          {/* Answer */}
          <div className="fade-up rounded-xl border border-primary/30 bg-primary/5 p-8 text-center" style={{ animationDelay: "0.2s" }}>
            <p className="text-sm text-primary font-medium mb-3">LaunchKit</p>
            <p className="text-xl font-light tracking-tight max-w-2xl mx-auto" style={{ fontFamily: "var(--font-ibm-plex-serif)" }}>
              Your code runs on Cloud Run. Your data lives in Neon. Your files are on real S3.
              We just removed the part where you had to set all of that up yourself.
            </p>
          </div>
        </div>
      </section>

      {/* Section 3: How It Works */}
      <section id="how-it-works" className="py-20 px-6 border-t border-border/30">
        <div className="max-w-5xl mx-auto text-center">
          <div className="inline-flex items-center rounded-full border border-primary/20 bg-primary/5 px-3 py-1 text-xs font-medium text-primary mb-6">
            How it works
          </div>
          <h2 className="text-3xl md:text-5xl font-light tracking-[-0.02em] mb-16" style={{ fontFamily: "var(--font-ibm-plex-serif)" }}>
            From idea to live URL.<br />
            <span className="italic">No DevOps degree required.</span>
          </h2>
          <div className="grid grid-cols-1 md:grid-cols-3 gap-8">
            {[
              { Icon: FolderOpen, step: "1", title: "Connect your repo", desc: "Paste your API key into Claude Desktop. One-time setup. Claude becomes your deployment interface." },
              { Icon: Rocket, step: "2", title: "Tell Claude to ship", desc: 'Say "deploy my app". Claude reads your codebase, figures out what infrastructure it needs, and provisions it — automatically.' },
              { Icon: Globe, step: "3", title: "Get a real URL", desc: "Cloud Run handles scale. Neon holds your data. Cloudflare serves your frontend. All wired together, all billed in one place." },
            ].map((item, i) => (
              <div key={item.step} className="fade-up flex flex-col items-center text-center" style={{ animationDelay: `${0.1 + i * 0.15}s` }}>
                <div className="h-12 w-12 rounded-xl bg-primary/10 text-primary flex items-center justify-center mb-4">
                  <item.Icon className="h-5 w-5" />
                </div>
                <div className="text-xs font-mono text-primary/40 mb-2">Step {item.step}</div>
                <h3 className="text-lg font-medium mb-2">{item.title}</h3>
                <p className="text-sm text-muted-foreground leading-relaxed max-w-xs">{item.desc}</p>
              </div>
            ))}
          </div>
        </div>
      </section>

      {/* Section 3: Infrastructure marquee */}
      <section className="py-16 border-y border-border/30 overflow-hidden">
        <p className="text-center text-sm text-muted-foreground mb-8">The exact platforms you&apos;d choose yourself — not replacements, not wrappers</p>
        <MarqueeStrip />
      </section>

      {/* Section 4: Pricing */}
      <section className="py-32 px-6">
        <div className="max-w-4xl mx-auto text-center mb-14">
          <h2 className="text-3xl md:text-5xl font-light tracking-[-0.02em]" style={{ fontFamily: "var(--font-ibm-plex-serif)" }}>
            Pay for what you use.<br />
            <span className="italic">Own what you build.</span>
          </h2>
          <p className="mt-4 text-muted-foreground max-w-md mx-auto">
            Monthly credit covers your infra costs. Scale-to-zero means idle projects cost $0. No egress markups. No lock-in fees.
          </p>
        </div>
        <div className="max-w-3xl mx-auto grid sm:grid-cols-2 gap-6">
          {/* Starter */}
          <div className="rounded-xl border border-border/50 bg-card p-8">
            <p className="text-sm text-muted-foreground font-medium">Starter</p>
            <div className="mt-3 flex items-end gap-1">
              <span className="text-5xl font-light text-primary">$5</span>
              <span className="text-muted-foreground pb-1.5">/mo</span>
            </div>
            <ul className="mt-6 space-y-3">
              {["$5 usage credit/mo", "10 GB egress free", "100 min build/mo", "Scale-to-zero (idle costs $0)", "Cloud Run + Neon + Cloudflare"].map((f) => (
                <li key={f} className="flex items-center gap-3 text-sm text-muted-foreground">
                  <CheckCircle className="h-4 w-4 text-primary shrink-0" />
                  {f}
                </li>
              ))}
            </ul>
            <Link href="/login" className="block mt-8">
              <Button variant="outline" className="cursor-pointer w-full rounded-lg min-h-[44px] border-border/50">
                Get started free
              </Button>
            </Link>
          </div>

          {/* Pro */}
          <div className="rounded-xl border border-primary/40 ring-1 ring-primary/20 bg-card p-8 relative overflow-hidden">
            <div className="absolute top-3 right-3">
              <span className="text-[10px] font-bold uppercase tracking-wider px-2 py-0.5 rounded-full bg-primary/15 text-primary">Most Popular</span>
            </div>
            <p className="text-sm font-medium text-primary">Pro</p>
            <div className="mt-3 flex items-end gap-1">
              <span className="text-5xl font-light text-primary">$20</span>
              <span className="text-muted-foreground pb-1.5">/mo</span>
            </div>
            <ul className="mt-6 space-y-3">
              {["$20 usage credit/mo", "50 GB egress free", "500 min build/mo", "Custom domains", "GitHub push-to-deploy", "Priority support"].map((f) => (
                <li key={f} className="flex items-center gap-3 text-sm text-muted-foreground">
                  <CheckCircle className="h-4 w-4 text-primary shrink-0" />
                  {f}
                </li>
              ))}
            </ul>
            <Link href="/login" className="block mt-8">
              <Button className="cursor-pointer w-full rounded-lg font-medium min-h-[44px] bg-primary text-primary-foreground hover:opacity-90">
                Upgrade to Pro
              </Button>
            </Link>
          </div>
        </div>
      </section>

      {/* Section 5: CTA + Footer */}
      <section className="py-20 px-6">
        <div className="max-w-3xl mx-auto">
          <div className="bg-card border rounded-2xl p-12 text-center relative overflow-hidden">
            <div className="pointer-events-none absolute inset-0 overflow-hidden">
              <div className="animate-blob absolute -top-20 left-1/3 h-60 w-60 rounded-full bg-primary/6 blur-3xl" />
            </div>
            <div className="relative">
              <h2 className="text-3xl font-light mb-4" style={{ fontFamily: "var(--font-ibm-plex-serif)" }}>
                Your next idea deserves<br />
                <span className="italic">real infrastructure.</span>
              </h2>
              <p className="text-muted-foreground mb-8 max-w-md mx-auto">
                Not a walled garden. Not a MinIO replacement. The actual cloud — yours from day one, managed by us so you can focus on winning the market.
              </p>
              <Link href="/login">
                <Button size="lg" className="cursor-pointer rounded-lg gap-2 font-semibold px-8 min-h-[48px] bg-primary text-primary-foreground hover:opacity-90">
                  Get started free <ArrowRight className="h-4 w-4" />
                </Button>
              </Link>
            </div>
          </div>
        </div>
      </section>

      {/* Footer */}
      <footer className="border-t border-border/50 py-8 px-6">
        <div className="max-w-6xl mx-auto flex flex-col sm:flex-row items-center justify-between gap-4 text-sm text-muted-foreground">
          <div className="flex items-center gap-2">
            <div className="h-4 w-4 rounded bg-primary flex items-center justify-center">
              <Zap className="h-2.5 w-2.5 text-primary-foreground" />
            </div>
            <span className="font-medium text-foreground/80" style={{ fontFamily: "var(--font-ibm-plex-serif)", fontStyle: "italic" }}>LaunchKit</span>
          </div>
          <div className="flex items-center gap-4">
            <a href="https://docs.launchkit.dev" className="hover:text-foreground transition-colors min-h-[44px] flex items-center">Docs</a>
            <a href="#" className="hover:text-foreground transition-colors min-h-[44px] flex items-center">Privacy</a>
            <a href="mailto:help@launchkit.dev" className="hover:text-foreground transition-colors min-h-[44px] flex items-center">Contact</a>
          </div>
          <span>© {new Date().getFullYear()} LaunchKit</span>
        </div>
      </footer>
    </div>
  );
}

/* ── Product Demo Window (Railway-style tab switching) ── */
function ProductDemo() {
  const tabs = ["Connect", "Deploy", "Monitor", "Secrets", "Domains"] as const;
  type Tab = (typeof tabs)[number];
  const [activeTab, setActiveTab] = useState<Tab>("Connect");
  const [progress, setProgress] = useState(0);
  const intervalRef = useRef<ReturnType<typeof setInterval> | null>(null);

  useEffect(() => {
    setProgress(0);
    if (intervalRef.current) clearInterval(intervalRef.current);
    intervalRef.current = setInterval(() => {
      setProgress((p) => {
        if (p >= 100) {
          const idx = tabs.indexOf(activeTab);
          const next = tabs[(idx + 1) % tabs.length];
          setActiveTab(next);
          return 0;
        }
        return p + 2;
      });
    }, 80);
    return () => { if (intervalRef.current) clearInterval(intervalRef.current); };
  }, [activeTab]);

  return (
    <div className="relative z-10 mt-16 w-full max-w-4xl mx-auto fade-up fade-up-delay-2">
      <div className="rounded-xl border border-border/50 bg-card/80 backdrop-blur overflow-hidden shadow-2xl shadow-black/40">
        {/* Title bar */}
        <div className="flex items-center gap-2 px-4 py-3 border-b border-border/50 bg-card/50">
          <div className="flex gap-1.5">
            <div className="h-3 w-3 rounded-full bg-white/10" />
            <div className="h-3 w-3 rounded-full bg-white/10" />
            <div className="h-3 w-3 rounded-full bg-white/10" />
          </div>
          <span className="text-xs text-muted-foreground font-mono ml-2">launchkit — production</span>
        </div>

        {/* Tab bar */}
        <div className="flex items-center gap-1 px-4 pt-3 border-b border-border/30">
          {tabs.map((tab) => {
            const isActive = tab === activeTab;
            return (
              <button
                key={tab}
                onClick={() => { setActiveTab(tab); setProgress(0); }}
                className="relative overflow-hidden px-3 py-1.5 text-xs font-medium rounded-t-md transition-colors min-h-[32px]"
              >
                <span className={isActive ? "text-foreground" : "text-muted-foreground hover:text-foreground/70"}>
                  {tab}
                </span>
                {isActive && (
                  <span
                    className="absolute inset-x-0 bottom-0 h-0.5 bg-primary transition-all duration-100"
                    style={{ width: `${progress}%` }}
                  />
                )}
              </button>
            );
          })}
        </div>

        {/* Tab content */}
        <div className="h-64 p-5">
          {activeTab === "Connect" && <ConnectTab />}
          {activeTab === "Deploy" && <DeployTab />}
          {activeTab === "Monitor" && <MonitorTab />}
          {activeTab === "Secrets" && <SecretsTab />}
          {activeTab === "Domains" && <DomainsTab />}
        </div>
      </div>
    </div>
  );
}

/* Connect tab: repo cards */
function ConnectTab() {
  const repos = [
    { name: "postgres-api", org: "launchkit", icon: Database },
    { name: "redis-cache", org: "launchkit", icon: Layers },
    { name: "nextjs-frontend", org: "launchkit", icon: Cloud },
  ];
  const [visible, setVisible] = useState(0);

  useEffect(() => {
    const timers: ReturnType<typeof setTimeout>[] = [];
    repos.forEach((_, i) => {
      timers.push(setTimeout(() => setVisible(i + 1), 400 + i * 300));
    });
    return () => timers.forEach(clearTimeout);
  }, []);

  return (
    <div className="space-y-3">
      {repos.map((repo, i) => (
        <div
          key={repo.name}
          className="flex items-center gap-3 rounded-lg border border-border/50 bg-background/50 p-3 transition-all"
          style={{ opacity: i < visible ? 1 : 0, transform: i < visible ? "translateY(0)" : "translateY(8px)", transition: "all 0.3s ease" }}
        >
          <div className="h-8 w-8 rounded-lg bg-primary/10 text-primary flex items-center justify-center">
            <repo.icon className="h-4 w-4" />
          </div>
          <div className="text-left">
            <p className="text-sm font-mono text-foreground">{repo.org}/{repo.name}</p>
            <p className="text-xs text-muted-foreground">Connected · main branch</p>
          </div>
          <div className="ml-auto">
            <span className="text-[10px] font-medium px-1.5 py-0.5 rounded bg-primary/15 text-primary">Linked</span>
          </div>
        </div>
      ))}
    </div>
  );
}

/* Deploy tab: terminal log */
function DeployTab() {
  const lines = [
    "$ launchkit deploy postgres-api",
    "◆ Analyzing codebase...",
    "✓  Next.js 14 · TypeScript · Tailwind CSS",
    "✓  PostgreSQL connection detected",
    "◆ Planning infrastructure...",
    "✓  Cloud Run  ·  Neon DB  ·  Cloudflare Pages",
    "◆ Building image...",
    "✓  Pushed to Artifact Registry",
    "◆ Deploying to Cloud Run...",
    "✓  Deployed",
    "→  https://myapp-abc123.launchkit.app",
  ];
  const [visibleCount, setVisibleCount] = useState(0);

  useEffect(() => {
    setVisibleCount(0);
    const timers: ReturnType<typeof setTimeout>[] = [];
    lines.forEach((_, i) => {
      timers.push(setTimeout(() => setVisibleCount(i + 1), 300 + i * 350));
    });
    return () => timers.forEach(clearTimeout);
  }, []);

  return (
    <div className="font-mono text-xs text-amber-400/80 space-y-1">
      {lines.slice(0, visibleCount).map((line, i) => {
        const lower = line.toLowerCase();
        const color = line.startsWith("$") ? "text-foreground"
          : lower.startsWith("✓") ? "text-primary"
          : line.startsWith("→") ? "text-amber-400 underline underline-offset-2"
          : "text-muted-foreground";
        return (
          <div key={i} className={`log-line ${color}`} style={{ animationDelay: `${i * 0.05}s` }}>
            {line}
          </div>
        );
      })}
    </div>
  );
}

/* Monitor tab: metric cards with count-up */
function MonitorTab() {
  return (
    <div className="grid grid-cols-3 gap-3">
      <MetricCard label="CPU" value="12%" sub="avg 1h" />
      <MetricCard label="RAM" value="256 MB" sub="of 512 MB" />
      <MetricCard label="Requests" value="1.2k" sub="past 1h" />
    </div>
  );
}

function MetricCard({ label, value, sub }: { label: string; value: string; sub: string }) {
  return (
    <div className="rounded-lg border border-border/50 bg-background/50 p-4 text-center">
      <p className="text-lg font-light text-foreground tabular-nums">{value}</p>
      <p className="text-[10px] text-muted-foreground mt-1">{label}</p>
      <p className="text-[10px] text-muted-foreground/50">{sub}</p>
    </div>
  );
}

/* Secrets tab */
function SecretsTab() {
  const vars = [
    { key: "DATABASE_URL", set: true },
    { key: "REDIS_URL", set: true },
    { key: "STRIPE_SECRET", set: true },
    { key: "NEXT_PUBLIC_API_URL", set: false },
  ];
  return (
    <div className="space-y-2">
      {vars.map((v) => (
        <div key={v.key} className="flex items-center justify-between rounded-lg border border-border/50 bg-background/50 px-3 py-2">
          <code className="text-xs font-mono text-foreground">{v.key}</code>
          <span className={`text-[10px] px-1.5 py-0.5 rounded border font-mono ${v.set ? "text-primary border-primary/20 bg-primary/5" : "text-amber-400 border-amber-400/20 bg-amber-400/5"}`}>
            {v.set ? "set" : "pending"}
          </span>
        </div>
      ))}
    </div>
  );
}

/* Domains tab */
function DomainsTab() {
  const domains = [
    { name: "myapp.launchkit.app", status: "Live" },
    { name: "api.launchkit.dev", status: "Pending" },
  ];
  return (
    <div className="space-y-2">
      {domains.map((d) => (
        <div key={d.name} className="flex items-center justify-between rounded-lg border border-border/50 bg-background/50 px-3 py-2">
          <code className="text-xs font-mono text-foreground">{d.name}</code>
          <span className={`text-[10px] px-1.5 py-0.5 rounded font-mono ${d.status === "Live" ? "text-primary bg-primary/10" : "text-amber-400 bg-amber-400/10"}`}>
            {d.status}
          </span>
        </div>
      ))}
    </div>
  );
}

/* Marquee strip */
function MarqueeStrip() {
  const items = ["Cloud Run", "Neon", "Upstash", "Cloudflare", "GCS", "AWS", "Artifact Registry"];
  return (
    <div className="overflow-hidden">
      <div className="flex w-max animate-marquee gap-8">
        {[...items, ...items].map((item, i) => (
          <div key={i} className="flex items-center gap-2 rounded-full border border-border/30 bg-card/40 px-4 py-2 text-sm text-muted-foreground whitespace-nowrap">
            <Shield className="h-3.5 w-3.5 text-primary/60" />
            {item}
          </div>
        ))}
      </div>
    </div>
  );
}
