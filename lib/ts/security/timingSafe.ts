const encoder = new TextEncoder();

export async function timingSafeEqual(a: string, b: string): Promise<boolean> {
  const aBytes = encoder.encode(a);
  const bBytes = encoder.encode(b);
  const algo = { name: "HMAC", hash: "SHA-256" };
  const key = (await crypto.subtle.generateKey(algo, false, ["sign", "verify"])) as CryptoKey;
  const hmac = await crypto.subtle.sign(algo, key, aBytes);
  const equal = await crypto.subtle.verify(algo, key, hmac, bBytes);
  return equal;
}
