/**
 * The href for a value that came out of an event, or null when it must stay text.
 *
 * Everything on the event page arrived through a public ingest key, so it is
 * attacker-controlled (VAPT SAST #38). React escapes text, which leaves the href as
 * the only place such a value can do anything. A regex tests the raw string while the
 * browser acts on its own normalisation of it (tabs and newlines stripped, case
 * folded); parsing first and emitting the parser's serialisation means the href is
 * exactly the http(s) URL that was checked.
 */
export function externalHttpUrl(value: string): string | null {
  let url: URL;
  try {
    url = new URL(value.trim());
  } catch {
    return null;
  }
  return url.protocol === "http:" || url.protocol === "https:" ? url.href : null;
}
