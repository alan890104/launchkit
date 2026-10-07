"use client";

import { useCallback, useEffect, useState } from "react";
import { Loader2 } from "lucide-react";
import { apiRequest } from "@/lib/api";
import type { BillingSummary } from "@/lib/types";

const RESOURCE_LABELS: Record<string, string> = {
  compute: "Compute (Cloud Run)",
  database: "Neon DB",
  cache: "Upstash Redis",
  storage: "GCS Storage",
  artifact_registry: "Artifact Registry",
  egress: "Egress",
};

export default function BillingPage() {
  const [summary, setSummary] = useState<BillingSummary | null>(null);
  const [loading, setLoading] = useState(true);

  const load = useCallback(async () => {
    setLoading(true);
    try {
      const data = await apiRequest<BillingSummary>("/api/billing/summary");
      setSummary(data);
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    load();
  }, [load]);

  return (
    <div className="px-6 py-8 max-w-6xl mx-auto space-y-8">
      {/* Page header */}
      <div>
        <h1 className="text-2xl font-light tracking-[-0.02em]" style={{ fontFamily: "var(--font-ibm-plex-serif)" }}>
          Billing
        </h1>
        <p className="text-sm text-muted-foreground mt-1">
          Track your usage and credit balance
        </p>
      </div>

      {loading ? (
        <LoadingState />
      ) : summary ? (
        <BillingContent summary={summary} />
      ) : (
        <div className="rounded-xl border border-border/50 bg-card p-6 text-sm text-muted-foreground">
          Unable to load billing information
        </div>
      )}
    </div>
  );
}

function BillingContent({ summary }: { summary: BillingSummary }) {
  const used = Math.max(0, summary.monthly_credit - summary.credit_remaining);
  const ratio = summary.monthly_credit > 0 ? Math.min(1, used / summary.monthly_credit) : 0;
  const maxCost = Math.max(...summary.usage.map((item) => item.total_cost), 0);

  return (
    <div className="space-y-6">
      {/* Metric cards */}
      <div className="grid gap-4 md:grid-cols-2">
        <MetricCard
          label="Credit Balance"
          value={summary.credit_remaining}
          prefix="$"
          suffix=""
          progressRatio={ratio}
          progressLabel={`Used $${used.toFixed(2)} / $${summary.monthly_credit.toFixed(2)}`}
          badge={summary.plan}
        />
        <div className="rounded-xl border border-border/50 bg-card p-6">
          <p className="text-xs text-muted-foreground uppercase tracking-wide">Period Spend</p>
          <CountUpNumber value={summary.total_spend} prefix="$" className="mt-3 text-4xl font-light text-foreground tabular-nums" />
          <p className="mt-3 text-xs text-muted-foreground">
            {formatDate(summary.period_start)} — {formatDate(summary.period_end)}
          </p>
          <p className="mt-1 text-xs text-muted-foreground/60 capitalize">
            Status: {summary.status}
          </p>
        </div>
      </div>

      {/* Usage table */}
      <div className="rounded-xl border border-border/50 bg-card overflow-hidden">
        <div className="px-5 py-3.5 border-b border-border/50">
          <p className="text-sm font-medium">Usage Breakdown</p>
        </div>

        {summary.usage.length === 0 ? (
          <div className="px-5 py-12 text-center text-sm text-muted-foreground">
            No usage this period
          </div>
        ) : (
          <div className="overflow-x-auto">
            <table className="w-full min-w-[640px] text-sm">
              <thead className="text-left text-muted-foreground">
                <tr className="border-b border-border/50">
                  <th className="px-5 py-3 font-medium">Resource</th>
                  <th className="px-5 py-3 font-medium">Cost</th>
                  <th className="px-5 py-3 font-medium">Usage</th>
                  <th className="px-5 py-3 font-medium">Share</th>
                </tr>
              </thead>
              <tbody>
                {summary.usage.map((item, idx) => {
                  const width = maxCost > 0 ? (item.total_cost / maxCost) * 100 : 0;
                  return (
                    <tr key={`${item.resource_type}-${item.unit}`} className="border-l-2 border-primary/50">
                      <td className="px-5 py-4 text-foreground">
                        {RESOURCE_LABELS[item.resource_type] ?? item.resource_type}
                      </td>
                      <td className="px-5 py-4 font-mono tabular-nums text-foreground">
                        ${item.total_cost.toFixed(4)}
                      </td>
                      <td className="px-5 py-4 text-muted-foreground">
                        {formatQuantity(item.total_qty)} {item.unit}
                      </td>
                      <td className="px-5 py-4">
                        <div className="h-1.5 rounded-full bg-muted overflow-hidden">
                          <div className="h-full rounded-full bg-primary transition-all duration-1000" style={{ width: `${width}%` }} />
                        </div>
                      </td>
                    </tr>
                  );
                })}
              </tbody>
            </table>
          </div>
        )}
      </div>
    </div>
  );
}

function MetricCard({
  label, value, prefix = "", suffix = "", progressRatio, progressLabel, badge,
}: {
  label: string; value: number; prefix?: string; suffix?: string;
  progressRatio: number; progressLabel: string; badge?: string;
}) {
  return (
    <div className="rounded-xl border border-border/50 bg-card p-6">
      <p className="text-xs text-muted-foreground uppercase tracking-wide">{label}</p>
      <div className="mt-3">
        <span className="text-4xl font-light text-foreground tabular-nums">
          {prefix}{typeof value === "number" ? value.toFixed(2) : value}{suffix}
        </span>
      </div>
      <div className="mt-4">
        <div className="h-1 rounded-full bg-muted overflow-hidden">
          <div
            className="h-full rounded-full bg-primary transition-all duration-1000"
            style={{ width: `${progressRatio * 100}%` }}
          />
        </div>
        <p className="mt-2 text-xs text-muted-foreground">{progressLabel}</p>
      </div>
      {badge && (
        <span className="inline-block mt-3 rounded-full border border-primary/20 bg-primary/5 px-2.5 py-0.5 text-xs font-medium text-primary">
          {badge}
        </span>
      )}
    </div>
  );
}

/* Count-up number animation */
function CountUpNumber({ value, prefix = "", className }: { value: number; prefix?: string; className?: string }) {
  const [display, setDisplay] = useState(0);

  useEffect(() => {
    const target = value;
    const duration = 1000;
    const start = performance.now();
    const startVal = 0;

    const animate = (now: number) => {
      const elapsed = now - start;
      const progress = Math.min(elapsed / duration, 1);
      const eased = 1 - Math.pow(1 - progress, 3);
      setDisplay(startVal + (target - startVal) * eased);
      if (progress < 1) requestAnimationFrame(animate);
    };
    requestAnimationFrame(animate);
  }, [value]);

  return (
    <div className={className}>
      {prefix}{display.toFixed(2)}
    </div>
  );
}

function LoadingState() {
  return (
    <div className="space-y-6">
      <div className="grid gap-4 md:grid-cols-2">
        {[0, 1].map((i) => (
          <div key={i} className="rounded-xl border border-border/50 bg-card p-6">
            <div className="h-3 w-24 rounded bg-muted/50 animate-pulse" />
            <div className="mt-4 h-8 w-32 rounded bg-muted/50 animate-pulse" />
            <div className="mt-4 h-1 w-full rounded bg-muted/50 animate-pulse" />
            <div className="mt-3 h-3 w-40 rounded bg-muted/50 animate-pulse" />
          </div>
        ))}
      </div>
      <div className="rounded-xl border border-border/50 bg-card overflow-hidden">
        <div className="px-5 py-3.5 border-b border-border/50">
          <div className="h-4 w-32 rounded bg-muted/50 animate-pulse" />
        </div>
        <div className="space-y-3 p-5">
          {[0, 1, 2].map((i) => (
            <div key={i} className="h-12 rounded-lg bg-muted/30 animate-pulse" />
          ))}
        </div>
      </div>
      <div className="flex items-center gap-2 text-sm text-muted-foreground">
        <Loader2 className="h-4 w-4 animate-spin" />
        Loading billing summary...
      </div>
    </div>
  );
}

function formatDate(value: string) {
  return new Date(value).toLocaleDateString("en-US", {
    year: "numeric",
    month: "short",
    day: "numeric",
  });
}

function formatQuantity(value: number) {
  if (Number.isInteger(value)) return value.toString();
  return value.toLocaleString("en-US", { maximumFractionDigits: 2 });
}
