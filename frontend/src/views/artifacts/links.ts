/** The digest an inkuna://artifact/<digest> link names, or "". */
export function artifactDigest(uri?: string) {
  const prefix = "inkuna://artifact/";
  return uri?.startsWith(prefix) ? uri.slice(prefix.length) : "";
}
