import { exec as execCallback, spawn } from "child_process";
import { promisify } from "node:util";
import { join } from "node:path";
import { copyFileSync, existsSync } from "node:fs";

const execAsync = promisify(execCallback)

process.env.DOCKER_CLI_HINTS = "false";
process.env.COMPOSE_MENU = "false";

const reset = process.argv.includes('--reset')
const debug = process.argv.includes('--debug')

const REPO_ROOT_PATH = join(__dirname, '..');
const BACKEND_PATH = join(REPO_ROOT_PATH, 'backend');
const FRONTEND_PATH = join(REPO_ROOT_PATH, 'frontend');
const TIMING_THRESHOLD_MS = 2000;

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
    env?: Record<string, string>,
    detached = false
): Promise<void> {
    return new Promise((resolve, reject) => {
        const childProcess = spawn(command, args, {
            cwd, detached, stdio: "inherit", env: {
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

// Compose now *requires* POSTGRES_PASSWORD (no weak fallback), so a fresh clone
// with no root .env would abort with "required variable POSTGRES_PASSWORD is
// missing a value". Seed it from .env.example so `npm run dev` stays zero-config.
// The example's CHANGE_ME placeholder is fine for a throwaway local dev DB; only
// existing files are left untouched (never overwritten unless --reset).
function ensureRootEnvFile(reset: boolean): void {
    const rootEnvPath = join(REPO_ROOT_PATH, '.env');
    const rootEnvExamplePath = join(REPO_ROOT_PATH, '.env.example');
    if (reset || !existsSync(rootEnvPath)) {
        console.log(`${reset ? "Resetting" : "Creating"} .env from .env.example`);
        copyFileSync(rootEnvExamplePath, rootEnvPath);
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

async function streamDockerCompose(): Promise<void> {
    await spawnAsync(
        "docker",
        ["compose", "up"],
        REPO_ROOT_PATH,
        undefined,
        true
    ).catch((error: unknown) => {
        if (isShuttingDown) {
            return;
        }
        throw error;
    });
}

async function startDockerComposeDetached(): Promise<void> {
    await spawnAsync("docker", ["compose", "up", "-d"], REPO_ROOT_PATH);
}

function waitForShutdownSignal(): Promise<never> {
    return new Promise<never>(() => {
        setInterval(() => {}, 1 << 30);
    });
}

async function main() {
    const totalStart = performance.now();

    if (reset) {
        await withTiming("Reset PostgreSQL database", resetDatabase);
    }

    await withTiming("Ensure .env files exist", () => {
        ensureRootEnvFile(reset);
        ensureEnvLocalFile(reset);
    });

    await withTiming("Ensure Docker is available", () => ensureDockerRunning());

    if (debug) {
        const elapsed = ((performance.now() - totalStart) / 1000).toFixed(1);
        console.log(`\n✓ Setup ready in ${elapsed}s — starting the full development stack (--debug: streaming logs)`);
        console.log("Press CTRL-C to stop Docker containers\n");
        await streamDockerCompose();
        return;
    }

    await withTiming("Start Docker compose (full stack)", () => startDockerComposeDetached());

    const totalElapsed = ((performance.now() - totalStart) / 1000).toFixed(1);
    console.log(`\n✓ Development setup complete! (${totalElapsed}s total)`);
    console.log("Press CTRL-C to stop Docker containers (stack runs in background; re-run with --debug to stream logs)");

    // Stack runs detached; idle until CTRL-C triggers the SIGINT handler's down.
    await waitForShutdownSignal();
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
