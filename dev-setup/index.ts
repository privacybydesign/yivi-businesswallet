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

async function startDockerCompose(): Promise<void> {
    console.log("Running docker compose up for containerized PostgreSQL");
    await spawnAsync("docker", ["compose", "up"], REPO_ROOT_PATH);
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

    const databasePromise = withTiming("Start Docker compose (PostgreSQL + migrations)", () => startDockerCompose())

    await Promise.all([databasePromise]);

    const totalElapsed = ((performance.now() - totalStart) / 1000).toFixed(1);
    console.log(`\n✓ Development setup complete! (${totalElapsed}s total)`);
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

