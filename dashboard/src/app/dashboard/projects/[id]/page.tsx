"use client";

import { useState, useEffect, useCallback, use, useRef } from "react";
import Link from "next/link";
import { useRouter } from "next/navigation";
import {
  Globe, Loader2, AlertCircle,
  CheckCircle2, Circle, X, XCircle,
  Zap, Eye, EyeOff, Plus, Trash2, Search,
  ShoppingCart, Link2, RefreshCw, KeyRound,
  ChevronDown, ChevronRight, ExternalLink, GitBranch,
  Trash, ArrowLeft,
} from "lucide-react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Badge } from "@/components/ui/badge";
import { toast } from "sonner";
import { apiRequest, streamDeployLogs } from "@/lib/api";
import type {
  DeploymentDetail,
  DeploymentListItem,
  ProjectHealth,
  SecretItem,
  PurchasedDomain,
  DomainSearchResult,
  RegistrarDNSRecord,
  DNSListResult,
  DNSChangeResult,
} from "@/lib/types";

/* ─────────────────────────────────────────────── */

const TERMINAL = new Set(["live", "failed", "cancelled", "error"]);

export default function ProjectPage({ params }: { params: Promise<{ id: string }> }) {
  const { id } = use(params);
  const [projectName, setProjectName] = useState("Project");
  const [liveURL, setLiveURL] = useState<string | null>(null);
  const [activeTab, setActiveTab] = useState("overview");

  const handleDeployLoaded = useCallback((name: string, url: string | null) => {
    setProjectName(name);
    if (url) setLiveURL(url);
  }, []);

  return (
    <div className="px-6 py-8 max-w-6xl mx-auto space-y-6">
      {/* Breadcrumb + header */}
      <div className="fade-up">
        <div className="flex items-center gap-2 text-sm text-muted-foreground mb-3">
          <Link href="/dashboard" className="inline-flex items-center gap-1 hover:text-foreground transition-colors">
            <ArrowLeft className="h-3 w-3" />
            Projects
          </Link>
          <ChevronRight className="h-3 w-3" />
          <span className="text-foreground font-medium">{projectName}</span>
        </div>

        <div className="flex items-center justify-between flex-wrap gap-3">
          <h1 className="text-2xl font-light tracking-[-0.02em]" style={{ fontFamily: "var(--font-ibm-plex-serif)" }}>
            {projectName}
          </h1>
          {liveURL && (
            <a
              href={liveURL}
              target="_blank"
              rel="noopener noreferrer"
              className="inline-flex items-center gap-1.5 text-xs text-primary hover:text-primary/80 transition-colors border border-primary/20 rounded-lg px-3 py-2 bg-primary/5"
            >
              <Globe className="h-3 w-3" />
              {liveURL.replace(/^https?:\/\//, "")}
              <ExternalLink className="h-3 w-3 opacity-60" />
            </a>
          )}
        </div>
      </div>

      {/* Tab bar */}
      <div className="flex gap-0 border-b border-border/50 fade-up fade-up-delay-1">
        {["overview", "deployments", "secrets", "domains", "settings"].map((tab) => (
          <button
            key={tab}
            onClick={() => setActiveTab(tab)}
            className={`relative cursor-pointer px-4 py-2.5 text-sm font-medium transition-colors border-b-2 -mb-px capitalize ${
              activeTab === tab
                ? "border-primary text-foreground"
                : "border-transparent text-muted-foreground hover:text-foreground"
            }`}
          >
            {tab}
          </button>
        ))}
      </div>

      {/* Tab content */}
      <div className="fade-up fade-up-delay-2">
        {activeTab === "overview" && (
          <OverviewTab projectId={id} onInfoLoaded={handleDeployLoaded} />
        )}
        {activeTab === "deployments" && (
          <DeploymentsTab projectId={id} onInfoLoaded={handleDeployLoaded} />
        )}
        {activeTab === "secrets" && <SecretsTab projectId={id} />}
        {activeTab === "domains" && <DomainsTab projectId={id} />}
        {activeTab === "settings" && <SettingsTab projectId={id} />}
      </div>
    </div>
  );
}

/* ══════════════════════════════════════════════
   OVERVIEW TAB
══════════════════════════════════════════════ */
function OverviewTab({
  projectId,
  onInfoLoaded,
}: {
  projectId: string;
  onInfoLoaded: (name: string, url: string | null) => void;
}) {
  const [health, setHealth] = useState<ProjectHealth | null>(null);
  const [loading, setLoading] = useState(true);

  useEffect(() => {
    apiRequest<ProjectHealth>(`/api/projects/${projectId}/health`)
      .then((h) => { setHealth(h); })
      .catch(() => {})
      .finally(() => setLoading(false));
  }, [projectId]);

  if (loading) return <SkeletonCards count={3} />;
  if (!health) return null;

  const statusConfig: Record<string, { color: string; label: string; dot: string }> = {
    healthy:  { color: "text-primary", label: "Healthy",  dot: "bg-primary" },
    degraded: { color: "text-amber-400",   label: "Degraded", dot: "bg-amber-400"   },
    down:     { color: "text-red-400",     label: "Down",     dot: "bg-red-400"      },
    unknown:  { color: "text-muted-foreground",   label: "Unknown",  dot: "bg-muted-foreground"   },
  };
  const sc = statusConfig[health.status] ?? statusConfig.unknown;

  return (
    <div className="space-y-5">
      {/* Status banner */}
      <div className="rounded-xl border border-border/50 bg-card p-5 flex items-center gap-3">
        <span className={`h-2.5 w-2.5 rounded-full shrink-0 ${sc.dot}`} />
        <span className={`text-sm font-medium ${sc.color}`}>{sc.label}</span>
        {health.last_deploy && (
          <span className="text-xs text-muted-foreground ml-auto">
            Last deployed {relTime(health.last_deploy.started_at)}
            {" · "}
            {health.last_deploy.trigger}
          </span>
        )}
      </div>

      {/* Metrics */}
      <div className="grid grid-cols-3 gap-4">
        {[
          { label: "Error rate", value: health.error_rate_1h == null ? "—" : `${health.error_rate_1h.toFixed(1)}%`, sub: "past 1h" },
          { label: "P95 latency", value: health.p95_ms == null ? "—" : `${Math.round(health.p95_ms)}ms`, sub: "past 1h" },
          { label: "Requests", value: health.requests_per_min == null ? "—" : `${Math.round(health.requests_per_min)}/min`, sub: "past 1h" },
        ].map((m) => (
          <div key={m.label} className="rounded-xl border border-border/50 bg-card p-5">
            <p className="text-2xl font-light tabular-nums text-foreground">{m.value}</p>
            <p className="text-xs text-muted-foreground mt-1">{m.label}</p>
            <p className="text-[10px] text-muted-foreground/60">{m.sub}</p>
          </div>
        ))}
      </div>
    </div>
  );
}

/* ══════════════════════════════════════════════
   DEPLOYMENTS TAB
══════════════════════════════════════════════ */
function DeploymentsTab({
  projectId,
  onInfoLoaded,
}: {
  projectId: string;
  onInfoLoaded: (name: string, url: string | null) => void;
}) {
  const [deployments, setDeployments] = useState<DeploymentListItem[]>([]);
  const [loading, setLoading] = useState(true);
  const [expanded, setExpanded] = useState<string | null>(null);
  const [redeploying, setRedeploying] = useState(false);
  const loaded = useRef(false);

  const load = useCallback(async () => {
    setLoading(true);
    try {
      const data = await apiRequest<{ deployments: DeploymentListItem[] }>(
        `/api/projects/${projectId}/deployments`
      );
      const list = data.deployments ?? [];
      setDeployments(list);
      if (!loaded.current && list.length > 0) {
        loaded.current = true;
        const first = list[0];
        onInfoLoaded(first.env_name, first.service_url || null);
      }
    } catch {
      toast.error("Failed to load deployments");
    } finally {
      setLoading(false);
    }
  }, [projectId, onInfoLoaded]);

  useEffect(() => { load(); }, [load]);

  useEffect(() => {
    const hasActive = deployments.some((d) => !TERMINAL.has(d.status));
    if (!hasActive) return;
    const interval = setInterval(load, 8000);
    return () => clearInterval(interval);
  }, [deployments, load]);

  const handleRedeploy = async () => {
    setRedeploying(true);
    try {
      await apiRequest<{ deployment_id: string }>(
        `/api/projects/${projectId}/redeploy`,
        { method: "POST" }
      );
      toast.success("Redeploy triggered");
      await load();
    } catch (err) {
      toast.error(`Redeploy failed: ${err instanceof Error ? err.message : "unknown"}`);
    } finally {
      setRedeploying(false);
    }
  };

  if (loading) return <SkeletonList />;

  if (deployments.length === 0) {
    return (
      <Empty
        icon={<Zap className="h-5 w-5" />}
        title="No deployments yet"
        desc='Tell Claude "deploy this project" and it will appear here.'
      />
    );
  }

  return (
    <div className="rounded-xl border border-border/50 bg-card overflow-hidden">
      <div className="px-5 py-3.5 border-b border-border/50 flex items-center justify-between">
        <p className="text-sm font-medium text-foreground">Deployments</p>
        <div className="flex items-center gap-2">
          <Button size="sm" variant="outline" onClick={handleRedeploy} disabled={redeploying} className="cursor-pointer rounded-lg gap-1.5 text-xs border-border/50">
            {redeploying ? <Loader2 className="h-3 w-3 animate-spin" /> : <RefreshCw className="h-3 w-3" />}
            Redeploy
          </Button>
          <button onClick={load} className="cursor-pointer text-muted-foreground hover:text-foreground transition-colors p-1.5 rounded-lg hover:bg-accent/50">
            <RefreshCw className="h-4 w-4" />
          </button>
        </div>
      </div>
      <div className="divide-y divide-border/40">
        {deployments.map((d) => (
          <DeployRow
            key={d.id}
            d={d}
            isExpanded={expanded === d.id}
            onToggle={() => setExpanded((prev) => (prev === d.id ? null : d.id))}
          />
        ))}
      </div>
    </div>
  );
}

function DeployRow({
  d,
  isExpanded,
  onToggle,
}: {
  d: DeploymentListItem;
  isExpanded: boolean;
  onToggle: () => void;
}) {
  const dur = (() => {
    if (!d.finished_at) return null;
    const s = Math.round(
      (new Date(d.finished_at).getTime() - new Date(d.started_at).getTime()) / 1000
    );
    return s < 60 ? `${s}s` : `${Math.floor(s / 60)}m ${s % 60}s`;
  })();

  return (
    <div>
      <button
        onClick={onToggle}
        className="cursor-pointer w-full flex items-center justify-between px-5 py-4 hover:bg-accent/30 transition-colors text-left"
      >
        <div className="flex items-center gap-3 min-w-0">
          <DeployStatusIcon status={d.status} />
          <div className="min-w-0">
            <div className="flex items-center gap-2 flex-wrap">
              <span className="text-sm font-medium text-foreground">{d.service_name}</span>
              <span className="text-[10px] text-muted-foreground capitalize border border-border/50 rounded px-1.5 py-0.5">
                {d.trigger}
              </span>
            </div>
            {d.service_url && (
              <div className="flex items-center gap-1 text-xs text-amber-400 mt-0.5 truncate">
                <Globe className="h-3 w-3 shrink-0" />
                <span className="truncate">{d.service_url.replace(/^https?:\/\//, "")}</span>
              </div>
            )}
          </div>
        </div>
        <div className="flex items-center gap-4 shrink-0 ml-4">
          <div className="text-right">
            <p className="text-xs text-muted-foreground">{relTime(d.started_at)}</p>
            {dur && <p className="text-xs text-muted-foreground">{dur}</p>}
          </div>
          <ChevronDown
            className={`h-4 w-4 text-muted-foreground transition-transform ${isExpanded ? "rotate-180" : ""}`}
          />
        </div>
      </button>

      {isExpanded && (
        <DeployLogPane deploymentId={d.id} isActive={!TERMINAL.has(d.status)} />
      )}
    </div>
  );
}

/* Inline log pane with SSE streaming */
function DeployLogPane({
  deploymentId,
  isActive,
}: {
  deploymentId: string;
  isActive: boolean;
}) {
  const [detail, setDetail] = useState<DeploymentDetail | null>(null);
  const [lines, setLines] = useState<string[]>([]);
  const [liveStatus, setLiveStatus] = useState("");
  const [streamDone, setStreamDone] = useState(false);
  const logRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    apiRequest<DeploymentDetail>(`/api/deployments/${deploymentId}`)
      .then((d) => {
        setDetail(d);
        if (d.build_log) {
          setLines(d.build_log.split("\n"));
        }
      })
      .catch(() => {});
  }, [deploymentId]);

  useEffect(() => {
    if (!isActive) return;
    setLines([]);
    const stop = streamDeployLogs(
      deploymentId,
      (line) => setLines((prev) => [...prev, line]),
      (s) => setLiveStatus(s),
      (s) => { setLiveStatus(s); setStreamDone(true); }
    );
    return stop;
  }, [deploymentId, isActive]);

  useEffect(() => {
    if (logRef.current) {
      logRef.current.scrollTop = logRef.current.scrollHeight;
    }
  }, [lines]);

  return (
    <div className="border-t border-border/50 bg-background">
      {/* Steps */}
      {detail && detail.steps.length > 0 && (
        <div className="flex items-center gap-0 px-5 py-3 border-b border-border/50 overflow-x-auto">
          {detail.steps.map((step, i) => (
            <div key={step.id} className="flex items-center shrink-0">
              <StepPill step={step} />
              {i < detail.steps.length - 1 && (
                <div className="w-5 h-px bg-border/30 mx-1" />
              )}
            </div>
          ))}
        </div>
      )}

      {/* Log */}
      <div
        ref={logRef}
        className="h-64 overflow-y-auto p-4 font-mono text-xs leading-relaxed text-amber-400/80"
      >
        {lines.length === 0 && !streamDone ? (
          <div className="flex items-center gap-2 text-muted-foreground">
            <Loader2 className="h-3 w-3 animate-spin" />
            <span>Loading logs...</span>
          </div>
        ) : (
          <LogContent lines={lines} />
        )}
        {isActive && !streamDone && (
          <div className="flex items-center gap-1.5 text-amber-400 mt-1">
            <Loader2 className="h-3 w-3 animate-spin" />
            <span>{liveStatus || "deploying..."}</span>
          </div>
        )}
      </div>

      {/* Footer */}
      {detail?.service_url && (
        <div className="flex items-center justify-between px-5 py-3 border-t border-border/50">
          <div className="flex items-center gap-2">
            <DeployStatusIcon status={liveStatus || detail.status} />
            <span className="text-xs text-muted-foreground font-mono capitalize">
              {liveStatus || detail.status}
            </span>
          </div>
          <a
            href={detail.service_url}
            target="_blank"
            rel="noopener noreferrer"
            className="flex items-center gap-1.5 text-xs text-amber-400 hover:text-amber-300 transition-colors"
          >
            <Globe className="h-3 w-3" />
            {detail.service_url.replace(/^https?:\/\//, "")}
          </a>
        </div>
      )}
    </div>
  );
}

/* ══════════════════════════════════════════════
   SECRETS TAB
══════════════════════════════════════════════ */
function SecretsTab({ projectId }: { projectId: string }) {
  const [secrets, setSecrets] = useState<SecretItem[]>([]);
  const [loading, setLoading] = useState(true);
  const [adding, setAdding] = useState(false);
  const [newKey, setNewKey] = useState("");
  const [newVal, setNewVal] = useState("");
  const [showVal, setShowVal] = useState(false);
  const [saving, setSaving] = useState(false);

  const load = useCallback(async () => {
    setLoading(true);
    try {
      const data = await apiRequest<{ secrets: SecretItem[] }>(
        `/api/projects/${projectId}/secrets`
      );
      setSecrets(data.secrets ?? []);
    } catch {
      toast.error("Failed to load secrets");
    } finally {
      setLoading(false);
    }
  }, [projectId]);

  useEffect(() => { load(); }, [load]);

  const handleAdd = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!newKey.trim() || !newVal.trim()) return;
    setSaving(true);
    try {
      await apiRequest(`/api/projects/${projectId}/secrets/${encodeURIComponent(newKey.trim())}`, {
        method: "PUT",
        body: JSON.stringify({ value: newVal.trim() }),
      });
      setNewKey("");
      setNewVal("");
      setAdding(false);
      await load();
      toast.success(`${newKey.trim()} saved`);
    } catch (err) {
      toast.error(`Save failed: ${err instanceof Error ? err.message : "unknown"}`);
    } finally {
      setSaving(false);
    }
  };

  const handleDelete = async (name: string) => {
    try {
      await apiRequest(`/api/projects/${projectId}/secrets/${encodeURIComponent(name)}`, {
        method: "DELETE",
      });
      await load();
      toast.success(`${name} deleted`);
    } catch (err) {
      toast.error(`Delete failed: ${err instanceof Error ? err.message : "unknown"}`);
    }
  };

  if (loading) return <SkeletonList />;

  return (
    <div className="space-y-4">
      {/* Header */}
      <div className="flex items-center justify-between">
        <div>
          <p className="text-sm font-medium">Environment Variables</p>
          <p className="text-xs text-muted-foreground mt-0.5">
            Values encrypted at rest, never in build logs
          </p>
        </div>
        <Button
          size="sm"
          onClick={() => setAdding(true)}
          disabled={adding}
          className="cursor-pointer rounded-lg gap-1.5 min-h-[40px] bg-primary text-primary-foreground hover:opacity-90"
        >
          <Plus className="h-3.5 w-3.5" />
          Add
        </Button>
      </div>

      {/* Add form */}
      {adding && (
        <form onSubmit={handleAdd} className="rounded-xl border border-primary/30 bg-primary/5 p-4 space-y-3 fade-up">
          <p className="text-xs font-medium text-primary">New Secret</p>
          <div className="grid grid-cols-2 gap-2">
            <div>
              <label className="text-xs text-muted-foreground mb-1 block">Key</label>
              <Input
                placeholder="DATABASE_URL"
                value={newKey}
                onChange={(e) => setNewKey(e.target.value.toUpperCase().replace(/\s/g, "_"))}
                className="rounded-lg border-border/50 focus:border-primary/40 font-mono text-xs"
                autoFocus
              />
            </div>
            <div>
              <label className="text-xs text-muted-foreground mb-1 block">Value</label>
              <div className="relative">
                <Input
                  type={showVal ? "text" : "password"}
                  placeholder="postgres://..."
                  value={newVal}
                  onChange={(e) => setNewVal(e.target.value)}
                  className="rounded-lg font-mono text-xs pr-9 border-border/50 focus:border-primary/40"
                />
                <button
                  type="button"
                  onClick={() => setShowVal((v) => !v)}
                  className="cursor-pointer absolute right-2.5 top-1/2 -translate-y-1/2 text-muted-foreground hover:text-foreground"
                >
                  {showVal ? <EyeOff className="h-3.5 w-3.5" /> : <Eye className="h-3.5 w-3.5" />}
                </button>
              </div>
            </div>
          </div>
          <div className="flex gap-2">
            <Button
              type="submit"
              size="sm"
              disabled={saving || !newKey.trim() || !newVal.trim()}
              className="cursor-pointer rounded-lg gap-1.5 bg-primary text-primary-foreground hover:opacity-90"
            >
              {saving ? <Loader2 className="h-3 w-3 animate-spin" /> : null}
              Save
            </Button>
            <Button
              type="button"
              size="sm"
              variant="ghost"
              onClick={() => { setAdding(false); setNewKey(""); setNewVal(""); }}
              className="cursor-pointer rounded-lg text-muted-foreground"
            >
              Cancel
            </Button>
          </div>
        </form>
      )}

      {/* Secrets list */}
      {secrets.length === 0 && !adding ? (
        <Empty
          icon={<KeyRound className="h-5 w-5" />}
          title="No secrets yet"
          desc="Add environment variables so your app can read DB connections, API keys, etc."
        />
      ) : (
        <div className="rounded-xl border border-border/50 bg-card overflow-hidden">
          <div className="divide-y divide-border/40">
            {secrets.map((s) => (
              <div key={s.name} className="flex items-center justify-between px-5 py-3.5 hover:bg-card/80 transition-colors">
                <div className="flex items-center gap-3">
                  <div className={`h-2 w-2 rounded-full shrink-0 ${s.is_set ? "bg-primary" : "bg-amber-400"}`} />
                  <code className="text-sm font-mono">{s.name}</code>
                  <span className={`text-[10px] px-1.5 py-0.5 rounded border font-mono ${s.is_set ? "text-primary border-primary/20 bg-primary/5" : "text-amber-400 border-amber-400/20 bg-amber-400/5"}`}>
                    {s.is_set ? "set" : "pending"}
                  </span>
                </div>
                <button
                  onClick={() => handleDelete(s.name)}
                  className="cursor-pointer text-muted-foreground hover:text-destructive transition-colors p-2 rounded-lg hover:bg-destructive/10"
                >
                  <Trash2 className="h-3.5 w-3.5" />
                </button>
              </div>
            ))}
          </div>
        </div>
      )}

      {secrets.length > 0 && (
        <p className="text-xs text-muted-foreground">
          Values are hidden. New secrets take effect on next redeploy.
        </p>
      )}
    </div>
  );
}

/* ══════════════════════════════════════════════
   DOMAINS TAB
══════════════════════════════════════════════ */
function DomainsTab({ projectId }: { projectId: string }) {
  const [domains, setDomains] = useState<PurchasedDomain[]>([]);
  const [loading, setLoading] = useState(true);
  const [query, setQuery] = useState("");
  const [searching, setSearching] = useState(false);
  const [results, setResults] = useState<DomainSearchResult[]>([]);
  const [attaching, setAttaching] = useState<string | null>(null);
  const [attachURL, setAttachURL] = useState("");
  const [attachSaving, setAttachSaving] = useState(false);
  const [expandedDNSDomain, setExpandedDNSDomain] = useState<string | null>(null);
  const [dnsRecords, setDnsRecords] = useState<Record<string, RegistrarDNSRecord[]>>({});
  const [dnsLoading, setDnsLoading] = useState(false);
  const [dnsDeleting, setDnsDeleting] = useState<number | null>(null);
  const [dnsConfirmDelete, setDnsConfirmDelete] = useState<number | null>(null);
  const [dnsAdding, setDnsAdding] = useState(false);
  const [dnsForm, setDnsForm] = useState<{ type: string; host: string; value: string; ttl: string }>({
    type: "A", host: "", value: "", ttl: "300",
  });

  const load = useCallback(async () => {
    setLoading(true);
    try {
      const data = await apiRequest<{ domains: PurchasedDomain[] }>(
        `/api/projects/${projectId}/domains`
      );
      setDomains(data.domains ?? []);
    } catch {
      toast.error("Failed to load domains");
    } finally {
      setLoading(false);
    }
  }, [projectId]);

  useEffect(() => { load(); }, [load]);

  const handleSearch = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!query.trim()) return;
    setSearching(true);
    setResults([]);
    try {
      const data = await apiRequest<{ results: DomainSearchResult[] }>(
        `/api/domains/search?q=${encodeURIComponent(query.trim())}`
      );
      setResults(data.results ?? []);
    } catch (err) {
      toast.error(`Search failed: ${err instanceof Error ? err.message : "unknown"}`);
    } finally {
      setSearching(false);
    }
  };

  const handleRegister = async (domain: string) => {
    try {
      await apiRequest(`/api/projects/${projectId}/domains/register`, {
        method: "POST",
        body: JSON.stringify({ domain, years: 1 }),
      });
      toast.success(`${domain} purchased`);
      setResults([]);
      setQuery("");
      await load();
    } catch (err) {
      toast.error(`Purchase failed: ${err instanceof Error ? err.message : "unknown"}`);
    }
  };

  const handleAttach = async (domain: string) => {
    if (!attachURL.trim()) return;
    setAttachSaving(true);
    try {
      await apiRequest(`/api/projects/${projectId}/domains/attach`, {
        method: "POST",
        body: JSON.stringify({ domain, service_url: attachURL.trim() }),
      });
      toast.success(`${domain} attached`);
      setAttaching(null);
      setAttachURL("");
      await load();
    } catch (err) {
      toast.error(`Attach failed: ${err instanceof Error ? err.message : "unknown"}`);
    } finally {
      setAttachSaving(false);
    }
  };

  const loadDNS = useCallback(async (domain: string) => {
    setDnsLoading(true);
    try {
      const data = await apiRequest<DNSListResult>(
        `/api/projects/${projectId}/dns?domain=${encodeURIComponent(domain)}`
      );
      setDnsRecords((prev) => ({ ...prev, [domain]: data.records ?? [] }));
    } catch {
      toast.error("Failed to load DNS records");
    } finally {
      setDnsLoading(false);
    }
  }, [projectId]);

  const handleDeleteDNS = async (domain: string, recordId: number) => {
    setDnsDeleting(recordId);
    try {
      await apiRequest<DNSChangeResult>(`/api/projects/${projectId}/dns`, {
        method: "DELETE",
        body: JSON.stringify({ domain, records: [{ id: recordId }] }),
      });
      toast.success("DNS record deleted");
      setDnsConfirmDelete(null);
      setDnsRecords((prev) => ({
        ...prev,
        [domain]: (prev[domain] ?? []).filter((r) => r.id !== recordId),
      }));
    } catch (err) {
      toast.error(`Delete failed: ${err instanceof Error ? err.message : "unknown"}`);
    } finally {
      setDnsDeleting(null);
    }
  };

  const handleAddDNS = async (domain: string) => {
    if (!dnsForm.type || !dnsForm.host || !dnsForm.value) return;
    setDnsAdding(true);
    try {
      const body: { domain: string; records: Array<{ type: string; host: string; value: string; ttl?: number }> } = {
        domain,
        records: [{
          type: dnsForm.type,
          host: dnsForm.host.trim(),
          value: dnsForm.value.trim(),
          ttl: dnsForm.ttl ? parseInt(dnsForm.ttl, 10) : undefined,
        }],
      };
      const data = await apiRequest<DNSChangeResult>(`/api/projects/${projectId}/dns`, {
        method: "POST",
        body: JSON.stringify(body),
      });
      if (data.errors && data.errors.length > 0) {
        toast.error(`Failed: ${data.errors.join(", ")}`);
        return;
      }
      toast.success("DNS record added");
      setDnsForm({ type: "A", host: "", value: "", ttl: "300" });
      if (data.created && data.created.length > 0) {
        setDnsRecords((prev) => ({
          ...prev,
          [domain]: [...(prev[domain] ?? []), ...data.created!],
        }));
      } else {
        await loadDNS(domain);
      }
    } catch (err) {
      toast.error(`Add failed: ${err instanceof Error ? err.message : "unknown"}`);
    } finally {
      setDnsAdding(false);
    }
  };

  const toggleDNSPanel = (domain: string) => {
    if (expandedDNSDomain === domain) {
      setExpandedDNSDomain(null);
      setDnsConfirmDelete(null);
    } else {
      setExpandedDNSDomain(domain);
      setDnsConfirmDelete(null);
      if (!dnsRecords[domain]) {
        loadDNS(domain);
      }
    }
  };

  return (
    <div className="space-y-6">
      {/* Registered domains */}
      <div>
        <div className="flex items-center justify-between mb-3">
          <p className="text-sm font-medium">Purchased Domains</p>
          <button onClick={load} className="cursor-pointer text-muted-foreground hover:text-foreground transition-colors p-1.5 rounded-lg hover:bg-accent/50">
            <RefreshCw className="h-4 w-4" />
          </button>
        </div>

        {loading ? (
          <SkeletonList />
        ) : domains.length === 0 ? (
          <div className="rounded-xl border border-border/50 bg-card/40 px-5 py-8 text-center">
            <Globe className="h-7 w-7 mx-auto mb-3 text-muted-foreground/40" />
            <p className="text-sm text-muted-foreground">No domains purchased</p>
            <p className="text-xs text-muted-foreground/60 mt-1">Search and buy a domain below</p>
          </div>
        ) : (
          <div className="rounded-xl border border-border/50 bg-card overflow-hidden">
            <div className="divide-y divide-border/40">
              {domains.map((d) => (
                <div key={d.id}>
                  <div className="flex items-center justify-between px-5 py-4">
                    <div className="flex items-center gap-3">
                      <Globe className="h-4 w-4 text-muted-foreground shrink-0" />
                      <div>
                        <p className="text-sm font-medium font-mono">{d.domain}</p>
                        <div className="flex items-center gap-2 mt-0.5">
                          <DomainStatusBadge status={d.status} />
                          <span className="text-xs text-muted-foreground">
                            Expires {new Date(d.expires_at).toLocaleDateString("en-US", { month: "short", day: "numeric", year: "numeric" })}
                          </span>
                          {d.dns_configured && <span className="text-xs text-primary">DNS ✓</span>}
                        </div>
                      </div>
                    </div>
                    <div className="flex items-center gap-2">
                      <Button
                        size="sm"
                        variant="outline"
                        onClick={() => toggleDNSPanel(d.domain)}
                        className="cursor-pointer rounded-lg gap-1.5 text-xs border-border/50"
                      >
                        <ChevronDown className={`h-3 w-3 transition-transform ${expandedDNSDomain === d.domain ? "rotate-180" : ""}`} />
                        DNS
                      </Button>
                      <Button
                        size="sm"
                        variant="outline"
                        onClick={() => setAttaching((prev) => (prev === d.domain ? null : d.domain))}
                        className="cursor-pointer rounded-lg gap-1.5 text-xs border-border/50"
                      >
                        <Link2 className="h-3 w-3" />
                        {attaching === d.domain ? "Cancel" : "Attach"}
                      </Button>
                    </div>
                  </div>

                  {attaching === d.domain && (
                    <div className="px-5 pb-4 border-t border-border/50 pt-3 bg-accent/20">
                      <p className="text-xs text-muted-foreground mb-2">
                        Enter the Service URL to attach (your Cloud Run / Fargate URL)
                      </p>
                      <div className="flex gap-2">
                        <Input
                          placeholder="https://myapp-xxx.run.app"
                          value={attachURL}
                          onChange={(e) => setAttachURL(e.target.value)}
                          className="rounded-lg font-mono text-xs border-border/50 focus:border-primary/40"
                          autoFocus
                        />
                        <Button
                          size="sm"
                          onClick={() => handleAttach(d.domain)}
                          disabled={attachSaving || !attachURL.trim()}
                          className="cursor-pointer rounded-lg shrink-0 bg-primary text-primary-foreground hover:opacity-90"
                        >
                          {attachSaving ? <Loader2 className="h-3 w-3 animate-spin" /> : "Attach"}
                        </Button>
                      </div>
                    </div>
                  )}

                  {expandedDNSDomain === d.domain && (
                    <div className="px-5 pb-5 border-t border-border/50 pt-4 bg-accent/20">
                      <div className="flex items-center justify-between mb-3">
                        <p className="text-sm font-medium">DNS Records</p>
                        <button onClick={() => loadDNS(d.domain)} className="text-muted-foreground hover:text-foreground transition-colors p-1 rounded-lg hover:bg-accent/50">
                          <RefreshCw className={`h-4 w-4 ${dnsLoading ? "animate-spin" : ""}`} />
                        </button>
                      </div>

                      {dnsLoading ? (
                        <div className="flex items-center justify-center py-6">
                          <Loader2 className="h-5 w-5 animate-spin text-muted-foreground" />
                        </div>
                      ) : (dnsRecords[d.domain] ?? []).length === 0 ? (
                        <div className="rounded-lg border border-border/50 bg-accent/30 px-4 py-5 text-center mb-4">
                          <p className="text-sm text-muted-foreground">No DNS records</p>
                        </div>
                      ) : (
                        <div className="space-y-2 mb-4">
                          {(dnsRecords[d.domain] ?? []).map((rec) => (
                            <div key={rec.id} className="flex items-center gap-3 rounded-lg border border-border/50 bg-accent/30 px-3 py-2.5">
                              <Badge variant="secondary" className="text-[10px] font-mono bg-muted text-foreground border-0 min-w-[44px] text-center">
                                {rec.type}
                              </Badge>
                              <div className="flex-1 min-w-0">
                                <div className="flex items-center gap-2">
                                  <code className="text-[11px] font-mono text-muted-foreground truncate">{rec.host || "@"}</code>
                                  <span className="text-[10px] text-muted-foreground/40">→</span>
                                  <code className="text-[11px] font-mono truncate max-w-[200px]" title={rec.value}>{rec.value}</code>
                                </div>
                                {rec.ttl && <p className="text-[10px] text-muted-foreground/60 mt-0.5">TTL {rec.ttl}s</p>}
                              </div>
                              {dnsDeleting === rec.id ? (
                                <Loader2 className="h-3.5 w-3.5 animate-spin text-muted-foreground shrink-0" />
                              ) : dnsConfirmDelete === rec.id ? (
                                <div className="flex items-center gap-1 shrink-0">
                                  <button onClick={() => handleDeleteDNS(d.domain, rec.id)} className="min-h-[32px] px-3 flex items-center justify-center rounded-lg bg-destructive/20 text-destructive text-xs font-medium">
                                    Confirm
                                  </button>
                                  <button onClick={() => setDnsConfirmDelete(null)} className="min-h-[32px] px-3 flex items-center justify-center rounded-lg bg-muted text-xs text-muted-foreground">
                                    Cancel
                                  </button>
                                </div>
                              ) : (
                                <button onClick={() => setDnsConfirmDelete(rec.id)} className="min-h-[32px] min-w-[32px] flex items-center justify-center rounded-lg hover:bg-destructive/10 text-muted-foreground hover:text-destructive transition-colors shrink-0">
                                  <Trash className="h-3.5 w-3.5" />
                                </button>
                              )}
                            </div>
                          ))}
                        </div>
                      )}

                      {/* Add DNS form */}
                      <div className="rounded-lg border border-border/50 bg-accent/30 p-3">
                        <p className="text-xs font-medium mb-2">Add DNS Record</p>
                        <div className="grid grid-cols-2 gap-2">
                          <select
                            value={dnsForm.type}
                            onChange={(e) => setDnsForm((prev) => ({ ...prev, type: e.target.value }))}
                            className="min-h-[40px] rounded-lg border border-border/50 bg-accent/50 px-3 py-2 text-xs font-mono focus:outline-none focus:ring-1 focus:ring-primary/20"
                          >
                            <option value="A">A</option>
                            <option value="AAAA">AAAA</option>
                            <option value="CNAME">CNAME</option>
                            <option value="TXT">TXT</option>
                            <option value="MX">MX</option>
                          </select>
                          <Input
                            placeholder="@ or subdomain"
                            value={dnsForm.host}
                            onChange={(e) => setDnsForm((prev) => ({ ...prev, host: e.target.value }))}
                            className="rounded-lg font-mono text-xs min-h-[40px] border-border/50 focus:border-primary/40"
                          />
                          <Input
                            placeholder="IP or target"
                            value={dnsForm.value}
                            onChange={(e) => setDnsForm((prev) => ({ ...prev, value: e.target.value }))}
                            className="rounded-lg font-mono text-xs min-h-[40px] border-border/50 focus:border-primary/40"
                          />
                          <Input
                            type="number"
                            placeholder="TTL"
                            value={dnsForm.ttl}
                            onChange={(e) => setDnsForm((prev) => ({ ...prev, ttl: e.target.value }))}
                            className="rounded-lg font-mono text-xs min-h-[40px] border-border/50 focus:border-primary/40"
                          />
                        </div>
                        <Button
                          size="sm"
                          onClick={() => handleAddDNS(d.domain)}
                          disabled={dnsAdding || !dnsForm.host || !dnsForm.value}
                          className="cursor-pointer rounded-lg gap-1.5 text-xs mt-3 w-full min-h-[40px] bg-primary text-primary-foreground hover:opacity-90"
                        >
                          {dnsAdding ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : <Plus className="h-3.5 w-3.5" />}
                          Add Record
                        </Button>
                      </div>
                    </div>
                  )}
                </div>
              ))}
            </div>
          </div>
        )}
      </div>

      {/* Domain search */}
      <div>
        <p className="text-sm font-medium mb-3">Search & Purchase</p>
        <form onSubmit={handleSearch} className="flex gap-2">
          <Input
            placeholder="myapp.dev"
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            className="rounded-lg font-mono text-sm border-border/50 focus:border-primary/40"
          />
          <Button
            type="submit"
            disabled={searching || !query.trim()}
            className="cursor-pointer rounded-lg shrink-0 gap-1.5 min-h-[44px] bg-primary text-primary-foreground hover:opacity-90"
          >
            {searching ? <Loader2 className="h-4 w-4 animate-spin" /> : <Search className="h-4 w-4" />}
            Search
          </Button>
        </form>

        {results.length > 0 && (
          <div className="mt-3 rounded-xl border border-border/50 bg-card overflow-hidden">
            <div className="divide-y divide-border/40">
              {results.map((r) => (
                <div key={r.domain} className="flex items-center justify-between px-5 py-3.5">
                  <div className="flex items-center gap-3">
                    <div className={`h-2 w-2 rounded-full ${r.available ? "bg-primary" : "bg-red-400"}`} />
                    <span className="text-sm font-mono">{r.domain}</span>
                    {r.available && <span className="text-xs text-muted-foreground">${r.price}/yr</span>}
                  </div>
                  {r.available ? (
                    <Button
                      size="sm"
                      onClick={() => handleRegister(r.domain)}
                      className="cursor-pointer rounded-lg gap-1.5 text-xs bg-primary text-primary-foreground hover:opacity-90"
                    >
                      <ShoppingCart className="h-3 w-3" />
                      Purchase
                    </Button>
                  ) : (
                    <span className="text-xs text-muted-foreground">Taken</span>
                  )}
                </div>
              ))}
            </div>
          </div>
        )}
      </div>
    </div>
  );
}

/* ══════════════════════════════════════════════
   SETTINGS TAB
══════════════════════════════════════════════ */
interface GitHubConnection {
  id: string;
  repo_full_name: string;
  branch: string;
  status: string;
  last_push_sha: string | null;
  created_at: string;
}

function SettingsTab({ projectId }: { projectId: string }) {
  const router = useRouter();
  const [connections, setConnections] = useState<GitHubConnection[]>([]);
  const [loadingGH, setLoadingGH] = useState(true);
  const [disconnecting, setDisconnecting] = useState<string | null>(null);
  const [deleting, setDeleting] = useState(false);

  useEffect(() => {
    apiRequest<{ connections: GitHubConnection[] }>(`/api/projects/${projectId}/github`)
      .then((d) => setConnections(d.connections ?? []))
      .catch(() => {})
      .finally(() => setLoadingGH(false));
  }, [projectId]);

  const handleDisconnect = async (connId: string, repoName: string) => {
    if (!window.confirm(`Disconnect ${repoName}?`)) return;
    setDisconnecting(connId);
    try {
      await apiRequest(`/api/projects/${projectId}/github/${connId}`, { method: 'DELETE' });
      setConnections((prev) => prev.filter((c) => c.id !== connId));
      toast.success('Disconnected');
    } catch (err) {
      toast.error(`Disconnect failed: ${err instanceof Error ? err.message : 'unknown'}`);
    } finally {
      setDisconnecting(null);
    }
  };

  const handleDeleteProject = async () => {
    if (!window.confirm('Delete this project? This cannot be undone.')) return;
    setDeleting(true);
    try {
      await apiRequest(`/api/projects/${projectId}`, { method: 'DELETE' });
      toast.success('Project deleted');
      router.push('/dashboard');
    } catch (err) {
      toast.error(`Delete failed: ${err instanceof Error ? err.message : 'unknown'}`);
      setDeleting(false);
    }
  };

  return (
    <div className="space-y-8">
      {/* GitHub Connections */}
      <div>
        <p className="text-sm font-medium mb-1">GitHub Connections</p>
        <p className="text-xs text-muted-foreground mb-4">
          Push to a connected repo to trigger automatic deployments
        </p>
        {loadingGH ? (
          <SkeletonList />
        ) : connections.length === 0 ? (
          <div className="rounded-xl border border-border/50 bg-card/40 px-5 py-8 text-center">
            <GitBranch className="h-7 w-7 mx-auto mb-3 text-muted-foreground/40" />
            <p className="text-sm text-muted-foreground">No GitHub connection</p>
            <p className="text-xs text-muted-foreground/60 mt-1">
              Ask Claude to "connect my GitHub repo"
            </p>
          </div>
        ) : (
          <div className="rounded-xl border border-border/50 bg-card overflow-hidden">
            <div className="divide-y divide-border/40">
              {connections.map((conn) => (
                <div key={conn.id} className="flex items-center justify-between px-5 py-4">
                  <div className="flex items-center gap-3">
                    <GitBranch className="h-4 w-4 text-muted-foreground shrink-0" />
                    <div>
                      <p className="text-sm font-medium font-mono">{conn.repo_full_name}</p>
                      <div className="flex items-center gap-2 mt-0.5">
                        <span className="text-[10px] border border-border/50 rounded px-1.5 py-0.5 text-muted-foreground">{conn.branch}</span>
                        {conn.status === 'active'
                          ? <span className="text-xs text-primary">active</span>
                          : <span className="text-xs text-muted-foreground">{conn.status}</span>}
                        {conn.last_push_sha && (
                          <span className="text-xs text-muted-foreground font-mono">{conn.last_push_sha.slice(0, 7)}</span>
                        )}
                      </div>
                    </div>
                  </div>
                  <Button
                    size="sm"
                    variant="ghost"
                    disabled={disconnecting === conn.id}
                    onClick={() => handleDisconnect(conn.id, conn.repo_full_name)}
                    className="cursor-pointer rounded-lg text-muted-foreground hover:text-destructive hover:bg-destructive/10 text-xs gap-1.5"
                  >
                    {disconnecting === conn.id ? <Loader2 className="h-3 w-3 animate-spin" /> : <X className="h-3 w-3" />}
                    Disconnect
                  </Button>
                </div>
              ))}
            </div>
          </div>
        )}
      </div>

      {/* Danger Zone */}
      <div>
        <p className="text-sm font-medium mb-1 text-destructive">Danger Zone</p>
        <div className="rounded-xl border border-destructive/30 bg-destructive/5 p-5">
          <div className="flex items-center justify-between">
            <div>
              <p className="text-sm font-medium">Delete Project</p>
              <p className="text-xs text-muted-foreground mt-0.5">
                Permanently delete all services, deployments, and associated data
              </p>
            </div>
            <Button
              size="sm"
              variant="outline"
              disabled={deleting}
              onClick={handleDeleteProject}
              className="cursor-pointer rounded-lg border-destructive/50 text-destructive hover:bg-destructive/10 gap-1.5 shrink-0"
            >
              {deleting ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : <Trash2 className="h-3.5 w-3.5" />}
              Delete
            </Button>
          </div>
        </div>
      </div>
    </div>
  );
}

/* ══════════════════════════════════════════════
   SHARED HELPERS
══════════════════════════════════════════════ */

function relTime(iso: string) {
  const diff = Date.now() - new Date(iso).getTime();
  const m = Math.floor(diff / 60000);
  if (m < 1) return "just now";
  if (m < 60) return `${m}m ago`;
  const h = Math.floor(m / 60);
  if (h < 24) return `${h}h ago`;
  return `${Math.floor(h / 24)}d ago`;
}

function DeployStatusIcon({ status }: { status: string }) {
  if (status === "live" || status === "success")
    return <CheckCircle2 className="h-4 w-4 text-primary shrink-0" />;
  if (status === "failed" || status === "error")
    return <XCircle className="h-4 w-4 text-destructive shrink-0" />;
  if (["building", "deploying", "provisioning", "pending"].includes(status))
    return <Loader2 className="h-4 w-4 text-amber-400 animate-spin shrink-0" />;
  return <Circle className="h-4 w-4 text-muted-foreground shrink-0" />;
}

function StepPill({ step }: { step: { step: string; status: string } }) {
  const label = step.step.replace(/_/g, " ");
  const isOk = step.status === "completed";
  const isFailed = step.status === "failed";
  const isRunning = step.status === "running";
  return (
    <div className={`flex items-center gap-1.5 rounded-lg px-2.5 py-1 text-[10px] font-medium border whitespace-nowrap ${
      isOk ? "border-primary/30 bg-primary/10 text-primary"
        : isFailed ? "border-destructive/30 bg-destructive/10 text-destructive"
        : isRunning ? "border-amber-500/30 bg-amber-500/10 text-amber-400"
        : "border-border/50 bg-accent/30 text-muted-foreground"
    }`}>
      {isOk ? <CheckCircle2 className="h-3 w-3" />
        : isFailed ? <XCircle className="h-3 w-3" />
        : isRunning ? <Loader2 className="h-3 w-3 animate-spin" />
        : <Circle className="h-3 w-3 opacity-40" />}
      {label}
    </div>
  );
}

function LogContent({ lines }: { lines: string[] }) {
  return (
    <div className="space-y-0.5">
      {lines.map((line, i) => {
        const lower = line.toLowerCase();
        const isError = lower.includes("error") || lower.includes("failed") || lower.includes("fatal");
        const isSuccess = lower.includes("success") || lower.includes("done") || line.startsWith("✓") || lower.includes("pushed") || lower.includes("deployed");
        const isStep = line.startsWith("---") || line.startsWith("===") || line.startsWith(">>>");
        return (
          <div key={i} className={`leading-snug ${
            isError ? "text-red-400"
              : isSuccess ? "text-primary"
              : isStep ? "text-amber-400 font-medium"
              : "text-muted-foreground"
          }`}>
            {line || " "}
          </div>
        );
      })}
    </div>
  );
}

function DomainStatusBadge({ status }: { status: string }) {
  const map: Record<string, string> = {
    active: "text-primary border-primary/20 bg-primary/5",
    pending: "text-amber-400 border-amber-400/20 bg-amber-400/5",
    expired: "text-red-400 border-red-400/20 bg-red-400/5",
    renewal_failed: "text-red-400 border-red-400/20 bg-red-400/5",
  };
  return (
    <span className={`text-[10px] px-1.5 py-0.5 rounded border capitalize ${map[status] ?? "text-muted-foreground border-border/50"}`}>
      {status.replace(/_/g, " ")}
    </span>
  );
}

function Empty({ icon, title, desc }: { icon: React.ReactNode; title: string; desc: string }) {
  return (
    <div className="flex flex-col items-center justify-center py-24 text-center gap-4">
      <div className="h-12 w-12 rounded-xl bg-primary/10 text-primary flex items-center justify-center">
        {icon}
      </div>
      <div>
        <p className="text-sm font-medium mb-1">{title}</p>
        <p className="text-xs text-muted-foreground max-w-xs mx-auto">{desc}</p>
      </div>
    </div>
  );
}

function SkeletonCards({ count }: { count: number }) {
  return (
    <div className="grid grid-cols-3 gap-4">
      {Array.from({ length: count }).map((_, i) => (
        <div key={i} className="h-24 rounded-xl bg-muted/50 animate-pulse" />
      ))}
    </div>
  );
}

function SkeletonList() {
  return (
    <div className="space-y-2">
      {[0, 1, 2].map((i) => (
        <div key={i} className="h-14 rounded-lg bg-muted/50 animate-pulse" />
      ))}
    </div>
  );
}
