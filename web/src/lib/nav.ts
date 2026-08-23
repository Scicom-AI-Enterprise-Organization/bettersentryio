import {
  Activity,
  AlertTriangle,
  BellRing,
  BookOpen,
  Boxes,
  Bug,
  Building2,
  ChartColumn,
  Gauge,
  KeyRound,
  Rocket,
  ScrollText,
  Settings,
  Settings2,
  ShieldCheck,
  Siren,
  UserCog,
  Users,
} from "lucide-react";

/**
 * The navigation tree.
 *
 * Two layers: a rail of sections, and a panel of that section's contents. Issues are
 * deliberately not a rail entry — an issue is always an issue *in* a project, so the
 * path to one runs through the project. Pick a project in the panel and the panel
 * becomes that project.
 */

export type NavItem = {
  label: string;
  href: string;
  admin?: boolean;
};

export type NavGroup = {
  label?: string;
  items: NavItem[];
};

export type NavSection = {
  id: "projects" | "monitors" | "settings" | "learn";
  label: string;
  icon: React.ElementType;
  /** Where the rail entry goes. */
  href: string;
  /** Every path that lights this rail entry. */
  match: string[];
  /** Static contents. The projects section builds its panel from live data instead. */
  groups: NavGroup[];
};

export const SECTIONS: NavSection[] = [
  {
    id: "projects",
    label: "Projects",
    icon: Boxes,
    href: "/apps",
    match: ["/apps"],
    groups: [],
  },
  {
    id: "monitors",
    label: "Monitors",
    icon: Activity,
    href: "/monitors",
    match: ["/monitors", "/incidents"],
    groups: [
      {
        items: [
          { label: "All monitors", href: "/monitors" },
          { label: "Incident log", href: "/incidents" },
        ],
      },
    ],
  },
  {
    id: "settings",
    label: "Settings",
    icon: Settings,
    // Alerts is what people come to Settings to change; "How it works" is a manual you
    // read once, so it sits last and no longer owns the section's landing page.
    href: "/admin/alerts",
    match: ["/profile", "/admin"],
    groups: [
      {
        label: "Alerting",
        items: [
          { label: "Alerts", href: "/admin/alerts", admin: true },
          { label: "API tokens", href: "/admin/tokens", admin: true },
          { label: "Audit log", href: "/admin/audit", admin: true },
        ],
      },
      { label: "Account", items: [{ label: "Profile", href: "/profile" }] },
      {
        label: "Organization",
        items: [
          { label: "Users", href: "/admin/users", admin: true },
          { label: "Roles", href: "/admin/roles", admin: true },
          { label: "Organization", href: "/admin/organization", admin: true },
        ],
      },
    ],
  },
  {
    // The guide is a destination of its own, not a setting: it earns a rail entry so
    // it is one click from anywhere, and its panel is the guide's own table of
    // contents rather than a single redundant link.
    id: "learn",
    label: "How it works",
    icon: BookOpen,
    href: "/learn",
    match: ["/learn"],
    groups: [
      {
        items: [
          { label: "Overview", href: "/learn" },
          { label: "Errors", href: "/learn#errors" },
          { label: "Errors & Outages", href: "/learn#outages" },
          { label: "Breached Metrics", href: "/learn#breached" },
          { label: "Warnings", href: "/learn#warnings" },
        ],
      },
    ],
  },
];

/** The views a project has, in the order they matter. */
export function projectNav(slug: string): NavItem[] {
  return [
    { label: "Errors & Outages", href: `/apps/${slug}/issues/outages` },
    { label: "Breached Metrics", href: `/apps/${slug}/issues/breached` },
    { label: "Warnings", href: `/apps/${slug}/issues/warnings` },
    { label: "Analytics", href: `/apps/${slug}/analytics` },
    { label: "Releases", href: `/apps/${slug}/releases` },
    // Alerts and Settings are the project's configuration, so they close the list.
    { label: "Alerts", href: `/apps/${slug}/alerts` },
    { label: "Settings", href: `/apps/${slug}/settings` },
  ];
}

export const ITEM_ICONS: Record<string, React.ElementType> = {
  outages: Siren,
  breached: Gauge,
  warnings: AlertTriangle,
  analytics: ChartColumn,
  releases: Rocket,
  alerts: BellRing,
  settings: Settings2,
  "/monitors": Activity,
  "/incidents": Siren,
  "/admin/alerts": BellRing,
  "/learn": BookOpen,
  "/learn#errors": Bug,
  "/learn#outages": Siren,
  "/learn#breached": Gauge,
  "/learn#warnings": AlertTriangle,
  "/profile": UserCog,
  "/admin/tokens": KeyRound,
  "/admin/audit": ScrollText,
  "/admin/users": Users,
  "/admin/roles": ShieldCheck,
  "/admin/organization": Building2,
};

/** Icon for a project view, keyed by its last path segment. */
export function viewIcon(href: string): React.ElementType | undefined {
  return ITEM_ICONS[href.split("/").pop() ?? ""];
}

export function sectionFor(pathname: string): NavSection {
  const hit = SECTIONS.find((s) =>
    s.match.some((m) => pathname === m || pathname.startsWith(`${m}/`)),
  );
  return hit ?? SECTIONS[0];
}

/** The project slug in the path, or null. `/apps/new` is creation, not a project. */
export function projectInPath(pathname: string): string | null {
  const m = /^\/apps\/([^/]+)/.exec(pathname);
  return m && m[1] !== "new" ? m[1] : null;
}

/** Landing view: the project list. */
export const HOME = "/apps";
