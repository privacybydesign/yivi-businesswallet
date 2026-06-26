import { exec as execCallback, spawn } from "child_process";
import { promisify } from "node:util";
import { join } from "node:path";
import { copyFileSync, existsSync } from "node:fs";

const execAsync = promisify(execCallback)

const reset = process.argv.includes('--reset')

const REPO_ROOT_PATH = join(__dirname, '..');
const BACKEND_PATH = join(REPO_ROOT_PATH, 'backend');
const FRONTEND_PATH = join(REPO_ROOT_PATH, 'frontend');
const TIMING_THRESHOLD_MS = 2000;

const IRMA_CLIENT_URL_ENV = "IRMA_CLIENT_URL";
const CLOUDFLARED_METRICS_URL = "http://localhost:2000/quicktunnel";
const TUNNEL_POLL_INTERVAL_MS = 1000;
const TUNNEL_POLL_TIMEOUT_MS = 60_000;

async function withTiming<T>(label: string, fn: () => T | Promise<T>): Promise<T> {
    const start = performance.now();
    try {
        const result = await fn();
        const elapsedMs = performance.now() - start;
        if (elapsedMs >= TIMING_THRESHOLD_MS) {
            console.log(`✓ ${label} (${(elapsedMs / 1000).toFixed(1)}s)`);
        }
        return result;
    } catch (error) {
        const elapsed = ((performance.now() - start) / 1000).toFixed(1);
        console.error(`✗ ${label} failed (${elapsed}s)`);
        throw error;
    }
}

function spawnAsync(
    command: string,
    args: string[] = [],
    cwd: string,
    env?: Record<string, string>
): Promise<void> {
    return new Promise((resolve, reject) => {
        const childProcess = spawn(command, args, {
            cwd, stdio: "inherit", env: {
                ...process.env,
                ...(env ?? {}),
            }
        });
        childProcess.on("close", (code) => {
            if (code === 0) {
                resolve();
            } else {
                reject(new Error(`Process spawned by '${[command, ...args].join(" ")}' failed with code '${code}'`));
            }
        });
    })
}

async function resetDatabase(): Promise<void> {
    console.log("Resetting PostgreSQL database (docker compose down -v)");
    await spawnAsync("docker", ["compose", "down", "-v"], REPO_ROOT_PATH);
}

function ensureEnvLocalFile(reset: boolean): void {
    const backendEnvLocalPath = join(BACKEND_PATH, '.env');
    const backendEnvLocalExamplePath = join(BACKEND_PATH, '.env.local.example');
    if (reset || !existsSync(backendEnvLocalPath)) {
        console.log(`${reset ? "Resetting" : "Creating"} backend/.env.local from backend/.env.local.example`);
        copyFileSync(backendEnvLocalExamplePath, backendEnvLocalPath);
    }
}

async function checkDocker(): Promise<void> {
    try {
        await execAsync("docker info");
    } catch {
        throw new Error("Docker is not running or installed. Please ensure Docker is installed and running.");
    }
}

async function ensureDockerRunning(): Promise<void> {
    console.log("Checking that docker is running");
    await checkDocker();
}

// hostname is absent until the tunnel is fully established.
interface QuickTunnelResponse {
    hostname?: string;
}

async function fetchTunnelHostname(): Promise<string | null> {
    let response: Response;
    try {
        response = await fetch(CLOUDFLARED_METRICS_URL);
    } catch {
        // cloudflared not listening yet (container still starting)
        return null;
    }
    if (!response.ok) {
        return null;
    }
    const body = (await response.json()) as QuickTunnelResponse;
    const hostname = body.hostname?.trim();
    return hostname ? hostname : null;
}

async function delay(ms: number): Promise<void> {
    return new Promise((resolve) => setTimeout(resolve, ms));
}

async function waitForTunnelUrl(): Promise<string> {
    const deadline = performance.now() + TUNNEL_POLL_TIMEOUT_MS;
    while (performance.now() < deadline) {
        const hostname = await fetchTunnelHostname();
        if (hostname) {
            return `https://${hostname}`;
        }
        await delay(TUNNEL_POLL_INTERVAL_MS);
    }
    throw new Error(
        `Timed out after ${TUNNEL_POLL_TIMEOUT_MS / 1000}s waiting for the Cloudflare quick tunnel at ${CLOUDFLARED_METRICS_URL}. ` +
        `Check network connectivity to Cloudflare, or set ${IRMA_CLIENT_URL_ENV} manually (LAN IP / named tunnel) to skip auto-tunnelling.`
    );
}

async function startTunnelAndIrma(): Promise<string> {
    console.log("Starting IRMA daemon and Cloudflare tunnel");
    await spawnAsync("docker", ["compose", "up", "-d", "irma", "cloudflared"], REPO_ROOT_PATH);

    const clientUrl = await waitForTunnelUrl();
    console.log(`Cloudflare tunnel ready: ${clientUrl}`);

    // Recreate irma so --url reflects the live tunnel. The requestor API is
    // independent of --url, so this does not disturb the backend boot probe.
    console.log("Recreating IRMA daemon with the tunnel URL");
    await spawnAsync(
        "docker",
        ["compose", "up", "-d", "--force-recreate", "irma"],
        REPO_ROOT_PATH,
        { [IRMA_CLIENT_URL_ENV]: clientUrl }
    );

    return clientUrl;
}

async function startDockerCompose(clientUrl: string): Promise<void> {
    console.log("Running docker compose up for the full development stack");
    await spawnAsync("docker", ["compose", "up"], REPO_ROOT_PATH, {
        [IRMA_CLIENT_URL_ENV]: clientUrl,
    });
}

async function main() {
    const totalStart = performance.now();

    if (reset) {
        await withTiming("Reset PostgreSQL database", resetDatabase);
    }

    await withTiming("Ensure .env.local file exists", () => {
        ensureEnvLocalFile(reset);
    });

    await withTiming("Ensure Docker is available", () => ensureDockerRunning());

    const presetClientUrl = process.env[IRMA_CLIENT_URL_ENV];
    const clientUrl = presetClientUrl
        ? (console.log(`${IRMA_CLIENT_URL_ENV} is preset (${presetClientUrl}); skipping auto tunnel`), presetClientUrl)
        : await withTiming("Start Cloudflare tunnel + IRMA daemon", () => startTunnelAndIrma());

    await withTiming("Start Docker compose (full stack)", () => startDockerCompose(clientUrl));

    const totalElapsed = ((performance.now() - totalStart) / 1000).toFixed(1);
    console.log(`\n✓ Development setup complete! (${totalElapsed}s total)`);
    console.log(`Phone-facing IRMA URL (scan target): ${clientUrl}`);
    console.log("Press CTRL-C to stop Docker containers");
}

main().catch(error => {
    console.error("Error while setting up local development setup:", error);
    process.exit(1);
})

let isShuttingDown = false;

async function shutdown(): Promise<void> {
    if (isShuttingDown) {
        return;
    }
    isShuttingDown = true;

    console.log("\nShutting down local development setup");

    await cleanup();
    process.exit(0);
}

async function cleanup(): Promise<void> {
    await spawnAsync("docker", ["compose", "down"], REPO_ROOT_PATH);
}

process.on("SIGINT", () => {
    shutdown().catch((error: any) => {
        console.error("Error during shutdown:", error);
        process.exit(1);
    });
});

process.on("SIGTERM", () => {
    shutdown().catch((error: any) => {
        console.error("Error during shutdown:", error);
        process.exit(1);
    });
})
