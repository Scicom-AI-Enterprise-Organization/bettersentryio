import { cookies } from "next/headers";

import { requireUser } from "@/lib/rbac";
import { getApps } from "@/lib/bsio";
import { ConsoleRail } from "@/components/console/rail";
import { ConsoleNavPanel } from "@/components/console/nav-panel";
import { ProjectPanel } from "@/components/console/project-panel";
import { SidebarStateProvider } from "@/components/console/sidebar-state";
import { ConsoleTopbar } from "@/components/console/topbar";
import {
  NAV_PANEL_COOKIE,
  PROJECT_PANEL_COOKIE,
  panelCollapsed,
} from "@/lib/panel-state";

export default async function AppLayout({ children }: { children: React.ReactNode }) {
  const user = await requireUser();
  const isAdmin = user.permissions.some((p) => ["users:read", "roles:read"].includes(p));

  // The panel lists projects, so it needs them. An unreachable engine must not take the
  // whole shell down — the list just renders empty.
  // Collapse state comes from the request, so a collapsed sidebar renders collapsed
  // in the first byte of HTML instead of flashing open and snapping shut.
  const jar = await cookies();
  const navCollapsed = panelCollapsed(jar.get(NAV_PANEL_COOKIE)?.value);
  const projectCollapsed = panelCollapsed(jar.get(PROJECT_PANEL_COOKIE)?.value);

  const apps = await getApps();
  const projects = apps.ok
    ? apps.data.apps.map((a) => ({
        slug: a.slug,
        name: a.name,
        platform: a.platform,
        unhealthy: a.unhealthy,
        connected: a.connected,
      }))
    : [];

  return (
    <SidebarStateProvider>
      <div className="flex h-screen overflow-hidden bg-background text-foreground">
        <ConsoleRail />
        <ConsoleNavPanel
          isAdmin={isAdmin}
          projects={projects}
          initialCollapsed={navCollapsed}
        />
        <ProjectPanel projects={projects} initialCollapsed={projectCollapsed} />
        <main className="flex min-w-0 flex-1 flex-col overflow-hidden">
          <ConsoleTopbar />
          <div className="scrollbar-thin flex-1 overflow-y-auto px-6 py-6 lg:px-8">
            {children}
          </div>
        </main>
      </div>
    </SidebarStateProvider>
  );
}
