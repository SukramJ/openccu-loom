import type { IconName } from "$lib/icons";
import { t } from "$lib/i18n";

/**
 * The navigation table, shared by the sidebar and by every surface that
 * needs to know which views exist and what they are called.
 *
 * It lives here rather than inside Sidebar.svelte because the start-route
 * preference (O03) has to offer exactly the views the operator can
 * actually reach. A second, hand-maintained list would drift the moment a
 * view is added or a capability gate changes - and the operator would be
 * offered a landing page that renders nothing.
 */

export type RouteKind =
  | "list"
  | "overview"
  | "favorites"
  | "detail"
  | "backups"
  | "sysvars"
  | "programs"
  | "groups"
  | "links"
  | "diagrams"
  | "messages"
  | "audit"
  | "diagnostics"
  | "energy"
  | "fleet"
  | "logs"
  | "settings"
  | "inbox"
  | "firmware"
  | "signal"
  | "matter"
  | "alarm"
  | "security"
  | "about"
  | "unknown";

/** One navigation entry. `matches` lists the route kinds it is active for. */
export type NavItem = {
  href: string;
  icon: IconName;
  label: string;
  matches: RouteKind[];
};

/** A labelled group of navigation entries. */
export type NavCluster = {
  label: string;
  items: NavItem[];
};

/**
 * Availability gates. Every one of them hides views that would otherwise
 * render an error or an empty shell: the Matter subtree is opt-in, the
 * history-backed views need the recording feature, and three admin views
 * are role-gated.
 */
export type NavGates = {
  matterEnabled: boolean;
  historyEnabled: boolean;
  isAdmin: boolean;
};

/**
 * Cluster-grouped navigation. Order is opinionated: the top cluster
 * surfaces day-to-day work, the lowest groups admin / system.
 *
 * Labels resolve through t(), so callers must invoke this inside a
 * reactive context if they want it to follow a locale switch.
 */
export function navClusters(gates: NavGates): NavCluster[] {
  return [
    {
      label: t("sidebar.cluster.overview"),
      items: [
        {
          href: "#/overview",
          icon: "mdi:dots-grid",
          label: t("nav.overview"),
          matches: ["overview"],
        },
        {
          href: "#/devices",
          icon: "mdi:home",
          label: t("nav.devices"),
          matches: ["list", "detail"],
        },
        {
          href: "#/favorites",
          icon: "mdi:star",
          label: t("nav.favorites"),
          matches: ["favorites"],
        },
        {
          href: "#/alarm",
          icon: "mdi:shield-home",
          label: t("nav.alarm"),
          matches: ["alarm"],
        },
        {
          href: "#/security",
          icon: "mdi:shield-alert",
          label: t("nav.security"),
          matches: ["security"],
        },
        {
          href: "#/inbox",
          icon: "mdi:list-checks",
          label: t("nav.inbox"),
          matches: ["inbox"],
        },
        {
          href: "#/fleet",
          icon: "mdi:server-network",
          label: t("nav.fleet"),
          matches: ["fleet"],
        },
      ],
    },
    {
      label: t("sidebar.cluster.automation"),
      items: [
        {
          href: "#/programs",
          icon: "mdi:play",
          label: t("nav.programs"),
          matches: ["programs"],
        },
        {
          href: "#/sysvars",
          icon: "mdi:zap",
          label: t("nav.sysvars"),
          matches: ["sysvars"],
        },
        {
          href: "#/groups",
          icon: "mdi:home-group",
          label: t("nav.groups"),
          matches: ["groups"],
        },
        {
          href: "#/links",
          icon: "mdi:link",
          label: t("nav.links"),
          matches: ["links"],
        },
      ],
    },
    {
      label: t("sidebar.cluster.diagnose"),
      items: [
        {
          href: "#/messages",
          icon: "mdi:bell",
          label: t("nav.messages"),
          matches: ["messages"],
        },
        {
          href: "#/diagnostics",
          icon: "mdi:gauge",
          label: t("nav.diagnostics"),
          matches: ["diagnostics"],
        },
        // Energy and Diagrams both chart measurement history — only surface
        // them when the opt-in history-recording feature is enabled.
        ...(gates.historyEnabled
          ? [
              {
                href: "#/energy",
                icon: "mdi:zap" as const,
                label: t("nav.energy"),
                matches: ["energy"] as RouteKind[],
              },
              {
                href: "#/diagrams",
                icon: "mdi:chart-line-variant" as const,
                label: t("nav.diagrams"),
                matches: ["diagrams"] as RouteKind[],
              },
            ]
          : []),
        {
          href: "#/signal",
          icon: "mdi:signal",
          label: t("nav.signal"),
          matches: ["signal"],
        },
        {
          href: "#/audit",
          icon: "mdi:history",
          label: t("nav.audit"),
          matches: ["audit"],
        },
        ...(gates.isAdmin
          ? [
              {
                href: "#/logs",
                icon: "mdi:text-box-search-outline" as const,
                label: t("nav.logs"),
                matches: ["logs"] as RouteKind[],
              },
            ]
          : []),
      ],
    },
    ...(gates.matterEnabled
      ? [
          {
            label: t("sidebar.cluster.bridges"),
            items: [
              {
                href: "#/matter",
                icon: "mdi:link" as const,
                label: t("nav.matter"),
                matches: ["matter"] as RouteKind[],
              },
            ],
          },
        ]
      : []),
    {
      label: t("sidebar.cluster.system"),
      items: [
        {
          href: "#/firmware",
          icon: "mdi:upload",
          label: t("nav.firmware"),
          matches: ["firmware"],
        },
        ...(gates.isAdmin
          ? [
              {
                href: "#/backups",
                icon: "mdi:server" as const,
                label: t("nav.backups"),
                matches: ["backups"] as RouteKind[],
              },
            ]
          : []),
        {
          href: "#/settings",
          icon: "mdi:settings",
          label: t("nav.settings"),
          matches: ["settings"],
        },
        {
          href: "#/about",
          icon: "mdi:information-outline",
          label: t("nav.about"),
          matches: ["about"],
        },
      ],
    },
  ];
}

/**
 * Views that were folded into another view, mapped to what absorbed
 * them. The keys stay resolvable for good: a bookmark, a shared link or
 * a stored start route naming a folded view is rewritten to its
 * successor instead of falling through to the not-found page.
 */
const FOLDED_ROUTES: Record<string, string> = {
  "/access": "/settings?tab=users",
  "/visibility": "/settings?tab=visibility",
};

/**
 * The successor of a folded route, or null when the route was never
 * folded. Accepts both the hash form used by navigation and stored
 * preferences (`#/access`) and the bare path the router works on
 * (`/access`), and answers in the same form it was asked in.
 */
export function foldedRouteTarget(route: string): string | null {
  const hashed = route.startsWith("#");
  const bare = hashed ? route.slice(1) : route;
  const target = FOLDED_ROUTES[bare.split("?")[0]];
  if (!target) return null;
  return hashed ? `#${target}` : target;
}

/**
 * The routes that can serve as a landing page, flattened from the
 * navigation with the same gates applied. Detail routes are excluded by
 * construction - they are not in the navigation because they need an
 * entity id, and a landing page must be reachable without one.
 */
export function landingTargets(gates: NavGates): { href: string; label: string }[] {
  return navClusters(gates).flatMap((c) =>
    c.items.map((i) => ({ href: i.href, label: `${c.label} · ${i.label}` })),
  );
}

/**
 * Whether a route is offered as a landing page under the given gates.
 * Used to build the selector, so the operator can only pick a view they
 * can actually reach right now.
 */
export function isValidLandingRoute(route: string, gates: NavGates): boolean {
  return landingTargets(gates).some((tgt) => tgt.href === route);
}

/**
 * Whether a route names a view that exists at all, ignoring the gates.
 *
 * This is deliberately the weaker check, and it is the one applied when a
 * stored start route is honoured on load. The gates depend on stores that
 * are still resolving during the first paint (capabilities, Matter status,
 * the identity's role), so gating there would be a race: a perfectly valid
 * start route would be discarded purely because its capability had not
 * arrived yet. A view that is genuinely unavailable renders its own empty
 * or error state, which is more honest than silently sending the operator
 * somewhere they did not ask for. Only a route that no longer exists at
 * all - a view removed in an update - falls back to the default.
 */
export function isKnownLandingRoute(route: string): boolean {
  return isValidLandingRoute(route, {
    matterEnabled: true,
    historyEnabled: true,
    isAdmin: true,
  });
}
