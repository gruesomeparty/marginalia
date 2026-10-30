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

// The image wrangler.jsonc names, and the instance it runs on: 1/4 vCPU and
// 1 GiB, since every review is loaded into memory at start.
const IMAGE = "marginalia";
const INSTANCE = "basic";

export class Marginalia extends Container<Env> {
	defaultPort = 8080;
	// Asleep after this long with no request in flight. A waiting agent's
	// await_review_done is a request in flight, so it keeps the host awake.
	// State is in R2, so sleeping loses nothing but the next start's time.
	sleepAfter = "30m";
	// The readiness probe: a route the host answers without credentials.
	pingEndpoint = "localhost/healthz";
	envVars = settings(this.env);

	private readonly wanted: Promise<string>;
	private starting?: Promise<void>;

	constructor(ctx: DurableObjectState<{}>, env: Env) {
		super(ctx, env);
		// Under the durable_object scheduling policy the container is
		// started with an image and an instance size, and the library only
		// passes env, entrypoint, internet access and labels. So it is handed
		// a container whose start() adds the two.
		const container = ctx.container;
		if (container) {
			const managed = new Proxy(container, {
				get(target, key) {
					if (key === "start") {
						return (options: Omit<ContainerStartupOptions, "image" | "containerSnapshot">) =>
							target.start({ ...options, image: target.images[IMAGE], instance: INSTANCE });
					}
					const value: unknown = Reflect.get(target, key);
					return typeof value === "function" ? value.bind(target) : value;
				},
			});
			Reflect.set(this, "container", managed);
		}
		// A new release is a new image, which a running container keeps until
		// it is restarted: it counts as a changed setting.
		this.wanted = fingerprint({ ...this.envVars, image: container?.images[IMAGE] ?? "" });
	}

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

	// awake says whether the container is running, without starting it or
	// counting as activity that keeps it awake.
	async awake(): Promise<boolean> {
		return Boolean(this.ctx.container?.running);
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
		const vars = settings(env);
		const problem = misconfigured(vars);
		if (problem) {
			return new Response(`marginalia: ${problem}\n`, { status: 500 });
		}
		const container = getContainer(env.MARGINALIA);
		const refused = await refuse(request, vars, () => container.awake());
		if (refused) {
			return refused;
		}
		return container.fetch(request);
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

// refuse answers, here at the edge, the requests the host would only refuse
// and anonymous health probes.
// The agent paths are public in Access by necessity, and every request the
// Worker forwards starts the container if it sleeps: without this, anyone
// could keep it awake, and billed, with requests that carry no credential.
// The host still checks every credential itself; this only saves it the
// trouble of being woken to say no.
async function refuse(
	request: Request,
	vars: Record<string, string>,
	awake: () => Promise<boolean>,
): Promise<Response | undefined> {
	const path = new URL(request.url).pathname;
	const oauth = Boolean(vars.MARGINALIA_MCP_OAUTH_ISSUER);
	const bearer = /^Bearer\s+(\S+)\s*$/i.exec(request.headers.get("Authorization") ?? "")?.[1];
	if (path === "/healthz") {
		// Anyone may ask, so a stranger's probe is answered here from the
		// container's state: it neither starts the container nor keeps it
		// awake. The static token buys the real check, end to end.
		if (bearer && vars.MARGINALIA_MCP_TOKEN && (await same(bearer, vars.MARGINALIA_MCP_TOKEN))) {
			return undefined;
		}
		const headers = { "Content-Type": "text/plain; charset=utf-8", "Cache-Control": "no-store" };
		try {
			return new Response((await awake()) ? "ok\n" : "ok (asleep)\n", { headers });
		} catch (err) {
			const why = err instanceof Error ? err.message : String(err);
			return new Response(`marginalia: the container is unavailable: ${why}\n`, { status: 503, headers });
		}
	}
	if (path.startsWith("/.well-known/")) {
		// The host serves only the OAuth metadata, and only with an issuer.
		return oauth && path.startsWith("/.well-known/oauth-protected-resource")
			? undefined
			: new Response("404 page not found\n", { status: 404 });
	}
	// An agent's long-poll of one review: read-only, bearer-authenticated by
	// the host, and public in Access like /mcp, so it is gated here the same way.
	const agentWait = /^\/agent\/r\/[^/]+\/wait\/?$/.test(path);
	if (path.startsWith("/agent/") && !agentWait) {
		// The host serves no other agent path.
		return new Response("404 page not found\n", { status: 404 });
	}
	if (path !== "/mcp" && !path.startsWith("/mcp/") && !agentWait) {
		// Every other path is the reviewer's, and Access lets a request
		// through only with its JWT attached. One without it means Access is
		// missing or misconfigured: the host would refuse it anyway, so it is
		// refused here rather than woken for. The host verifies the JWT.
		return request.headers.has("Cf-Access-Jwt-Assertion")
			? undefined
			: new Response("marginalia: this page is behind Cloudflare Access, which did not vouch for the request\n", {
					status: 403,
				});
	}
	if (bearer) {
		// With OAuth, a JWT can be checked only by the host. Without it, the
		// static token is the only credential there is, and is checked here
		// too, in constant time.
		if (oauth || !vars.MARGINALIA_MCP_TOKEN || (await same(bearer, vars.MARGINALIA_MCP_TOKEN))) {
			return undefined;
		}
	}
	// The host's own challenge, so a client reads it the same either way.
	const challenge = oauth
		? `Bearer resource_metadata="${vars.MARGINALIA_PUBLIC_URL.replace(/\/$/, "")}/.well-known/oauth-protected-resource/mcp"`
		: 'Bearer realm="marginalia"';
	return new Response(`marginalia: ${agentWait ? "this path" : "/mcp"} needs \`Authorization: Bearer <token>\`\n`, {
		status: 401,
		headers: { "WWW-Authenticate": challenge },
	});
}

// same compares two secrets in constant time, by their digests, which have
// equal lengths whatever the inputs.
async function same(a: string, b: string): Promise<boolean> {
	const digest = (s: string) => crypto.subtle.digest("SHA-256", new TextEncoder().encode(s.trim()));
	const [x, y] = await Promise.all([digest(a), digest(b)]);
	return crypto.subtle.timingSafeEqual(x, y);
}
