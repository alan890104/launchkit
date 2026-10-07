"use client";

import { useState, useEffect } from "react";
import { useRouter } from "next/navigation";
import { getToken, setToken } from "@/lib/api";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { toast } from "sonner";
import { Loader2, KeyRound, Zap } from "lucide-react";
import Link from "next/link";

const API_BASE = process.env.NEXT_PUBLIC_API_URL || "http://localhost:8080";

export default function LoginPage() {
  const router = useRouter();
  const [apiKey, setApiKey] = useState("");
  const [googleLoading, setGoogleLoading] = useState(false);

  useEffect(() => {
    if (getToken()) {
      router.push("/dashboard");
    }
  }, [router]);

  async function signInWithGoogle() {
    setGoogleLoading(true);
    try {
      const { initializeApp, getApps } = await import("firebase/app");
      const { getAuth, GoogleAuthProvider, signInWithPopup } = await import("firebase/auth");

      const firebaseConfig = {
        apiKey: process.env.NEXT_PUBLIC_FIREBASE_API_KEY,
        authDomain: process.env.NEXT_PUBLIC_FIREBASE_AUTH_DOMAIN,
        projectId: process.env.NEXT_PUBLIC_FIREBASE_PROJECT_ID,
      };

      if (!firebaseConfig.apiKey || !firebaseConfig.projectId) {
        throw new Error("Firebase environment variables are not set");
      }

      const app = getApps().length === 0 ? initializeApp(firebaseConfig) : getApps()[0];
      const auth = getAuth(app);
      const provider = new GoogleAuthProvider();

      const result = await signInWithPopup(auth, provider);
      const idToken = await result.user.getIdToken();

      const res = await fetch(`${API_BASE}/auth/session`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ id_token: idToken }),
      });

      if (!res.ok) {
        const body = await res.json().catch(() => ({ error: res.statusText }));
        throw new Error(body.error || "Sign-in failed. Please try again.");
      }

      const data = await res.json();
      if (data.token) {
        setToken(data.token);
        router.push("/dashboard");
      } else {
        throw new Error("The server did not return a token");
      }
    } catch (err) {
      const message = err instanceof Error ? err.message : "Google sign-in failed";
      toast.error(message);
    } finally {
      setGoogleLoading(false);
    }
  }

  function handleApiKeySubmit(e: React.FormEvent) {
    e.preventDefault();
    const trimmed = apiKey.trim();
    if (!trimmed) {
      toast.error("Enter an API key");
      return;
    }
    setToken(trimmed);
    router.push("/dashboard");
  }

  return (
    <div className="flex flex-col min-h-screen bg-background text-foreground relative overflow-hidden">
      {/* Ambient blobs */}
      <div className="pointer-events-none fixed inset-0 overflow-hidden">
        <div className="animate-blob absolute -top-40 left-1/4 h-96 w-96 rounded-full bg-primary/8 blur-3xl" />
        <div className="animate-blob absolute bottom-0 right-1/4 h-80 w-80 rounded-full bg-primary/5 blur-3xl [animation-delay:10s]" />
      </div>

      {/* Top bar */}
      <div className="fixed top-0 inset-x-0 z-50 flex items-center justify-between px-6 h-14">
        <Link href="/" className="flex items-center gap-2 text-sm font-medium text-foreground hover:text-primary transition-colors" style={{ fontFamily: "var(--font-ibm-plex-serif)", fontStyle: "italic" }}>
          <div className="h-5 w-5 rounded-md bg-primary flex items-center justify-center">
            <Zap className="h-3 w-3 text-primary-foreground" />
          </div>
          LaunchKit
        </Link>
      </div>

      {/* Centered card */}
      <div className="flex flex-1 items-center justify-center px-4 relative z-10">
        <div className="w-full max-w-sm rounded-2xl border border-border/50 bg-card/80 p-8 backdrop-blur shadow-2xl shadow-black/40">
          {/* Logo */}
          <div className="flex items-center justify-center gap-2.5 mb-6">
            <div className="h-10 w-10 rounded-xl bg-primary/10 flex items-center justify-center">
              <Zap className="h-5 w-5 text-primary" />
            </div>
            <h1 className="text-xl font-medium" style={{ fontFamily: "var(--font-ibm-plex-serif)", fontStyle: "italic" }}>
              LaunchKit
            </h1>
          </div>

          <h2 className="text-center text-2xl font-light mb-1" style={{ fontFamily: "var(--font-ibm-plex-serif)" }}>
            Welcome back.
          </h2>
          <p className="text-center text-sm text-muted-foreground mb-8">
            Sign in to your account
          </p>

          {/* Google button */}
          <Button
            onClick={signInWithGoogle}
            disabled={googleLoading}
            variant="outline"
            className="w-full rounded-xl gap-2 font-medium min-h-[48px] border-border/50 hover:border-primary/30"
          >
            {googleLoading ? (
              <Loader2 className="h-4 w-4 animate-spin" />
            ) : (
              <svg className="h-4 w-4" viewBox="0 0 24 24">
                <path d="M22.56 12.25c0-.78-.07-1.53-.2-2.25H12v4.26h5.92a5.06 5.06 0 0 1-2.2 3.32v2.77h3.57c2.08-1.92 3.28-4.74 3.28-8.1z" fill="#4285F4"/>
                <path d="M12 23c2.97 0 5.46-.98 7.28-2.66l-3.57-2.77c-.98.66-2.23 1.06-3.71 1.06-2.86 0-5.29-1.93-6.16-4.53H2.18v2.84C3.99 20.53 7.7 23 12 23z" fill="#34A853"/>
                <path d="M5.84 14.09c-.22-.66-.35-1.36-.35-2.09s.13-1.43.35-2.09V7.07H2.18C1.43 8.55 1 10.22 1 12s.43 3.45 1.18 4.93l2.85-2.22.81-.62z" fill="#FBBC05"/>
                <path d="M12 5.38c1.62 0 3.06.56 4.21 1.64l3.15-3.15C17.45 2.09 14.97 1 12 1 7.7 1 3.99 3.47 2.18 7.07l3.66 2.84c.87-2.6 3.3-4.53 6.16-4.53z" fill="#EA4335"/>
              </svg>
            )}
            {googleLoading ? "Signing in..." : "Continue with Google"}
          </Button>

          {/* Divider */}
          <div className="relative my-6">
            <hr className="border-border/50" />
            <div className="absolute inset-0 flex items-center justify-center">
              <span className="bg-card px-3 text-[10px] text-muted-foreground uppercase tracking-wider">or continue with API key</span>
            </div>
          </div>

          {/* API Key form */}
          <form onSubmit={handleApiKeySubmit} className="space-y-3">
            <div className="flex gap-2">
              <Input
                value={apiKey}
                onChange={(e) => setApiKey(e.target.value)}
                placeholder="lk_xxx..."
                type="text"
                className="flex-1 rounded-xl min-h-[48px] border-border/50 focus:border-primary/40 font-mono text-sm"
              />
              <Button type="submit" variant="default" className="rounded-xl gap-1.5 font-medium min-h-[48px] px-4 bg-primary text-primary-foreground hover:opacity-90">
                <KeyRound className="h-3.5 w-3.5" />
                Connect
              </Button>
            </div>
          </form>
        </div>
      </div>

      {/* Bottom note */}
      <p className="text-center text-xs text-muted-foreground pb-12 px-4 max-w-sm mx-auto relative z-10">
        Don&apos;t have an account? Connect via Claude Desktop to get started.
      </p>
    </div>
  );
}
