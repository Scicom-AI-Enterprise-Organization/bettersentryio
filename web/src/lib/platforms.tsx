import type { ReactNode } from "react";

/**
 * The platforms we actually ship an integration for.
 *
 * Deliberately short. A grid of forty logos where thirty-five are "generic HTTP with a
 * different icon" is a lie about what is supported — every entry here has a snippet
 * written for its real shape (an ASGI lifespan task, a Celery worker, a cron line).
 */

export type PlatformId = "fastapi" | "python" | "celery" | "shell" | "docker" | "kubernetes";

export type Platform = {
  id: PlatformId;
  name: string;
  /** Grouping for the picker tabs. */
  group: "Server" | "Worker" | "Infrastructure";
  /** One line: when this is the right pick. */
  blurb: string;
  logo: ReactNode;
};

/* ---- logos ------------------------------------------------------------------
 * Hand-drawn simplified marks rather than brand assets: no external requests, no
 * licensing question, and they stay legible at 40px where a detailed logo does not.
 */

const FastAPILogo = (
  <svg viewBox="0 0 32 32" className="h-8 w-8" role="img" aria-label="FastAPI">
    <circle cx="16" cy="16" r="15" fill="#05998b" />
    <path d="M17.5 5 L9 17.5 h5.5 L13 27 l9.5-13h-5.7z" fill="#fff" />
  </svg>
);

const PythonLogo = (
  <svg viewBox="0 0 32 32" className="h-8 w-8" role="img" aria-label="Python">
    <path
      d="M15.9 3c-2.6 0-4.4.5-5.3 1.3-.8.7-1 1.7-1 2.9v2.2h6.6v1H7.6c-1.4 0-2.5.8-3.1 2.2-.6 1.6-.6 3.2 0 4.9.5 1.4 1.5 2.3 2.9 2.3h2.1v-2.7c0-1.6 1.4-3.2 3.1-3.2h5.5c1.3 0 2.4-1.1 2.4-2.4V7.2c0-1.3-1-2.3-2.4-2.6-.9-.2-1.7-.3-2.2-.3zm-3.6 2a1.1 1.1 0 110 2.2 1.1 1.1 0 010-2.2z"
      fill="#3776ab"
    />
    <path
      d="M16.1 29c2.6 0 4.4-.5 5.3-1.3.8-.7 1-1.7 1-2.9v-2.2h-6.6v-1h8.6c1.4 0 2.5-.8 3.1-2.2.6-1.6.6-3.2 0-4.9-.5-1.4-1.5-2.3-2.9-2.3h-2.1v2.7c0 1.6-1.4 3.2-3.1 3.2h-5.5c-1.3 0-2.4 1.1-2.4 2.4v4.3c0 1.3 1 2.3 2.4 2.6.9.2 1.7.3 2.2.3zm3.6-2a1.1 1.1 0 110-2.2 1.1 1.1 0 010 2.2z"
      fill="#ffd43b"
    />
  </svg>
);

const CeleryLogo = (
  <svg viewBox="0 0 32 32" className="h-8 w-8" role="img" aria-label="Celery">
    <circle cx="16" cy="16" r="15" fill="#37814a" />
    <path
      d="M16 7c-1.2 2.4-1.6 5-1.4 7.6-1.6-1.6-3.6-2.6-5.6-3 .6 2.5 2 4.7 3.9 6.3-1 .1-2 .5-2.9 1.1 1.6 1.5 3.7 2.3 5.9 2.3v3.9h.2v-3.9c2.2 0 4.3-.8 5.9-2.3-.9-.6-1.9-1-2.9-1.1 1.9-1.6 3.3-3.8 3.9-6.3-2 .4-4 1.4-5.6 3 .2-2.6-.2-5.2-1.4-7.6z"
      fill="#fff"
    />
  </svg>
);

const ShellLogo = (
  <svg viewBox="0 0 32 32" className="h-8 w-8" role="img" aria-label="Shell">
    <rect x="1" y="4" width="30" height="24" rx="3" fill="#1f2937" />
    <rect x="1" y="4" width="30" height="5" rx="3" fill="#374151" />
    <path
      d="M7 15l3.5 3-3.5 3M14 21h6"
      stroke="#4ade80"
      strokeWidth="2"
      strokeLinecap="round"
      strokeLinejoin="round"
      fill="none"
    />
  </svg>
);

const DockerLogo = (
  <svg viewBox="0 0 32 32" className="h-8 w-8" role="img" aria-label="Docker">
    <g fill="#2496ed">
      <rect x="11" y="12" width="3.4" height="3.4" />
      <rect x="15" y="12" width="3.4" height="3.4" />
      <rect x="19" y="12" width="3.4" height="3.4" />
      <rect x="15" y="8" width="3.4" height="3.4" />
      <rect x="7" y="16" width="3.4" height="3.4" />
      <rect x="11" y="16" width="3.4" height="3.4" />
      <rect x="15" y="16" width="3.4" height="3.4" />
      <rect x="19" y="16" width="3.4" height="3.4" />
      <path d="M3 21c4 5 12 5.6 18.6 3.3 3.4-1.2 5.7-3.3 6.8-6.1-1.6-.9-3.6-.9-5 .1-.5-2-1.9-3.3-3.4-4l-.8 1.7c.9.6 1.7 1.6 1.6 3.2H3z" />
    </g>
  </svg>
);

const KubernetesLogo = (
  <svg viewBox="0 0 32 32" className="h-8 w-8" role="img" aria-label="Kubernetes">
    <path d="M16 2l12 6v12l-12 6L4 20V8z" fill="#326ce5" />
    <g stroke="#fff" strokeWidth="1.6" fill="none">
      <circle cx="16" cy="16" r="3.4" />
      <path d="M16 6.2v6.4M16 19.4v6.4M7.6 11.2l5.5 3.2M18.9 17.6l5.5 3.2M7.6 20.8l5.5-3.2M18.9 14.4l5.5-3.2" />
    </g>
  </svg>
);

export const PLATFORMS: Platform[] = [
  {
    id: "fastapi",
    name: "FastAPI",
    group: "Server",
    blurb: "A background loop inside an ASGI service.",
    logo: FastAPILogo,
  },
  {
    id: "python",
    name: "Python",
    group: "Worker",
    blurb: "Any while loop — a consumer, a trainer, a poller.",
    logo: PythonLogo,
  },
  {
    id: "celery",
    name: "Celery",
    group: "Worker",
    blurb: "A queue worker that should be draining tasks.",
    logo: CeleryLogo,
  },
  {
    id: "shell",
    name: "Shell / cron",
    group: "Infrastructure",
    blurb: "No SDK — one curl, so a silent cron failure is visible.",
    logo: ShellLogo,
  },
  {
    id: "docker",
    name: "Docker",
    group: "Infrastructure",
    blurb: "Passing the key in as environment, not baking it in.",
    logo: DockerLogo,
  },
  {
    id: "kubernetes",
    name: "Kubernetes",
    group: "Infrastructure",
    blurb: "The key as a Secret, mounted into the pod.",
    logo: KubernetesLogo,
  },
];

export const PLATFORM_GROUPS = ["Server", "Worker", "Infrastructure"] as const;

export function platform(id: string | undefined): Platform | undefined {
  return PLATFORMS.find((p) => p.id === id);
}

/**
 * A small logo for lists, falling back to a neutral mark for projects with no platform.
 *
 * The fallback is deliberately not a plus: in the collapsed project rail it would sit
 * beside the real "New project" plus and read as another add button.
 */
export function platformMark(id: string | undefined): ReactNode {
  const p = platform(id);
  if (p) return p.logo;
  return (
    <svg viewBox="0 0 32 32" className="h-8 w-8" role="img" aria-label="No platform set">
      <rect x="3" y="3" width="26" height="26" rx="6" className="fill-muted" />
      <g className="fill-muted-foreground">
        <circle cx="12" cy="12" r="2" />
        <circle cx="20" cy="12" r="2" />
        <circle cx="12" cy="20" r="2" />
        <circle cx="20" cy="20" r="2" />
      </g>
    </svg>
  );
}
