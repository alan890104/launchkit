"use client";

import { useState, useEffect, useCallback } from "react";
import Link from "next/link";
import { usePathname, useRouter } from "next/navigation";
import { FolderOpen, CreditCard, LogOut, Loader2, BookOpen, Zap } from "lucide-react";
import { clearToken, getToken, apiRequest } from "@/lib/api";
import type { ApiKey } from "@/lib/types";

export default function DashboardLayout({
  children,
}: {
  children: React.ReactNode;
}) {
  const [userDisplayName, setUserDisplayName] = useState<string | null>(null);
  const [userInitial, setUserInitial] = useState("U");
  const pathname = usePathname();
  const router = useRouter();

  useEffect(() => {
    if (!getToken()) {
      router.push("/login");
      return;
    }
    const fetchInfo = async () => {
      try {
        const data = await apiRequest<{ keys: ApiKey[] }>("/auth/keys");
        if (data.keys?.length > 0) {
          const name = data.keys[0].name;
          setUserDisplayName(name);
          setUserInitial(name.charAt(0).toUpperCase());
        }
      } catch {
        // silent
      }
    };
    fetchInfo();
  }, [router]);

  const handleLogout = useCallback(() => {
    clearToken();
    router.push("/login");
  }, [router]);

  const navItems = [
    { href: "/dashboard", icon: FolderOpen, label: "Projects", active: pathname === "/dashboard" || pathname.startsWith("/dashboard/projects") },
    { href: "/dashboard/billing", icon: CreditCard, label: "Billing", active: pathname.startsWith("/dashboard/billing") },
    { href: "https://docs.launchkit.dev", icon: BookOpen, label: "Docs", active: false, external: true },
  ];

  return (
    <div className="flex min-h-screen bg-background">
      {/* Sidebar */}
      <aside className="fixed inset-y-0 left-0 z-30 w-56 flex flex-col border-r border-border/50 bg-sidebar">
        {/* Logo */}
        <div className="flex items-center gap-2.5 px-4 h-14">
          <div className="h-8 w-8 rounded-lg bg-primary flex items-center justify-center">
            <Zap className="h-4 w-4 text-primary-foreground" />
          </div>
          <Link href="/" className="text-sm font-medium tracking-tight text-foreground hover:text-primary transition-colors" style={{ fontFamily: "var(--font-ibm-plex-serif)", fontStyle: "italic" }}>
            LaunchKit
          </Link>
        </div>

        {/* Navigation */}
        <nav className="flex-1 px-3 py-4 space-y-1">
          {navItems.map((item) => {
            const Comp = item.external ? "a" : Link;
            const props = item.external ? { href: item.href, target: "_blank", rel: "noopener noreferrer" } : { href: item.href };
            return (
              <Comp
                key={item.href}
                {...props}
                className={`flex items-center gap-3 rounded-lg px-3 py-2 text-sm font-medium transition-colors min-h-[40px] ${
                  item.active
                    ? "bg-accent text-foreground"
                    : "text-muted-foreground hover:text-foreground hover:bg-accent/50"
                }`}
              >
                <item.icon className="h-4 w-4 shrink-0" />
                {item.label}
                {item.external && <item.icon className="h-3 w-3 ml-auto opacity-40" />}
              </Comp>
            );
          })}
        </nav>

        {/* User section */}
        <div className="border-t border-border/50 px-3 py-3 mt-auto">
          <div className="flex items-center justify-between gap-2">
            <div className="flex items-center gap-2.5 min-w-0">
              <div className="h-8 w-8 rounded-full bg-primary/10 flex items-center justify-center shrink-0">
                <span className="text-xs font-semibold text-primary">
                  {userInitial}
                </span>
              </div>
              <span className="text-sm text-foreground truncate">
                {userDisplayName ?? "User"}
              </span>
            </div>
            <button
              onClick={handleLogout}
              className="cursor-pointer h-8 w-8 flex items-center justify-center rounded-lg text-muted-foreground hover:text-destructive hover:bg-destructive/10 transition-colors shrink-0"
              title="Logout"
            >
              <LogOut className="h-4 w-4" />
            </button>
          </div>
        </div>
      </aside>

      {/* Main content */}
      <main className="flex-1 ml-56 min-h-screen">
        {children}
      </main>
    </div>
  );
}
