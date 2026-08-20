import { timingSafeEqual } from "$/lib/ts/security/timingSafe.js";

const encoder = new TextEncoder();

const ITERATIONS = 100_000;

interface HashedPassword {
  algo: "PBKDF2-SHA-512";
  iterations: number;
  salt: string;
  hash: string;
}

export async function hashPassword(password: string): Promise<string> {
  const key = await crypto.subtle.importKey("raw", encoder.encode(password), "PBKDF2", false, [
    "deriveBits",
  ]);
  const salt = crypto.getRandomValues(new Uint8Array(16));
  const hash = await crypto.subtle.deriveBits(
    {
      name: "PBKDF2",
      hash: "SHA-512",
      salt,
      iterations: ITERATIONS,
    },
    key,
    512,
  );
  const hashedPassword: HashedPassword = {
    algo: "PBKDF2-SHA-512",
    iterations: ITERATIONS,
    salt: btoa(String.fromCharCode(...salt)),
    hash: btoa(String.fromCharCode(...new Uint8Array(hash))),
  };
  return JSON.stringify(hashedPassword);
}

export async function verifyPassword(password: string, hashedPassword: string): Promise<boolean> {
  const { algo, iterations, salt, hash } = JSON.parse(hashedPassword) as HashedPassword;
  if (algo !== "PBKDF2-SHA-512") {
    throw new Error("Unsupported hashing algorithm");
  }
  const key = await crypto.subtle.importKey("raw", encoder.encode(password), "PBKDF2", false, [
    "deriveBits",
  ]);
  const derivedBits = await crypto.subtle.deriveBits(
    {
      name: "PBKDF2",
      hash: "SHA-512",
      salt: Uint8Array.from(atob(salt), (c) => c.charCodeAt(0)),
      iterations: iterations,
    },
    key,
    512,
  );
  const derivedHash = btoa(String.fromCharCode(...new Uint8Array(derivedBits)));
  return timingSafeEqual(derivedHash, hash);
}
