import { checkAuthorization } from "./auth";

export default {
  async fetch(req: Request, env: Env): Promise<Response> {
    try {
      const url = new URL(req.url);
      const path = url.pathname;
      switch (req.method) {
        case "GET":
          const file = await env.BUCKET.get(path);
          if (!file) {
            return new Response("File not found", { status: 404 });
          }
          return new Response(file.body);
        case "PUT":
          if (!checkAuthorization(req, env.PASSWORD)) {
            return new Response("Unauthorized", { status: 401 });
          }
          await env.BUCKET.put(path, req.body);
          return new Response("File uploaded successfully");
        default:
          return new Response("Method not allowed", { status: 405 });
      }
    } catch (err) {
      console.error(err);
      return new Response("Internal server error", { status: 500 });
    }
  },
};
