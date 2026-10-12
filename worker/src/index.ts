import { RelayHub } from "./hub"
import { Env } from "./env"

export { RelayHub }

export default {
  async fetch(request: Request, env: Env): Promise<Response> {
    const url = new URL(request.url)
    if (url.pathname === "/ws") {
      if (request.headers.get("Upgrade")?.toLowerCase() !== "websocket") {
        return new Response("expected websocket upgrade", { status: 400 })
      }
      // One global hub instance holds every teacher and classroom socket.
      const stub = env.HUB.get(env.HUB.idFromName("global"))
      return stub.fetch(request)
    }
    if (url.pathname === "/healthz") return new Response("ok")
    return new Response("not found", { status: 404 })
  },
}
