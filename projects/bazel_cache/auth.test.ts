import { describe, expect, it } from "vitest";
import { checkAuthorization } from "./auth";

describe("checkAuthorization", () => {
  it("returns false when no Authorization header is present", () => {
    const req = new Request("http://localhost");
    const result = checkAuthorization(req, "password");
    expect(result).toBe(false);
  });
});
