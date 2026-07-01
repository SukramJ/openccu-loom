// Central icon registry. We ship Lucide icons (already in
// `package.json` as @lucide/svelte) but keep a name dispatcher so
// templates use a single import instead of per-file Lucide picks.
//
// The naming follows Home-Assistant's MDI vocabulary where it makes
// sense (mdi:cog, mdi:signal, mdi:battery-alert, …) so a reader who
// knows HA can predict what each call resolves to. A small adapter
// layer maps those mdi-style keys to the Lucide equivalents — Lucide
// is what we have in the bundle.

import type { Component } from "svelte";
import {
  Activity,
  AlertCircle,
  AlertTriangle,
  ArrowLeft,
  ArrowRight,
  BarChart3,
  Battery,
  BatteryWarning,
  Bell,
  BellOff,
  Box,
  Calendar,
  CalendarClock,
  Check,
  CheckCircle2,
  ChevronDown,
  ChevronRight,
  ChevronUp,
  CircleAlert,
  Cloud,
  CloudRain,
  Clipboard,
  Cog,
  Copy,
  DoorClosed,
  Download,
  Droplets,
  Eye,
  EyeOff,
  FileDown,
  FileUp,
  Filter,
  Flame,
  Gauge,
  Globe,
  Grid3x3,
  Hand,
  Hash,
  History,
  Home,
  Info,
  KeyRound,
  LineChart,
  Lightbulb,
  Link as LinkIcon,
  List,
  ListChecks,
  Lock,
  LogOut,
  Menu,
  MoreVertical,
  MousePointerClick,
  Move,
  Network,
  Palette,
  Pencil,
  Percent,
  Play,
  Plus,
  Power,
  RefreshCw,
  Ruler,
  Save,
  Search,
  Server,
  Settings,
  Shield,
  ShieldAlert,
  Signal,
  SlidersHorizontal,
  Sun,
  Moon,
  Thermometer,
  Timer,
  Trash2,
  Triangle,
  Type as TypeIcon,
  Unlock,
  Upload,
  Volume2,
  Waves,
  Wifi,
  WifiOff,
  Wind,
  X,
  XCircle,
  Star,
  Zap,
} from "@lucide/svelte";

// IconName uses an HA-style namespace (`mdi:`) so call-sites read like
// HA templates. The mapping below is deliberately small — we only add
// what the SPA actually consumes.
export type IconName =
  | "mdi:alert"
  | "mdi:alert-circle"
  | "mdi:alert-triangle"
  | "mdi:arrow-left"
  | "mdi:arrow-right"
  | "mdi:battery"
  | "mdi:battery-alert"
  | "mdi:bell"
  | "mdi:bell-off"
  | "mdi:calendar"
  | "mdi:calendar-clock"
  | "mdi:check"
  | "mdi:check-circle"
  | "mdi:chevron-down"
  | "mdi:chevron-right"
  | "mdi:chevron-up"
  | "mdi:close"
  | "mdi:close-circle"
  | "mdi:clipboard"
  | "mdi:cog"
  | "mdi:content-copy"
  | "mdi:door-closed"
  | "mdi:download"
  | "mdi:export"
  | "mdi:filter"
  | "mdi:gauge"
  | "mdi:globe"
  | "mdi:history"
  | "mdi:home"
  | "mdi:import"
  | "mdi:information-outline"
  | "mdi:key"
  | "mdi:lightbulb"
  | "mdi:link"
  | "mdi:list-checks"
  | "mdi:lock"
  | "mdi:lock-open"
  | "mdi:logout"
  | "mdi:menu"
  | "mdi:more-vert"
  | "mdi:pencil"
  | "mdi:play"
  | "mdi:plus"
  | "mdi:power"
  | "mdi:refresh"
  | "mdi:save"
  | "mdi:search"
  | "mdi:server"
  | "mdi:server-network"
  | "mdi:settings"
  | "mdi:shield"
  | "mdi:signal"
  | "mdi:sliders"
  | "mdi:sun"
  | "mdi:moon"
  | "mdi:thermometer"
  | "mdi:trash-can"
  | "mdi:upload"
  | "mdi:visible"
  | "mdi:hidden"
  | "mdi:volume"
  | "mdi:wifi"
  | "mdi:wifi-off"
  | "mdi:radio-tower"
  | "mdi:waveform"
  | "mdi:text-box-search-outline"
  | "mdi:bell-alert"
  | "mdi:cube-outline"
  | "mdi:dots-grid"
  | "mdi:door"
  | "mdi:format-list-bulleted"
  | "mdi:gesture-tap-button"
  | "mdi:run-fast"
  | "mdi:smoke-detector-variant"
  | "mdi:water-alert"
  | "mdi:weather-windy"
  | "mdi:star"
  | "mdi:star-outline"
  | "mdi:zap";

const REGISTRY: Record<IconName, Component> = {
  "mdi:alert": AlertCircle,
  "mdi:alert-circle": CircleAlert,
  "mdi:alert-triangle": AlertTriangle,
  "mdi:arrow-left": ArrowLeft,
  "mdi:arrow-right": ArrowRight,
  "mdi:battery": Battery,
  "mdi:battery-alert": BatteryWarning,
  "mdi:bell": Bell,
  "mdi:bell-off": BellOff,
  "mdi:calendar": Calendar,
  "mdi:calendar-clock": CalendarClock,
  "mdi:check": Check,
  "mdi:check-circle": CheckCircle2,
  "mdi:chevron-down": ChevronDown,
  "mdi:chevron-right": ChevronRight,
  "mdi:chevron-up": ChevronUp,
  "mdi:close": X,
  "mdi:close-circle": XCircle,
  "mdi:clipboard": Clipboard,
  "mdi:cog": Cog,
  "mdi:content-copy": Copy,
  "mdi:door-closed": DoorClosed,
  "mdi:download": Download,
  "mdi:export": FileDown,
  "mdi:filter": Filter,
  "mdi:gauge": Gauge,
  "mdi:globe": Globe,
  "mdi:history": History,
  "mdi:home": Home,
  "mdi:import": FileUp,
  "mdi:information-outline": Info,
  "mdi:key": KeyRound,
  "mdi:lightbulb": Lightbulb,
  "mdi:link": LinkIcon,
  "mdi:list-checks": ListChecks,
  "mdi:lock": Lock,
  "mdi:lock-open": Unlock,
  "mdi:logout": LogOut,
  "mdi:menu": Menu,
  "mdi:more-vert": MoreVertical,
  "mdi:pencil": Pencil,
  "mdi:play": Play,
  "mdi:plus": Plus,
  "mdi:power": Power,
  "mdi:refresh": RefreshCw,
  "mdi:save": Save,
  "mdi:search": Search,
  "mdi:server": Server,
  "mdi:server-network": Network,
  "mdi:settings": Settings,
  "mdi:shield": Shield,
  "mdi:signal": Signal,
  "mdi:sliders": SlidersHorizontal,
  "mdi:sun": Sun,
  "mdi:moon": Moon,
  "mdi:thermometer": Thermometer,
  "mdi:trash-can": Trash2,
  "mdi:upload": Upload,
  "mdi:visible": Eye,
  "mdi:hidden": EyeOff,
  "mdi:volume": Volume2,
  "mdi:wifi": Wifi,
  "mdi:wifi-off": WifiOff,
  "mdi:radio-tower": Wifi,
  "mdi:waveform": Activity,
  "mdi:text-box-search-outline": List,
  "mdi:zap": Zap,
  "mdi:star": Star,
  "mdi:star-outline": Star,
  // Device-type + view-toggle glyphs (also present in LOOSE_REGISTRY,
  // promoted here so the strict <Icon> component renders them instead of
  // falling back to Info).
  "mdi:bell-alert": Bell,
  "mdi:cube-outline": Box,
  "mdi:door": DoorClosed,
  "mdi:dots-grid": Grid3x3,
  "mdi:format-list-bulleted": List,
  "mdi:gesture-tap-button": MousePointerClick,
  "mdi:run-fast": Activity,
  "mdi:smoke-detector-variant": AlertOctagonReplacement(),
  "mdi:water-alert": Droplets,
  "mdi:weather-windy": Wind,
};

export function resolveIcon(name: IconName): Component {
  return REGISTRY[name] ?? Info;
}

// Loose-typed icon registry for backend-driven mdi tokens (e.g.
// `hmui.HintFor` ships `mdi:water-percent`). Same Component values as
// REGISTRY, just keyed by `string` so the AutoTile composer can pass
// the backend's raw hint through without a parallel TS classifier.
// Unknown tokens fall back to a generic Gauge.
const LOOSE_REGISTRY: Record<string, Component> = {
  ...REGISTRY,
  // Motion / presence / occupancy
  "mdi:run-fast": Activity,
  "mdi:account-eye": Eye,
  "mdi:account-eye-outline": Eye,
  // Climate / sensors
  "mdi:thermometer-check": Thermometer,
  "mdi:thermometer-lines": Thermometer,
  "mdi:water-thermometer": Thermometer,
  "mdi:water-percent": Droplets,
  "mdi:water-opacity": Droplets,
  "mdi:water-alert": Droplets,
  "mdi:weather-sunny": Sun,
  "mdi:brightness-6": Sun,
  "mdi:weather-pouring": CloudRain,
  "mdi:weather-windy": Wind,
  "mdi:smog": Cloud,
  "mdi:smoke-detector-variant": AlertOctagonReplacement(),
  "mdi:molecule-co2": Cloud,
  // Electrical
  "mdi:flash": Zap,
  "mdi:flash-outline": Zap,
  "mdi:current-ac": Zap,
  "mdi:sine-wave": Activity,
  // Counts / values
  "mdi:counter": Hash,
  "mdi:numeric": Hash,
  "mdi:percent": Percent,
  "mdi:dots-grid": Grid3x3,
  // Containers / shapes
  "mdi:cube-outline": Box,
  "mdi:cup-water": Droplets,
  "mdi:angle-acute": Triangle,
  "mdi:ruler": Ruler,
  "mdi:axis-arrow": Move,
  "mdi:waves-arrow-right": Waves,
  // UI / state
  "mdi:circle": CircleAlert,
  "mdi:circle-outline": CircleAlert,
  "mdi:bell-alert": Bell,
  "mdi:shield-alert": ShieldAlert,
  "mdi:lan-disconnect": WifiOff,
  "mdi:radio-tower": Wifi,
  "mdi:ip-network": Server,
  "mdi:cog-clockwise": Cog,
  "mdi:speedometer": Gauge,
  "mdi:speedometer-medium": Gauge,
  // Devices
  "mdi:door": DoorClosed,
  "mdi:window-open-variant": Box,
  "mdi:valve": Gauge,
  "mdi:palette": Palette,
  "mdi:fire": Flame,
  "mdi:fire-circle": Flame,
  // Buttons / gestures
  "mdi:gesture-tap": Hand,
  "mdi:gesture-tap-button": MousePointerClick,
  "mdi:gesture-tap-hold": Hand,
  // Misc
  "mdi:timer-outline": Timer,
  "mdi:restart": RefreshCw,
  "mdi:chart-bar": BarChart3,
  "mdi:chart-line-variant": LineChart,
  "mdi:format-list-bulleted": List,
  "mdi:text": TypeIcon,
};

// AlertOctagon is not exported under that name in every lucide
// version — fall back to AlertTriangle. Helper keeps the LOOSE_REGISTRY
// readable as a single literal.
function AlertOctagonReplacement(): Component {
  return AlertTriangle;
}

/**
 * Looks up the lucide component for an arbitrary mdi-style token.
 * Used by the AutoTile composer to render backend-supplied
 * `ui_hint.icon` values without going through the strict IconName
 * union. Unknown tokens render as a generic Gauge so the tile never
 * crashes.
 */
export function resolveIconLoose(name: string | undefined): Component {
  if (!name) return Gauge;
  return LOOSE_REGISTRY[name] ?? Gauge;
}

// Convenience: domain-driven icon picker. Used by the device-list and
// the quick-control tab to assign icons to a homematic channel-type.
export function domainIcon(
  domain:
    | "switch"
    | "light"
    | "cover"
    | "climate"
    | "lock"
    | "valve"
    | "siren"
    | string
    | null
    | undefined,
): IconName {
  switch (domain) {
    case "switch":
      return "mdi:power";
    case "light":
      return "mdi:lightbulb";
    case "cover":
      return "mdi:sliders";
    case "climate":
      return "mdi:thermometer";
    case "lock":
      return "mdi:lock";
    case "valve":
      return "mdi:gauge";
    case "siren":
      return "mdi:volume";
    default:
      return "mdi:cog";
  }
}
