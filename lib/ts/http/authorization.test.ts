import { describe, expect, it } from "vitest";
import { BasicAuthenticator } from "./authorization.js";

describe("BasicAuthenticator", () => {
  describe("#authenticate", () => {
    it("returns null if there is no Authorization header", async () => {
      const authenticator = new BasicAuthenticator();
      const request = new Request("http://localhost");
      const result = await authenticator.authenticate(request);
      expect(result).toBeNull();
    });
  });
});
