const API_BASE = process.env.NEXT_PUBLIC_API_URL || "http://localhost:8080";

export async function apiRequest<T>(
  path: string,
  options: RequestInit = {}
): Promise<T> {
  const token =
    typeof window !== "undefined" ? localStorage.getItem("lk_token") : null;

  const res = await fetch(`${API_BASE}${path}`, {
    ...options,
    headers: {
      "Content-Type": "application/json",
      ...(token ? { Authorization: `Bearer ${token}` } : {}),
      ...options.headers,
    },
  });

  if (!res.ok) {
    const body = await res.json().catch(() => ({ error: res.statusText }));
    throw new Error(body.error || res.statusText);
  }

  return res.json();
}

export function setToken(token: string) {
  localStorage.setItem("lk_token", token);
}

export function getToken(): string | null {
  if (typeof window === "undefined") return null;
  return localStorage.getItem("lk_token");
}

export function clearToken() {
  localStorage.removeItem("lk_token");
}

// Returns a function that opens an SSE-style ReadableStream for live log streaming.
// Uses fetch (not EventSource) so we can send the Authorization header.
export function streamDeployLogs(
  deploymentId: string,
  onLine: (line: string) => void,
  onStatus: (status: string) => void,
  onDone: (status: string) => void
): () => void {
  const controller = new AbortController();
  const token = getToken();
  const url = `${API_BASE}/api/deployments/${deploymentId}/logs/stream`;

  (async () => {
    try {
      const res = await fetch(url, {
        headers: {
          Authorization: token ? `Bearer ${token}` : "",
        },
        signal: controller.signal,
      });
      if (!res.ok || !res.body) return;

      const reader = res.body.getReader();
      const decoder = new TextDecoder();
      let buf = "";

      while (true) {
        const { done, value } = await reader.read();
        if (done) break;
        buf += decoder.decode(value, { stream: true });
        const parts = buf.split("\n\n");
        buf = parts.pop() ?? "";
        for (const part of parts) {
          const dataLine = part.split("\n").find((l) => l.startsWith("data: "));
          if (!dataLine) continue;
          try {
            const ev = JSON.parse(dataLine.slice(6));
            if (ev.type === "log" && ev.line != null) onLine(ev.line);
            else if (ev.type === "status" && ev.status) onStatus(ev.status);
            else if (ev.type === "done") onDone(ev.status ?? "");
          } catch { /* ignore parse errors */ }
        }
      }
    } catch { /* aborted or network error */ }
  })();

  return () => controller.abort();
}
