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

export function checkAuthorization(req: Request, password: string): boolean {
  const authorization = req.headers.get("Authorization");
  if (!authorization) {
    return false;
  }
  const [scheme, encoded] = authorization.split(" ");
  if (!encoded || scheme !== "Basic") {
    return false;
  }
  const credentials = decoder.decode(Uint8Array.from(atob(encoded), (c) => c.charCodeAt(0)));
  const index = credentials.indexOf(":");
  const pass = credentials.substring(index + 1);
  return timingSafeEqual(pass, password);
}
