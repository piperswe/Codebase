import { describe, expect, it } from "vitest";
import { hashPassword, verifyPassword } from "$/lib/ts/security/hash.js";

describe("hashPassword and verifyPassword", () => {
  it("should verify matches", async () => {
    const password = "mySecretPassword";
    const hash = await hashPassword(password);
    const isValid = await verifyPassword(password, hash);
    expect(isValid).toBe(true);
  });
  it("should not verify different passwords", async () => {
    const password = "mySecretPassword";
    const hash = await hashPassword(password);
    const isValid = await verifyPassword("differentPassword", hash);
    expect(isValid).toBe(false);
  });
});
