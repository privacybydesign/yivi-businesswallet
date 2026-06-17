const API_BASE_URL = import.meta.env.VITE_API_BASE_URL ?? "";

export type HealthResponse = { status: string };

export async function getHealth(): Promise<HealthResponse> {
    const res = await fetch(`${API_BASE_URL}/healthz`);
    if (!res.ok) {
        throw new Error(`Health check failed: ${res.status}`);
    }
    return res.json() as Promise<HealthResponse>;
}
