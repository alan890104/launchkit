"use client";

import { useState, useEffect, useCallback } from "react";
import Link from "next/link";
import { useRouter } from "next/navigation";
import {
  Copy, Plus, Trash2, KeyRound, CheckCircle, Layers,
  Globe, AlertCircle, Loader2,
  FolderOpen, ChevronRight,
} from "lucide-react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Separator } from "@/components/ui/separator";
import {
  AlertDialog, AlertDialogAction, AlertDialogCancel,
  AlertDialogContent, AlertDialogDescription,
  AlertDialogFooter, AlertDialogHeader, AlertDialogTitle, AlertDialogTrigger,
} from "@/components/ui/alert-dialog";
import {
  Dialog, DialogContent, DialogHeader, DialogTitle, DialogFooter,
} from "@/components/ui/dialog";
import { Label } from "@/components/ui/label";
import { toast } from "sonner";
import { apiRequest, getToken, setToken } from "@/lib/api";
import type { Project, ApiKey } from "@/lib/types";

/* ────────────────────────────────── */

const REGIONS = [
  { value: "us-east4",        label: "US East (N. Virginia)" },
  { value: "us-central1",     label: "US Central (Iowa)" },
  { value: "us-west1",        label: "US West (Oregon)" },
  { value: "europe-west1",    label: "Europe West (Belgium)" },
  { value: "asia-northeast1", label: "Asia Northeast (Tokyo)" },
];

export default function DashboardPage() {
  const [token, setLocalToken] = useState<string | null>(null);
  const [showNewProject, setShowNewProject] = useState(false);
  const [refreshKey, setRefreshKey] = useState(0);
  const router = useRouter();

  useEffect(() => { setLocalToken(getToken()); }, []);

  useEffect(() => {
    if (!getToken()) router.push('/login');
  }, [router]);

  if (!token) return <ConnectScreen onConnect={(t) => { setToken(t); setLocalToken(t); }} />;

  return (
    <div className="px-6 py-8 max-w-6xl mx-auto space-y-12">
      {/* Page header */}
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-light tracking-[-0.02em]" style={{ fontFamily: "var(--font-ibm-plex-serif)" }}>
            Projects
          </h1>
          <p className="text-sm text-muted-foreground mt-1">
            Manage your deployments and API keys
          </p>
        </div>
        <Button
          variant="outline"
          onClick={() => setShowNewProject(true)}
          className="cursor-pointer rounded-lg gap-1.5 min-h-[40px] border-border/50 text-sm"
        >
          <Plus className="h-3.5 w-3.5" />
          New Project
        </Button>
      </div>

      <ProjectsTab key={refreshKey} />

      <Separator className="border-border/50" />

      <KeysSection />

      <NewProjectDialog
        open={showNewProject}
        onClose={() => setShowNewProject(false)}
        onCreated={(id) => {
          setShowNewProject(false);
          setRefreshKey((k) => k + 1);
          router.push(`/dashboard/projects/${id}`);
        }}
      />
    </div>
  );
}

/* ── New Project Dialog ── */
function NewProjectDialog({
  open,
  onClose,
  onCreated,
}: {
  open: boolean;
  onClose: () => void;
  onCreated: (id: string) => void;
}) {
  const [name, setName] = useState("");
  const [region, setRegion] = useState("us-east4");
  const [loading, setLoading] = useState(false);

  const handleCreate = async () => {
    const trimmed = name.trim();
    if (!trimmed) return;
    setLoading(true);
    try {
      const data = await apiRequest<{ project: { id: string } }>("/api/projects", {
        method: "POST",
        body: JSON.stringify({ name: trimmed, region }),
      });
      toast.success(`Project "${trimmed}" created`);
      setName("");
      setRegion("us-east4");
      onCreated(data.project.id);
    } catch (err) {
      toast.error(`Failed: ${err instanceof Error ? err.message : "unknown"}`);
    } finally {
      setLoading(false);
    }
  };

  return (
    <Dialog open={open} onOpenChange={(o) => { if (!o) onClose(); }}>
      <DialogContent className="rounded-2xl max-w-md">
        <DialogHeader>
          <DialogTitle>New Project</DialogTitle>
        </DialogHeader>
        <div className="space-y-4 py-2">
          <div className="space-y-1.5">
            <Label htmlFor="proj-name" className="text-sm">Project name</Label>
            <Input
              id="proj-name"
              placeholder="my-app"
              value={name}
              onChange={(e) => setName(e.target.value)}
              onKeyDown={(e) => e.key === "Enter" && handleCreate()}
              className="rounded-lg border-border/50 focus:border-primary/40"
              autoFocus
            />
          </div>
          <div className="space-y-1.5">
            <Label htmlFor="proj-region" className="text-sm">Region</Label>
            <select
              id="proj-region"
              value={region}
              onChange={(e) => setRegion(e.target.value)}
              className="w-full rounded-lg border border-border/50 bg-background px-3 py-2 text-sm focus:outline-none focus:border-primary/40"
            >
              {REGIONS.map((r) => (
                <option key={r.value} value={r.value}>{r.label}</option>
              ))}
            </select>
          </div>
        </div>
        <DialogFooter className="gap-2">
          <Button variant="ghost" onClick={onClose} className="cursor-pointer rounded-lg">
            Cancel
          </Button>
          <Button
            onClick={handleCreate}
            disabled={loading || !name.trim()}
            className="cursor-pointer rounded-lg bg-primary text-primary-foreground hover:opacity-90"
          >
            {loading ? <Loader2 className="h-4 w-4 animate-spin" /> : "Create"}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

/* ── Projects Tab ── */
function ProjectsTab() {
  const [projects, setProjects] = useState<Project[]>([]);
  const [loading, setLoading] = useState(true);

  const load = useCallback(async () => {
    setLoading(true);
    try {
      const data = await apiRequest<{ projects: Project[] }>("/api/projects");
      setProjects(data.projects ?? []);
    } catch {
      toast.error("Could not load projects");
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => { load(); }, [load]);

  if (loading) return (
    <div className="grid sm:grid-cols-2 lg:grid-cols-3 gap-4">
      {[0, 1, 2].map((i) => (
        <div key={i} className="h-36 rounded-xl bg-muted/50 animate-pulse" />
      ))}
    </div>
  );

  if (projects.length === 0) return <EmptyProjects />;

  return (
    <div className="grid sm:grid-cols-2 lg:grid-cols-3 gap-4">
      {projects.map((p, idx) => (
        <div
          key={p.id}
          className="fade-up"
          style={{ animationDelay: `${idx * 0.08}s` }}
        >
          <ProjectCard project={p} />
        </div>
      ))}
    </div>
  );
}

/* ── Project card ── */
function ProjectCard({ project }: { project: Project }) {
  const d = project.last_deploy;
  const relativeTime = (iso: string) => {
    const diff = Date.now() - new Date(iso).getTime();
    const m = Math.floor(diff / 60000);
    if (m < 1) return "just now";
    if (m < 60) return `${m}m ago`;
    const h = Math.floor(m / 60);
    if (h < 24) return `${h}h ago`;
    return `${Math.floor(h / 24)}d ago`;
  };

  const deployDuration = (d: Project["last_deploy"]) => {
    if (!d?.finished_at || !d.started_at) return null;
    const s = Math.round((new Date(d.finished_at).getTime() - new Date(d.started_at).getTime()) / 1000);
    return `${s}s`;
  };

  return (
    <Link href={`/dashboard/projects/${project.id}`} className="block group cursor-pointer">
      <div className="rounded-xl border border-border/50 bg-card p-5 hover:border-primary/30 transition-all duration-200 h-full">
        {/* Header */}
        <div className="flex items-start justify-between mb-3">
          <div className="flex items-center gap-2.5">
            <div className="h-8 w-8 rounded-lg bg-primary/10 text-primary flex items-center justify-center">
              <FolderOpen className="h-4 w-4" />
            </div>
            <div>
              <p className="font-medium text-sm text-foreground">{project.name}</p>
              <p className="text-[11px] text-muted-foreground mt-0.5">{project.region}</p>
            </div>
          </div>
          {d && <StatusBadge status={d.status} />}
        </div>

        {/* URL */}
        {d?.service_url ? (
          <div className="flex items-center gap-1.5 text-xs text-amber-400 truncate mb-2">
            <Globe className="h-3 w-3 shrink-0" />
            <span className="truncate">{d.service_url.replace(/^https?:\/\//, "")}</span>
          </div>
        ) : (
          <p className="text-xs text-muted-foreground mb-2">No deployments yet</p>
        )}

        {/* Footer */}
        <div className="mt-3 pt-3 border-t border-border/50 flex items-center justify-between">
          <span className="text-xs text-muted-foreground group-hover:text-primary transition-colors">
            {d ? `${relativeTime(d.started_at)}` : "View project"}
          </span>
          <ChevronRight className="h-3 w-3 text-muted-foreground group-hover:text-primary transition-colors" />
        </div>
      </div>
    </Link>
  );
}

/* ── Empty state ── */
function EmptyProjects() {
  return (
    <div className="flex flex-col items-center justify-center py-32 text-center gap-4 fade-up">
      <div className="h-16 w-16 rounded-2xl bg-primary/10 text-primary flex items-center justify-center">
        <Layers className="h-7 w-7" />
      </div>
      <div>
        <p className="font-medium text-lg mb-1">No projects yet</p>
        <p className="text-sm text-muted-foreground max-w-sm">
          Open Claude Desktop and say "deploy /path/to/myapp" — your first project will appear here.
        </p>
      </div>
      <div className="mt-2 rounded-xl border border-border/50 bg-card/60 p-4 font-mono text-xs text-muted-foreground max-w-sm text-left">
        <span className="text-primary">claude:</span> "Deploy /Users/you/myapp"
      </div>
    </div>
  );
}

/* ── Status badge ── */
function StatusBadge({ status }: { status: string }) {
  if (status === "live") return (
    <div className="flex items-center gap-1.5 text-xs font-medium text-primary">
      <span className="relative flex h-2 w-2">
        <span className="animate-ping absolute inline-flex h-full w-full rounded-full bg-primary opacity-75" />
        <span className="relative inline-flex rounded-full h-2 w-2 bg-primary" />
      </span>
      Live
    </div>
  );
  if (status === "failed") return (
    <div className="flex items-center gap-1.5 text-xs font-medium text-red-400">
      <AlertCircle className="h-3.5 w-3.5" />
      Failed
    </div>
  );
  if (["building", "deploying", "provisioning"].includes(status)) return (
    <div className="flex items-center gap-1.5 text-xs font-medium text-amber-400">
      <Loader2 className="h-3 w-3 animate-spin" />
      {status.charAt(0).toUpperCase() + status.slice(1)}
    </div>
  );
  return (
    <span className="text-xs text-muted-foreground capitalize">{status}</span>
  );
}

/* ── API Keys Section ── */
function KeysSection() {
  const [keys, setKeys] = useState<ApiKey[]>([]);
  const [newKeyName, setNewKeyName] = useState("");
  const [newKeyValue, setNewKeyValue] = useState<string | null>(null);
  const [loading, setLoading] = useState(false);

  const fetchKeys = useCallback(async () => {
    try {
      const data = await apiRequest<{ keys: ApiKey[] }>("/auth/keys");
      setKeys(data.keys ?? []);
    } catch { /* silent */ }
  }, []);

  useEffect(() => { fetchKeys(); }, [fetchKeys]);

  const handleCreate = async () => {
    if (!newKeyName.trim()) return;
    setLoading(true);
    try {
      const data = await apiRequest<{ key: string }>("/auth/keys", {
        method: "POST",
        body: JSON.stringify({ name: newKeyName.trim() }),
      });
      setNewKeyValue(data.key);
      setNewKeyName("");
      await fetchKeys();
      toast.success("API key created");
    } catch (err) {
      toast.error(`Failed: ${err instanceof Error ? err.message : "unknown"}`);
    } finally {
      setLoading(false);
    }
  };

  const handleRevoke = async (id: string) => {
    try {
      await apiRequest(`/auth/keys/${id}`, { method: "DELETE" });
      await fetchKeys();
      toast.success("Revoked");
    } catch (err) {
      toast.error(`Failed: ${err instanceof Error ? err.message : "unknown"}`);
    }
  };

  const copy = (text: string) => {
    navigator.clipboard.writeText(text);
    toast.success("Copied");
  };

  const API_URL = process.env.NEXT_PUBLIC_API_URL || "http://localhost:8080";
  const configSnippet = JSON.stringify({
    mcpServers: {
      launchkit: {
        url: `${API_URL}/mcp`,
        headers: {
          Authorization: `Bearer ${newKeyValue || keys[0]?.prefix + "..." || "lk_your_key_here"}`,
        },
      },
    },
  }, null, 2);

  return (
    <div className="space-y-8">
      {/* Header */}
      <div>
        <h2 className="text-lg font-medium" style={{ fontFamily: "var(--font-ibm-plex-serif)" }}>API Keys</h2>
        <p className="text-sm text-muted-foreground mt-1">
          Manage your API keys for connecting Claude Desktop
        </p>
      </div>

      {/* New key banner */}
      {newKeyValue && (
        <div className="rounded-xl border border-primary/40 bg-primary/5 p-5 fade-up">
          <p className="text-sm font-medium text-primary mb-3">
            New API key created — save it now, it won&apos;t be shown again:
          </p>
          <div className="flex items-center gap-2 rounded-lg bg-background border border-border/50 p-3">
            <code className="flex-1 break-all font-mono text-sm text-primary text-xs">{newKeyValue}</code>
            <Button variant="ghost" size="sm" className="cursor-pointer shrink-0" onClick={() => copy(newKeyValue)}>
              <Copy className="h-3.5 w-3.5" />
            </Button>
          </div>
          <Button variant="ghost" size="sm" className="mt-3 cursor-pointer text-muted-foreground text-xs" onClick={() => setNewKeyValue(null)}>
            I&apos;ve saved it <CheckCircle className="h-3 w-3 ml-1" />
          </Button>
        </div>
      )}

      {/* Create form */}
      <div className="rounded-xl border border-border/50 bg-card p-5">
        <p className="text-sm font-medium mb-3">Create new API key</p>
        <form onSubmit={(e) => { e.preventDefault(); handleCreate(); }} className="flex gap-2">
          <Input
            placeholder="Key name, e.g. Claude Desktop"
            value={newKeyName}
            onChange={(e) => setNewKeyName(e.target.value)}
            className="rounded-lg border-border/50 focus:border-primary/40"
          />
          <Button type="submit" disabled={loading || !newKeyName.trim()} className="cursor-pointer shrink-0 rounded-lg gap-1.5 min-h-[44px] bg-primary text-primary-foreground hover:opacity-90">
            <Plus className="h-4 w-4" />
            Create
          </Button>
        </form>
      </div>

      {/* Keys list */}
      <div className="rounded-xl border border-border/50 bg-card overflow-hidden">
        <div className="px-5 py-3.5 border-b border-border/50">
          <p className="text-sm font-medium">Your API Keys</p>
        </div>
        {keys.length === 0 ? (
          <div className="py-12 text-center text-sm text-muted-foreground">
            <KeyRound className="h-8 w-8 mx-auto mb-3 opacity-30" />
            No API keys yet
          </div>
        ) : (
          <div className="divide-y divide-border/40">
            {keys.map((k) => (
              <div key={k.id} className="flex items-center justify-between px-5 py-3.5 hover:bg-card/80 transition-colors">
                <div>
                  <p className="text-sm font-medium text-foreground">{k.name}</p>
                  <div className="mt-0.5 flex items-center flex-wrap gap-2 text-xs text-muted-foreground">
                    <code className="bg-muted/50 border border-border/50 rounded px-1.5 py-0.5 font-mono">{k.prefix}...</code>
                    <span>Created {new Date(k.created_at).toLocaleDateString("en-US", { month: "short", day: "numeric", year: "numeric" })}</span>
                    {k.last_used_at && (
                      <span>· Last used {new Date(k.last_used_at).toLocaleDateString("en-US", { month: "short", day: "numeric", year: "numeric" })}</span>
                    )}
                  </div>
                </div>
                <AlertDialog>
                  <AlertDialogTrigger className="cursor-pointer ml-3 h-11 w-11 inline-flex items-center justify-center rounded-lg text-muted-foreground hover:text-destructive hover:bg-destructive/10 transition-colors">
                    <Trash2 className="h-3.5 w-3.5" />
                  </AlertDialogTrigger>
                  <AlertDialogContent className="rounded-2xl">
                    <AlertDialogHeader>
                      <AlertDialogTitle>Revoke API key?</AlertDialogTitle>
                      <AlertDialogDescription>Claude Desktop connections using this key will be immediately terminated. This action cannot be undone.</AlertDialogDescription>
                    </AlertDialogHeader>
                    <AlertDialogFooter>
                      <AlertDialogCancel className="cursor-pointer rounded-lg">Cancel</AlertDialogCancel>
                      <AlertDialogAction onClick={() => handleRevoke(k.id)} className="cursor-pointer rounded-lg bg-destructive text-destructive-foreground hover:bg-destructive/90">
                        Revoke
                      </AlertDialogAction>
                    </AlertDialogFooter>
                  </AlertDialogContent>
                </AlertDialog>
              </div>
            ))}
          </div>
        )}
      </div>

      {/* Claude Desktop config */}
      <div className="rounded-xl border border-border/50 bg-card p-5">
        <p className="text-sm font-medium mb-1">Connect Claude Desktop</p>
        <p className="text-xs text-muted-foreground font-mono mb-4">~/Library/Application Support/Claude/claude_desktop_config.json</p>
        <div className="relative rounded-xl bg-background border border-border/50 p-4">
          <pre className="overflow-x-auto font-mono text-xs text-muted-foreground leading-relaxed">{configSnippet}</pre>
          <Button variant="ghost" size="sm" className="absolute right-2 top-2 cursor-pointer h-7 text-xs" onClick={() => copy(configSnippet)}>
            <Copy className="mr-1 h-3 w-3" />
            Copy
          </Button>
        </div>
        <p className="mt-4 text-sm text-muted-foreground">After adding this, restart Claude Desktop and say:</p>
        <p className="mt-1 text-sm font-medium text-foreground">"Deploy /Users/you/myapp"</p>
      </div>
    </div>
  );
}

/* ── Connect screen ── */
function ConnectScreen({ onConnect }: { onConnect: (token: string) => void }) {
  const [input, setInput] = useState("");
  return (
    <div className="flex flex-col min-h-screen bg-background relative overflow-hidden">
      {/* Ambient blobs */}
      <div className="pointer-events-none fixed inset-0 overflow-hidden">
        <div className="animate-blob absolute -top-40 left-1/4 h-96 w-96 rounded-full bg-primary/8 blur-3xl" />
      </div>

      <div className="flex-1 flex items-center justify-center px-6 relative z-10">
        <div className="w-full max-w-sm">
          <div className="text-center mb-8 fade-up">
            <div className="flex items-center justify-center gap-2.5 mb-4">
              <div className="h-12 w-12 rounded-xl bg-primary/10 text-primary flex items-center justify-center">
                <KeyRound className="h-5 w-5" />
              </div>
            </div>
            <h1 className="text-lg font-medium mb-2 text-foreground">Connect to LaunchKit</h1>
            <p className="text-sm text-muted-foreground">Enter your API key or dev token</p>
          </div>
          <div className="flex gap-2 fade-up fade-up-delay-1">
            <Input
              placeholder="lk_xxx..."
              value={input}
              onChange={(e) => setInput(e.target.value)}
              onKeyDown={(e) => e.key === "Enter" && input.trim() && onConnect(input.trim())}
              className="font-mono rounded-lg border-border/50 focus:border-primary/40"
            />
            <Button onClick={() => input.trim() && onConnect(input.trim())} className="cursor-pointer rounded-lg shrink-0 min-h-[44px] bg-primary text-primary-foreground hover:opacity-90">
              Connect
            </Button>
          </div>
          <Separator className="my-6 border-border/50" />
          <div className="text-sm text-muted-foreground fade-up fade-up-delay-2">
            <p className="font-medium text-foreground mb-2 text-xs uppercase tracking-wider">Local development</p>
            <code className="block rounded-xl bg-card border border-border/50 p-3 font-mono text-xs">
              LAUNCHKIT_DEV_KEY=mykey go run ./cmd/api
            </code>
            <p className="mt-2 text-xs">Then enter <code className="text-primary">mykey</code> to connect.</p>
          </div>
        </div>
      </div>
    </div>
  );
}
