import {
  Activity,
  AlertTriangle,
  BookOpen,
  Boxes,
  Building2,
  Gauge,
  Plug,
  Settings,
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
  id: "projects" | "monitors" | "settings";
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
    href: "/learn",
    match: ["/learn", "/profile", "/admin"],
    groups: [
      { items: [{ label: "How it works", href: "/learn" }] },
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
];

/** The views a project has, in the order they matter. */
export function projectNav(slug: string): NavItem[] {
  return [
    { label: "Errors & Outages", href: `/apps/${slug}/issues/outages` },
    { label: "Breached Metrics", href: `/apps/${slug}/issues/breached` },
    { label: "Warnings", href: `/apps/${slug}/issues/warnings` },
    { label: "Setup", href: `/apps/${slug}/setup` },
  ];
}

export const ITEM_ICONS: Record<string, React.ElementType> = {
  outages: Siren,
  breached: Gauge,
  warnings: AlertTriangle,
  setup: Plug,
  "/monitors": Activity,
  "/incidents": Siren,
  "/learn": BookOpen,
  "/profile": UserCog,
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
