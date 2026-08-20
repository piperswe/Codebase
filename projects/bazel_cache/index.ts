import { BasicAuthenticator } from "$/lib/ts/http/authorization.js";

let authenticator: BasicAuthenticator | undefined;
async function getAuthenticator(env: Env): Promise<BasicAuthenticator> {
  if (!authenticator) {
    authenticator = new BasicAuthenticator();
    await authenticator.addUsers([{ username: "user", password: env.PASSWORD }]);
  }
  return authenticator;
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
          const authenticator = await getAuthenticator(env);
          if (!(await authenticator.authenticate(req))) {
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
