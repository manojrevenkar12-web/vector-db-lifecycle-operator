import type { Actor, ClusterDetail, ClusterSummary, Patch, Preview } from "./types";

const base = (import.meta.env.VITE_API_URL as string | undefined) ?? "";

export class ApiError extends Error {
  constructor(readonly status: number, message: string) {
    super(message);
  }
}

async function call<T>(path: string, init?: RequestInit): Promise<T> {
  let res: Response;
  try {
    res = await fetch(base + path, { ...init, headers: { "Content-Type": "application/json", ...(init?.headers ?? {}) } });
  } catch {
    throw new ApiError(0, "The change API is not reachable. Start it with `go run ./cmd/api` and reload.");
  }
  const text = await res.text();
  const body = text ? (JSON.parse(text) as unknown) : null;
  if (!res.ok) {
    const msg = body && typeof body === "object" && "error" in body ? String((body as { error: unknown }).error) : res.statusText;
    throw new ApiError(res.status, msg);
  }
  return body as T;
}

export const api = {
  clusters: () => call<ClusterSummary[]>("/v1/clusters"),
  cluster: (id: string) => call<ClusterDetail>(`/v1/clusters/${encodeURIComponent(id)}`),
  preview: (id: string, patch: Patch) => call<Preview>(`/v1/clusters/${encodeURIComponent(id)}/changes/preview`, { method: "POST", body: JSON.stringify(patch) }),
  approve: (pid: string, actor: Actor) => call<Preview>(`/v1/changes/${pid}/approve`, { method: "POST", body: JSON.stringify({ actor }) }),
  apply: (pid: string, actor: Actor, key: string) =>
    call<{ generation: number }>(`/v1/changes/${pid}/apply`, { method: "POST", body: JSON.stringify({ actor }), headers: { "Idempotency-Key": key } }),
};
