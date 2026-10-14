// The Worker in front of `marginalia host`. It does three things and holds
// no state: refuses to start a misconfigured instance, hands every
// MARGINALIA_* setting to the container, and forwards each request to the
// one container there is. Identity is not checked here — Cloudflare Access
// sits in front of the Worker, and the binary verifies Access's JWT itself.
import { Container, getContainer } from "@cloudflare/containers";

// Long enough for the host to load every review from R2 before it listens.
// The library's default (20s) is sized for an app that listens at once; a
// first request after sleep would otherwise fail on a store that is merely
// large, not broken.
const COLD_START_MS = 120_000;

export class Marginalia extends Container<Env> {
	defaultPort = 8080;
	// Asleep after this long with no request in flight. A waiting agent's
	// await_review_done is a request in flight, so it keeps the host awake.
	// State is in R2, so sleeping loses nothing but the next start's time.
	sleepAfter = "30m";
	// The readiness probe: a route the host answers without credentials.
	pingEndpoint = "localhost/healthz";
	envVars = settings(this.env);

	private starting?: Promise<void>;

	override async fetch(request: Request): Promise<Response> {
		if (!this.ctx.container?.running || this.starting) {
			// One start for every request that arrives during it.
			this.starting ??= this.startAndWaitForPorts({
				cancellationOptions: { portReadyTimeoutMS: COLD_START_MS },
			}).finally(() => {
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
