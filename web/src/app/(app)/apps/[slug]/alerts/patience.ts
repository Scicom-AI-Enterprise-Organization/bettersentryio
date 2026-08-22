/**
 * Shared by the server page and the client control, so it lives outside both:
 * a "use client" module's exports cannot be called from a server component.
 */
export function patienceLabel(seconds: number): string {
  if (seconds === 0) return "Off — send every alert";
  if (seconds < 3600) return `${seconds / 60} minutes`;
  if (seconds === 3600) return "1 hour";
  if (seconds < 86400) return `${seconds / 3600} hours`;
  return "24 hours";
}
