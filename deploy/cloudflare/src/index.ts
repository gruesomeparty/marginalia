// The Worker in front of `marginalia host`. It refuses to start a
// misconfigured instance, hands every MARGINALIA_* setting to the container,
// and forwards each request to the one container there is. Identity is not
// checked here — Cloudflare Access sits in front of the Worker, and the
// binary verifies Access's JWT itself.
import { Container, getContainer } from "@cloudflare/containers";

// Long enough for the host to load every review from R2 before it listens.
// The library's default (20s) is sized for an app that listens at once; a
// first request after sleep would otherwise fail on a store that is merely
// large, not broken.
const COLD_START_MS = 120_000;

// How long the host gets to finish what is in flight once told to stop.
const STOP_GRACE_MS = 15_000;

// Where the Durable Object remembers which settings the running container
// was started with — a hash, never the values.
const STARTED_WITH = "started-with";

export class Marginalia extends Container<Env> {
	defaultPort = 8080;
	// Asleep after this long with no request in flight. A waiting agent's
	// await_review_done is a request in flight, so it keeps the host awake.
	// State is in R2, so sleeping loses nothing but the next start's time.
	sleepAfter = "30m";
	// The readiness probe: a route the host answers without credentials.
	pingEndpoint = "localhost/healthz";
	envVars = settings(this.env);

	private readonly wanted = fingerprint(this.envVars);
	private starting?: Promise<void>;

	override async fetch(request: Request): Promise<Response> {
		if (this.starting || !this.ctx.container?.running || (await this.outdated())) {
			// One (re)start for every request that arrives during it.
			this.starting ??= this.restart().finally(() => {
				this.starting = undefined;
			});
			try {
				await this.starting;
			} catch (err) {
				// Most often a setting the binary refused; its log says which.
				const why = err instanceof Error ? err.message : String(err);
				return new Response(`marginalia: the host did not start: ${why}\n`, { status: 503 });
			}
		}
		return super.fetch(request);
	}

	// outdated says the running container was started with settings other
	// than this deploy's. The binary reads its environment once, at start, so
	// without this a rotated token or a changed setting would wait for the
	// container's next sleep — which an agent that keeps waiting postpones
	// indefinitely.
	private async outdated(): Promise<boolean> {
		return this.ctx.storage.kv.get<string>(STARTED_WITH) !== (await this.wanted);
	}

	private async restart(): Promise<void> {
		if (this.ctx.container?.running) {
			// SIGTERM: the host stops accepting, drains what is in flight,
			// and exits. Every write it acknowledged is already in R2.
			await this.stop();
			for (let waited = 0; this.ctx.container.running && waited < STOP_GRACE_MS; waited += 250) {
				await scheduler.wait(250);
			}
			if (this.ctx.container.running) {
				await this.destroy();
			}
		}
		await this.startAndWaitForPorts({
			cancellationOptions: { portReadyTimeoutMS: COLD_START_MS },
		});
		this.ctx.storage.kv.put(STARTED_WITH, await this.wanted);
	}
}

export default {
	async fetch(request, env): Promise<Response> {
		const problem = misconfigured(settings(env));
		if (problem) {
			return new Response(`marginalia: ${problem}\n`, { status: 500 });
		}
		return getContainer(env.MARGINALIA).fetch(request);
	},
} satisfies ExportedHandler<Env>;

// settings is every MARGINALIA_* string the Worker was given — the variables
// and secrets set at deploy — which is the whole of the binary's
// configuration: it reads its settings from the environment by design, so a
// setting a later release adds needs no change here.
function settings(env: Env): Record<string, string> {
	const out: Record<string, string> = {};
	for (const [key, value] of Object.entries(env)) {
		if (key.startsWith("MARGINALIA_") && typeof value === "string" && value !== "") {
			out[key] = value;
		}
	}
	return out;
}

// fingerprint is a hash of the settings, so the Durable Object can tell a
// deploy that changed them without keeping a copy of any secret.
async function fingerprint(vars: Record<string, string> | undefined): Promise<string> {
	const canonical = JSON.stringify(Object.entries(vars ?? {}).sort(([a], [b]) => (a < b ? -1 : 1)));
	const digest = await crypto.subtle.digest("SHA-256", new TextEncoder().encode(canonical));
	return [...new Uint8Array(digest)].map((b) => b.toString(16).padStart(2, "0")).join("");
}

// misconfigured names what the binary cannot know is wrong here. Everything
// else — a missing token, half an identity — the binary refuses itself, and
// its log says which.
function misconfigured(vars: Record<string, string>): string | undefined {
	const missing = ["MARGINALIA_PUBLIC_URL", "MARGINALIA_STORE"].filter((key) => !vars[key]);
	if (missing.length > 0) {
		return `not configured: set ${missing.join(" and ")} (deploy/cloudflare/README.md)`;
	}
	// The binary's default store is a directory, and a container's disk does
	// not outlive it: every review would vanish the first time it sleeps.
	if (!vars.MARGINALIA_STORE.startsWith("r2://")) {
		return "MARGINALIA_STORE must be an r2:// store: a container's disk is gone when it sleeps";
	}
	return undefined;
}
