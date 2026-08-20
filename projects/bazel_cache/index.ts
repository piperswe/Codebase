const encoder = new TextEncoder();
const decoder = new TextDecoder();

function timingSafeEqual(a: string, b: string): boolean {
  const aBytes = encoder.encode(a);
  const bBytes = encoder.encode(b);

  // Do not return early when lengths differ — that leaks the secret's
  // length through timing.  Compare against self and negate instead.
  if (aBytes.byteLength !== bBytes.byteLength) {
    return !crypto.subtle.timingSafeEqual(aBytes, aBytes);
  }

  return crypto.subtle.timingSafeEqual(aBytes, bBytes);
}

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
          const authorization = req.headers.get("Authorization");
          if (!authorization) {
            return new Response("Unauthorized", { status: 401 });
          }
          const [scheme, encoded] = authorization.split(" ");
          if (!encoded || scheme !== "Basic") {
            return new Response("Unauthorized", { status: 401 });
          }
          const credentials = decoder.decode(
            Uint8Array.from(atob(encoded), (c) => c.charCodeAt(0)),
          );
          const index = credentials.indexOf(":");
          const pass = credentials.substring(index + 1);
          if (!timingSafeEqual(pass, env.PASSWORD)) {
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
