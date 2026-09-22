// Original Wuu artwork on a 24-unit grid. Open contours, soft corners and
// occasional solid capsules keep small controls legible beside the Wuu face.
// This source is shared by React controls and the main-process browser overlay.
export type IconNode = readonly [
  tag: "path" | "rect" | "circle" | "ellipse",
  attributes: Readonly<Record<string, string | number>>,
];

const path = (d: string, solid = false): IconNode => ["path", {
  d, ...(solid ? { fill: "currentColor", stroke: "none" } : {}),
}];
const rect = (x: number, y: number, width: number, height: number, rx = 2.5, solid = false): IconNode => ["rect", {
  x, y, width, height, rx, ...(solid ? { fill: "currentColor", stroke: "none" } : {}),
}];
const circle = (cx: number, cy: number, r: number, solid = false): IconNode => ["circle", {
  cx, cy, r, ...(solid ? { fill: "currentColor", stroke: "none" } : {}),
}];
const rotate = (nodes: readonly IconNode[], angle: number): IconNode[] => nodes.map(([tag, attrs]) => [
  tag, { ...attrs, transform: `rotate(${angle} 12 12)` },
]);
// Branching/dense silhouettes need more breathing room than an open contour.
const inset = (nodes: readonly IconNode[], scale: number): IconNode[] => nodes.map(([tag, attrs]) => [
  tag, { ...attrs, transform: `translate(12 12) scale(${scale}) translate(-12 -12)` },
]);

const frame = rect(3.5, 3.5, 17, 17, 4.5);
const ring = circle(12, 12, 9);
const check = path("m4.5 12 5.5 5.5L20 6.5");
const containedCheck = path("m7.5 12 3.3 3.5 5.7-7");
const plus = path("M12 3.5v17M3.5 12h17");
const cross = path("m5 5 14 14M19 5 5 19");
const arrow = [path("M12 20V4m-6 6 6-6 6 6")];
const chevron = [path("m8.5 5.5 6.5 6.5-6.5 6.5")];
const folder = path("M3 9V7c0-2 1-3 3-3h2.2c1 0 1.5.3 2.2 1l1.1 1.1c.6.6 1.2.9 2.3.9H17c2.8 0 4 1.4 4 4v5c0 2.6-1.4 4-4 4H7c-2.6 0-4-1.4-4-4V9Zm.5 0h7");
const file = path("M13 3H8C5.6 3 4.5 4.3 4.5 6.5v11c0 2.2 1.1 3.5 3.5 3.5h8c2.4 0 3.5-1.3 3.5-3.5V9L13 3Zm0 0v3.5C13 8.2 13.8 9 15.5 9h4");
const chat = path("M9 4h6c3.8 0 6 2.2 6 6v3c0 3.8-2.2 6-6 6H9l-4.5 2 .7-4.2C3.7 15.6 3 14.3 3 12v-2c0-3.8 2.2-6 6-6Z");
const search = [path("M10.5 3.5c4.1 0 7 2.9 7 7s-2.9 7-7 7-7-2.9-7-7 2.9-7 7-7ZM16 16l4.5 4.5")];
const monitor = [rect(3, 3.5, 18, 13, 3.5), path("M12 16.5v4M8 20.5h8")];
const shield = path("M12 3c2.7 2 5.3 2.5 8 3v5c0 5-3 8.3-8 10-5-1.7-8-5-8-10V6c2.7-.5 5.3-1 8-3Z");
const person = [circle(12, 7.5, 3.5), path("M4.5 21v-2c0-3.8 2.7-6 7.5-6s7.5 2.2 7.5 6v2")];
const terminal = [path("m4.5 6.5 5.5 5.5-5.5 5.5"), rect(12.5, 15.5, 8, 2.5, 1.25, true)];
const archive = [rect(3, 3.5, 18, 5, 2), path("M4.5 8.5V16c0 3 1.5 4.5 4.5 4.5h6c3 0 4.5-1.5 4.5-4.5V8.5M9 12.5h6")];
const sliders = [path("M7 3v4m0 6v8M17 3v9m0 6v3"), rect(4, 7, 6, 6, 2.5, true), rect(14, 12, 6, 6, 2.5, true)];
const clock = [ring, path("M12 6.5V12l4 2.5")];
const alert = [ring, path("M12 7v5"), circle(12, 16, 1, true)];
const triangleAlert = [path("M9.6 4.8c1.1-2 3.7-2 4.8 0l6.2 11.7c1.1 2.1 0 4-2.4 4H5.8c-2.4 0-3.5-1.9-2.4-4Z"), path("M12 8.5v4"), circle(12, 16.5, 1, true)];
const ellipsis = [circle(5, 12, 1.5, true), circle(12, 12, 1.5, true), circle(19, 12, 1.5, true)];
const loader = [path("M20.5 12a8.5 8.5 0 1 1-8.5-8.5")];
const pencil = [path("m4 20 1-5L16.5 3.5a2.8 2.8 0 0 1 4 4L9 19ZM14.5 5.5l4 4")];
const squarePen = [path("M12.5 4H9C5.7 4 4 5.7 4 9v6c0 3.3 1.7 5 5 5h6c3.3 0 5-1.7 5-5v-3.5"), path("m10 14 .8-3.6 7.3-7.3a1.8 1.8 0 0 1 2.5 2.5l-7.3 7.3Z", true)];
const branch = [path("M6 7v10m12-10v2c0 3-2 4-5 4H6"), circle(6, 4.5, 2.5), circle(6, 19.5, 2.5), circle(18, 4.5, 2.5)];
const commit = [path("M3 12h5m8 0h5"), circle(12, 12, 4)];
const globe = [ring, path("M3 12h18M12 3c-3.8 4.4-3.8 13.6 0 18 3.8-4.4 3.8-13.6 0-18Z")];
const blocks = [rect(3.5, 3.5, 7, 7), rect(3.5, 13.5, 7, 7), rect(13.5, 13.5, 7, 7), rect(14, 3, 7, 7, 2.5, true)];
const eye = [path("M2.5 12C5 7.5 8 5 12 5s7 2.5 9.5 7C19 16.5 16 19 12 19s-7-2.5-9.5-7Z"), circle(12, 12, 3)];
const pin = [path("M8 3.5h8l-.8 6.5 2.6 2.5c.6.6.9 1.2.9 2H5.3c0-.8.3-1.4.9-2L8.8 10Z", true), path("M12 15V21")];
const code = [path("m7.5 6-5 6 5 6m9-12 5 6-5 6M14 4l-4 16")];
const plug = [path("M8 3v5m8-5v5M5 8h14v3a7 7 0 0 1-14 0V8Zm7 10v3")];
const panelLeft = [frame, rect(6.5, 6.5, 3.5, 11, 1.75, true)];
const panelRight = rotate(panelLeft, 180);
const panelChevron = (right: boolean, pointsRight: boolean): IconNode[] => [
  frame, path(right ? "M14 4v16" : "M10 4v16"),
  path(pointsRight ? (right ? "m6 9 3 3-3 3" : "m14 9 3 3-3 3") : (right ? "m9 9-3 3 3 3" : "m17 9-3 3 3 3")),
];

export const iconArtwork = {
  Activity: [path("M2.5 12h4L10 4l4 16 3.5-8h4")],
  AlertCircle: alert,
  AlertTriangle: triangleAlert,
  Archive: archive,
  ArrowDown: rotate(arrow, 180),
  ArrowLeft: rotate(arrow, -90),
  ArrowRight: rotate(arrow, 90),
  ArrowUp: arrow,
  ArrowUpRight: [path("M5 19 19 5M8 5h11v11")],
  BarChart3: [rect(4, 12, 3, 9, 1.5, true), rect(10.5, 3, 3, 18, 1.5, true), rect(17, 8, 3, 13, 1.5, true)],
  Bell: [path("M18.5 10c0-4-2.1-6.5-6.5-6.5S5.5 6 5.5 10v3c0 1.5-.5 2.5-2 4h17c-1.5-1.5-2-2.5-2-4ZM9.5 20c1.4 1.3 3.6 1.3 5 0")],
  Blocks: blocks,
  Bookmark: [path("M8 3h8c2 0 3 1 3 3v15l-7-4-7 4V6c0-2 1-3 3-3Z")],
  BookOpen: [path("M12 6.5C8.5 4 6.5 3.5 3 4v15c3.5-.5 5.5 0 9 2 3.5-2 5.5-2.5 9-2V4c-3.5-.5-5.5 0-9 2.5Zm0 0V21")],
  Bot: [path("M12 3c5.3 0 9 3.7 9 9s-3.7 9-9 9-9-3.7-9-9 3.7-9 9-9Z"), rect(7.8, 8, 2.5, 7, 1.25, true), rect(13.7, 8, 2.5, 7, 1.25, true)],
  Brain: inset([path("M12 5c-2-4-7-2.5-7 1.5-4 1.5-3.5 6.5-.5 8-1 4.5 4.5 7.5 7.5 4 3 3.5 8.5.5 7.5-4 3-1.5 3.5-6.5-.5-8C19 2.5 14 1 12 5Zm0 0v13.5M6 10c2 0 3 1 3 3m9-3c-2 0-3 1-3 3")], 0.9),
  Bug: [rect(7, 7, 10, 13, 5), path("m8 3 2 4m6-4-2 4M3 9l4 2m10 0 4-2M3 16h4m10 0h4M5 22l3-3m8 0 3 3M12 12v8")],
  CalendarDays: [rect(3.5, 5, 17, 16, 3.5), path("M7 3v4m10-4v4M4 10h16"), circle(8, 14, 1, true), circle(13, 14, 1, true), circle(8, 18, 1, true), circle(17, 18, 1, true)],
  Camera: [path("M8 6.5 9.5 4h5L16 6.5h2c2 0 3 1 3 3v8c0 2-1 3-3 3H6c-2 0-3-1-3-3v-8c0-2 1-3 3-3Z"), circle(12, 13, 4)],
  Check: [check],
  ChevronDown: rotate(chevron, 90),
  ChevronLeft: rotate(chevron, 180),
  ChevronRight: chevron,
  ChevronUp: rotate(chevron, -90),
  ChevronsDown: [path("m7 4 5 5 5-5M7 14l5 5 5-5")],
  ChevronsUpDown: [path("m8 8 4-4 4 4m-8 8 4 4 4-4")],
  Circle: [ring],
  CircleAlert: alert,
  CircleCheck: [ring, containedCheck],
  CircleDot: [ring, circle(12, 12, 4, true)],
  CircleHelp: [ring, path("M9 8c0-3 6-3 6 0 0 2-3 2-3 5"), circle(12, 16.5, 1, true)],
  ClipboardList: [path("M8 5H6.5C4.8 5 4 6 4 8v10c0 2 1 3 3 3h10c2 0 3-1 3-3V8c0-2-.8-3-2.5-3H16"), rect(8, 2.5, 8, 5, 2), path("M9 12h7m-7 4h5")],
  Clock: clock,
  Clock3: clock,
  Code2: code,
  Copy: [rect(8, 8, 13, 13, 3.5), path("M16 4H7C4.3 4 3 5.3 3 8v8")],
  CornerDownRight: [path("M5 4v7c0 3 1.5 4.5 4.5 4.5H20m-5-5 5 5-5 5")],
  CornerUpLeft: [path("M19 20v-7c0-3-1.5-4.5-4.5-4.5H4m5 5-5-5 5-5")],
  Cpu: [rect(6, 6, 12, 12, 3), rect(9, 9, 6, 6, 1.5, true), path("M9 2.5V6m6-3.5V6M9 18v3.5m6-3.5v3.5M2.5 9H6m-3.5 6H6m12-6h3.5M18 15h3.5")],
  Database: [["ellipse", { cx: 12, cy: 6, rx: 8, ry: 3 }], path("M4 6v12c0 4 16 4 16 0V6M4 12c0 4 16 4 16 0")],
  Download: [path("M12 3v12m-5-5 5 5 5-5M4 16v2c0 2 1 3 3 3h10c2 0 3-1 3-3v-2")],
  Ellipsis: ellipsis,
  ExternalLink: [path("M13 3.5h7.5V11M20 4 11 13M9 4H7C4.5 4 3.5 5.5 3.5 8v9c0 2.5 1.5 3.5 4 3.5H16c2.5 0 4-1 4-3.5v-2")],
  Eye: eye,
  EyeOff: [path("M4 4 20 20M8 5.8A9 9 0 0 1 12 5c4 0 7 2.5 9.5 7l-2 3M5 7.5 2.5 12C5 16.5 8 19 12 19a9 9 0 0 0 4-.8M9 9a4.3 4.3 0 0 0 6 6")],
  FileDiff: [file, path("M8 12h5m-2.5-2.5v5M8 18h7")],
  FilePlus: [file, path("M8 15h8m-4-4v8")],
  FileText: [file, path("M8 13h7m-7 4h5")],
  FileX: [file, path("m9 12 6 6m0-6-6 6")],
  Film: [rect(3, 3, 18, 18, 3.5), path("M8 3v18M16 3v18M3 8h5m-5 8h5m8-8h5m-5 8h5")],
  FlaskConical: [path("M8.5 3h7M10 3v6L4.5 18c-1 1.5 0 3 2 3h11c2 0 3-1.5 2-3L14 9V3M7 14h10")],
  FoldVertical: [path("M12 3v5m-4-4 4 4 4-4M12 21v-5m-4 4 4-4 4 4M4 12h16")],
  Folder: [folder],
  FolderMinus: [folder, path("M9 14.5h6")],
  FolderOpen: [path("M3 15V7c0-2 1-3 3-3h3l3 3h5c2 0 3 1 3 3M4 20l2-8h15l-2 8Z")],
  FolderPlus: [folder, path("M9 14.5h6m-3-3v6")],
  FolderX: [folder, path("m9.5 12 5 5m0-5-5 5")],
  Gauge: [path("M5 19a9 9 0 1 1 14 0ZM12 5v2M5 12h2m10 0h2m-7 3 4-6"), circle(12, 15, 1.5, true)],
  GitBranch: inset(branch, 0.9),
  GitCommit: commit,
  GitCommitHorizontal: commit,
  GitCompare: [path("M5.5 16V4m-3 3 3-3 3 3m10 1v12m-3-3 3 3 3-3"), circle(5.5, 19, 2.5), circle(18.5, 5, 2.5)],
  GitPullRequest: inset([path("M5.5 8v8M18.5 16V9c0-3-2-4-5-4h-2m3-3-3 3 3 3"), circle(5.5, 5, 2.5), circle(5.5, 19, 2.5), circle(18.5, 19, 2.5)], 0.9),
  Globe: globe,
  Globe2: globe,
  GripHorizontal: [circle(5, 8.5, 1.25, true), circle(12, 8.5, 1.25, true), circle(19, 8.5, 1.25, true), circle(5, 15.5, 1.25, true), circle(12, 15.5, 1.25, true), circle(19, 15.5, 1.25, true)],
  Hammer: [path("M7 3h9l5 5-4 4-4-4H9l-4 4V7ZM12 11l-8 8a2.1 2.1 0 0 0 3 3l8-8")],
  Hand: inset([path("M7 12V5a2 2 0 0 1 4 0v6m0-7a2 2 0 0 1 4 0v7m0-5a2 2 0 0 1 4 0v8m0-3a2 2 0 0 1 4 0v3c0 5-3 8-8 8h-1c-3 0-4-1-6-3l-4-5c-1.5-2 1-4 2.5-2L7 15")], 0.87),
  Hash: [path("M9 3 7 21M17 3l-2 18M4 8h17M3 16h17")],
  ImagePlus: [path("M12 4H8C5 4 3.5 5.5 3.5 8.5V16c0 3 1.5 4.5 4.5 4.5h8c3 0 4.5-1.5 4.5-4.5v-4M4 17l5-5 8 8M16 6h6m-3-3v6"), circle(8.5, 8, 1.25, true)],
  Images: [rect(7, 7, 14, 14, 3.5), path("M16 3H7C4.3 3 3 4.3 3 7v9M8 17l4-4 8 7"), circle(16.5, 11, 1.25, true)],
  Inbox: [path("M3 12 6 4h12l3 8v5c0 3-1.5 4-4 4H7c-2.5 0-4-1-4-4Zm0 0h5l1.5 3h5l1.5-3h5")],
  Info: [ring, path("M12 11v6"), circle(12, 7.5, 1, true)],
  KeyRound: [circle(8, 8, 5), path("m11.5 11.5 9 9M16 16l3-3m0 6 3-3")],
  Laptop: [rect(5, 3.5, 14, 13, 3), path("M5 16.5 2.5 21h19L19 16.5")],
  Layers: [path("m12 3 9 5-9 5-9-5Zm-9 9 9 5 9-5M3 16l9 5 9-5")],
  LayoutDashboard: [rect(3, 3, 7, 10), rect(14, 3, 7, 6), rect(3, 17, 7, 4, 2), rect(14, 13, 7, 8)],
  LayoutGrid: [rect(3.5, 3.5, 7, 7), rect(13.5, 3.5, 7, 7), rect(3.5, 13.5, 7, 7), rect(13.5, 13.5, 7, 7)],
  List: [path("M9 5h12M9 12h12M9 19h12"), circle(3.5, 5, 1, true), circle(3.5, 12, 1, true), circle(3.5, 19, 1, true)],
  ListTodo: [path("m3 5 2 2 3-4M12 6h9m-18 9 2 2 3-4m4 3h9")],
  ListTree: [path("M4 3v14c0 2 1 3 3 3h3M4 8h6M14 5h7m-7 6h7m-7 6h7")],
  Loader2: loader,
  LoaderCircle: loader,
  Lock: [rect(5, 10, 14, 11, 3.5), path("M8 10V6a4 4 0 0 1 8 0v4"), rect(11, 14, 2, 3.5, 1, true)],
  LogOut: [path("M10 3H7C4.3 3 3 4.3 3 7v10c0 2.7 1.3 4 4 4h3M11 12h10m-5-5 5 5-5 5")],
  Mail: [rect(3, 4.5, 18, 15, 3.5), path("m4 6 8 7 8-7")],
  Maximize2: [path("M14 3.5h6.5V10M20 4l-6 6M10 20.5H3.5V14M4 20l6-6")],
  MessageCircle: [chat, path("M8 10h8M8 14h5")],
  MessageSquare: [chat, path("M8 10h8M8 14h5")],
  MessageSquarePlus: [chat, path("M8.5 11.5h7m-3.5-3.5v7")],
  MessagesSquare: [path("M17 4H8C5 4 3.5 5.5 3.5 8.5V14L7 12.5h8c3 0 4.5-1.5 4.5-4.5S19 4 17 4ZM8 16.5h8L20.5 20v-8")],
  Minimize2: [path("M20.5 10H14V3.5M20 4l-6 6M3.5 14H10v6.5M4 20l6-6")],
  Minus: [path("M4 12h16")],
  Monitor: monitor,
  Moon: [path("M10 3a9 9 0 1 0 11 11C12 17 7 11 10 3Z")],
  MoreHorizontal: ellipsis,
  Network: inset([path("M12 8v5M5 16v-3h14v3"), rect(9, 2.5, 6, 5.5, 2), rect(2, 16, 6, 5.5, 2), rect(16, 16, 6, 5.5, 2)], 0.88),
  NotebookPen: [path("M12 3H7C4.5 3 3.5 4 3.5 6.5v11c0 2.5 1 3.5 3.5 3.5h10M7 3v18"), path("m11 15 1-4 7-7a2.1 2.1 0 0 1 3 3l-7 7Z", true)],
  PackagePlus: [path("m12 3 9 5-9 5-9-5Zm-9 5v10l9 4v-9m9-5v4M7.5 5.5l9 5M16 18h6m-3-3v6")],
  PanelLeft: panelLeft,
  PanelLeftClose: panelChevron(false, false),
  PanelLeftOpen: panelChevron(false, true),
  PanelRightClose: panelChevron(true, true),
  PanelRightOpen: panelChevron(true, false),
  Paperclip: [path("m8.5 12.5 6-6c3-3 7 1 4 4l-8 8c-5 5-11-1-6-6l8-8c2-2 4-2 6-1")],
  Pencil: pencil,
  PencilLine: [...pencil, path("M13 21h8")],
  PieChart: [path("M10 3.5a9 9 0 1 0 10.5 10.5H10ZM14 3v7h7c-.7-4-3-6.3-7-7Z")],
  Pin: pin,
  PinOff: [path("m3 3 18 18M10 4h5l-.6 6M7.5 12l-.7 2h5.7M12 16v5")],
  Plug: plug,
  PlugZap: [path("M7 3v5M4 8h11v3c0 4-2 6-5.5 6S4 15 4 11V8Zm5.5 9v4m10-18-3 6h5l-3 6")],
  Plus: [plus],
  Presentation: [rect(3, 3.5, 18, 13, 3), path("M12 16.5v5m-5 0h10M7 12l3-4 3 2 4-4")],
  Puzzle: [path("M9 4c0-3 6-3 6 0h5v5c-3 0-3 6 0 6v5h-5c0-3-6-3-6 0H4v-5c3 0 3-6 0-6V4Z")],
  RefreshCw: [path("M4 9a8.5 8.5 0 0 1 14.5-3L21 8M21 3v5h-5M20 15a8.5 8.5 0 0 1-14.5 3L3 16m0 5v-5h5")],
  RotateCcw: [path("M3 4v6h6M3.5 9a8.5 8.5 0 1 1 .5 7")],
  RotateCw: [path("M21 4v6h-6M20.5 9a8.5 8.5 0 1 0-.5 7")],
  ScrollText: [path("M6 8H3V6a3 3 0 0 1 6 0v12a3 3 0 0 0 6 0v-2h7v2c0 2-1 3-3 3H12M6 3h11c2 0 3 1 3 3v6M12 7h5m-5 4h5")],
  Search: search,
  Send: [path("m12 3 8.5 17-8.5-4-8.5 4Zm0 0v13")],
  Settings: sliders,
  Settings2: sliders,
  Shield: [shield],
  ShieldCheck: [shield, path("m8 11 3 3 5-5")],
  Shuffle: inset([path("M3 5h3c4 0 8 14 12 14h3m-4-4 4 4-4 3M3 19h3c1.5 0 3-2 4.5-4.5M14 8c1.5-2 2.5-3 4-3h3m-4-3 4 3-4 4")], 0.9),
  SlidersHorizontal: rotate(sliders, 90),
  Smartphone: [rect(6.5, 2.5, 11, 19, 3.5), path("M10 5h4"), rect(10, 17.5, 4, 1.75, .875, true)],
  Sparkles: inset([path("M10 4c0 5-2 7-7 7 5 0 7 2 7 7 0-5 2-7 7-7-5 0-7-2-7-7Z"), path("M19 2v5m-2.5-2.5h5M19 17v5m-2.5-2.5h5")], 0.9),
  Split: [path("M5 21V7c0-2 1-3 3-3h2m-3-2 3 2-3 3M5 16c0-4 4-4 7-4h3c3 0 4-2 4-5V3m-3 3 3-3 3 3")],
  Square: [frame],
  SquareCheck: [frame, containedCheck],
  SquarePen: squarePen,
  Terminal: terminal,
  Trash2: [path("M3 6h18M9 6V3h6v3M5 6l1 12c.2 2 1 3 3 3h6c2 0 2.8-1 3-3l1-12M9 10l.5 7m5.5-7-.5 7")],
  TriangleAlert: triangleAlert,
  UserRound: person,
  Users: [circle(9, 7, 3.5), path("M2.5 21v-2c0-4 2-6 6.5-6s6.5 2 6.5 6v2M16 3.5c5 0 5 7 0 7m2 3c3 .5 3.5 3 3.5 7.5")],
  Workflow: inset([path("M6 8v7c0 2 1 3 3 3h7M12 5h6v6"), rect(2.5, 2, 7, 6, 2), rect(15, 14, 7, 7, 2.5)], 0.9),
  Wrench: [path("m13 4 1 5 5 1 2-4c2 6-2 10-8 8l-6.5 6.5a2.1 2.1 0 0 1-3-3L10 11C8 5 12 1 18 3Z")],
  X: [cross],
  Zap: [path("m13.5 2-10 12h8l-1 8 10-12h-8Z", true)],
  ZoomIn: [...search, path("M7 10.5h7m-3.5-3.5v7")],
  CollabNodes: [path("m11 7-4.5 8m6.5-8 4.5 8M8 18h8"), circle(12, 4.5, 2.5, true), circle(5, 18, 2.5, true), circle(19, 18, 2.5, true)],
  PanelRight: panelRight,
} as const satisfies Record<string, readonly IconNode[]>;

export type IconName = keyof typeof iconArtwork;

/** Render trusted artwork into isolated main-process HTML without React. */
export function iconSVG(name: IconName, size: number): string {
  const nodes = iconArtwork[name].map(([tag, attributes]) => {
    const attrs = Object.entries(attributes).map(([key, value]) => `${key}="${value}"`).join(" ");
    return `<${tag} ${attrs}/>`;
  }).join("");
  return `<svg width="${size}" height="${size}" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.75" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">${nodes}</svg>`;
}
