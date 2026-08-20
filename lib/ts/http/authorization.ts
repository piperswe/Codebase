import { hashPassword, verifyPassword } from "$/lib/ts/security/hash.js";

const decoder = new TextDecoder();

interface HashedUser {
  username: string;
  passwordHash: string;
}

export type User = { username: string; password: string } | HashedUser;

export class BasicAuthenticator {
  #users: Map<string, HashedUser> = new Map();

  constructor() {}

  async addUsers(users: User[]) {
    for (const user of users) {
      if ("password" in user) {
        this.#users.set(user.username, {
          username: user.username,
          passwordHash: await hashPassword(user.password),
        });
      } else {
        this.#users.set(user.username, user);
      }
    }
  }

  async authenticate(request: Request): Promise<string | null> {
    const authorization = request.headers.get("Authorization");
    if (!authorization) {
      return null;
    }
    const [scheme, encoded] = authorization.split(" ");
    if (!encoded || scheme !== "Basic") {
      return null;
    }
    const credentials = decoder.decode(Uint8Array.from(atob(encoded), (c) => c.charCodeAt(0)));
    const index = credentials.indexOf(":");
    const username = credentials.substring(0, index);
    const password = credentials.substring(index + 1);
    const user = this.#users.get(username);
    if (!user) {
      return null;
    }
    if (await verifyPassword(password, user.passwordHash)) {
      return username;
    } else {
      return null;
    }
  }
}
