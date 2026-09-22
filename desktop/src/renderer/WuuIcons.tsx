import { createElement, forwardRef, type SVGProps } from "react";
import { iconArtwork, type IconName } from "../shared/iconArtwork";

// Third-party identities keep their recognizable marks. Product controls use
// Wuu's own artwork; the GitHub mark is the only retained library asset.
export { Github } from "lucide-react";

export type IconProps = SVGProps<SVGSVGElement> & { size?: string | number };
export type IconComponent = ReturnType<typeof createIcon>;

function createIcon(name: IconName) {
  const slug = name.replace(/([a-z0-9])([A-Z])/g, "$1-$2").toLowerCase();
  const Icon = forwardRef<SVGSVGElement, IconProps>(function WuuIcon({
    size = 24, color = "currentColor", strokeWidth = 1.75, className = "", children, ...props
  }, ref) {
    return (
      <svg ref={ref} xmlns="http://www.w3.org/2000/svg" width={size} height={size}
        viewBox="0 0 24 24" fill="none" color={color} stroke={color} strokeWidth={strokeWidth}
        strokeLinecap="round" strokeLinejoin="round" focusable="false"
        aria-hidden={props["aria-label"] || props["aria-labelledby"] || children ? undefined : true}
        data-icon={slug} className={`wuu-icon wuu-icon-${slug}${className ? ` ${className}` : ""}`} {...props}>
        {iconArtwork[name].map(([tag, attributes], index) => createElement(tag, { ...attributes, key: index }))}
        {children}
      </svg>
    );
  });
  Icon.displayName = name;
  return Icon;
}

export const Activity = createIcon("Activity");
export const AlertCircle = createIcon("AlertCircle");
export const AlertTriangle = createIcon("AlertTriangle");
export const Archive = createIcon("Archive");
export const ArrowDown = createIcon("ArrowDown");
export const ArrowLeft = createIcon("ArrowLeft");
export const ArrowRight = createIcon("ArrowRight");
export const ArrowUp = createIcon("ArrowUp");
export const ArrowUpRight = createIcon("ArrowUpRight");
export const BarChart3 = createIcon("BarChart3");
export const Bell = createIcon("Bell");
export const Blocks = createIcon("Blocks");
export const Bookmark = createIcon("Bookmark");
export const BookOpen = createIcon("BookOpen");
export const Bot = createIcon("Bot");
export const Brain = createIcon("Brain");
export const Bug = createIcon("Bug");
export const CalendarDays = createIcon("CalendarDays");
export const Camera = createIcon("Camera");
export const Check = createIcon("Check");
export const ChevronDown = createIcon("ChevronDown");
export const ChevronLeft = createIcon("ChevronLeft");
export const ChevronRight = createIcon("ChevronRight");
export const ChevronUp = createIcon("ChevronUp");
export const ChevronsDown = createIcon("ChevronsDown");
export const ChevronsUpDown = createIcon("ChevronsUpDown");
export const Circle = createIcon("Circle");
export const CircleAlert = createIcon("CircleAlert");
export const CircleCheck = createIcon("CircleCheck");
export const CircleDot = createIcon("CircleDot");
export const CircleHelp = createIcon("CircleHelp");
export const ClipboardList = createIcon("ClipboardList");
export const Clock = createIcon("Clock");
export const Clock3 = createIcon("Clock3");
export const Code2 = createIcon("Code2");
export const Copy = createIcon("Copy");
export const CornerDownRight = createIcon("CornerDownRight");
export const CornerUpLeft = createIcon("CornerUpLeft");
export const Cpu = createIcon("Cpu");
export const Database = createIcon("Database");
export const Download = createIcon("Download");
export const Ellipsis = createIcon("Ellipsis");
export const ExternalLink = createIcon("ExternalLink");
export const Eye = createIcon("Eye");
export const EyeOff = createIcon("EyeOff");
export const FileDiff = createIcon("FileDiff");
export const FilePlus = createIcon("FilePlus");
export const FileText = createIcon("FileText");
export const FileX = createIcon("FileX");
export const Film = createIcon("Film");
export const FlaskConical = createIcon("FlaskConical");
export const FoldVertical = createIcon("FoldVertical");
export const Folder = createIcon("Folder");
export const FolderMinus = createIcon("FolderMinus");
export const FolderOpen = createIcon("FolderOpen");
export const FolderPlus = createIcon("FolderPlus");
export const FolderX = createIcon("FolderX");
export const Gauge = createIcon("Gauge");
export const GitBranch = createIcon("GitBranch");
export const GitCommit = createIcon("GitCommit");
export const GitCommitHorizontal = createIcon("GitCommitHorizontal");
export const GitCompare = createIcon("GitCompare");
export const GitPullRequest = createIcon("GitPullRequest");
export const Globe = createIcon("Globe");
export const Globe2 = createIcon("Globe2");
export const GripHorizontal = createIcon("GripHorizontal");
export const Hammer = createIcon("Hammer");
export const Hand = createIcon("Hand");
export const Hash = createIcon("Hash");
export const ImagePlus = createIcon("ImagePlus");
export const Images = createIcon("Images");
export const Inbox = createIcon("Inbox");
export const Info = createIcon("Info");
export const KeyRound = createIcon("KeyRound");
export const Laptop = createIcon("Laptop");
export const Layers = createIcon("Layers");
export const LayoutDashboard = createIcon("LayoutDashboard");
export const LayoutGrid = createIcon("LayoutGrid");
export const List = createIcon("List");
export const ListTodo = createIcon("ListTodo");
export const ListTree = createIcon("ListTree");
export const Loader2 = createIcon("Loader2");
export const LoaderCircle = createIcon("LoaderCircle");
export const Lock = createIcon("Lock");
export const LogOut = createIcon("LogOut");
export const Mail = createIcon("Mail");
export const Maximize2 = createIcon("Maximize2");
export const MessageCircle = createIcon("MessageCircle");
export const MessageSquare = createIcon("MessageSquare");
export const MessageSquarePlus = createIcon("MessageSquarePlus");
export const MessagesSquare = createIcon("MessagesSquare");
export const Minimize2 = createIcon("Minimize2");
export const Minus = createIcon("Minus");
export const Monitor = createIcon("Monitor");
export const Moon = createIcon("Moon");
export const MoreHorizontal = createIcon("MoreHorizontal");
export const Network = createIcon("Network");
export const NotebookPen = createIcon("NotebookPen");
export const PackagePlus = createIcon("PackagePlus");
export const PanelLeft = createIcon("PanelLeft");
export const PanelLeftClose = createIcon("PanelLeftClose");
export const PanelLeftOpen = createIcon("PanelLeftOpen");
export const PanelRightClose = createIcon("PanelRightClose");
export const PanelRightOpen = createIcon("PanelRightOpen");
export const Paperclip = createIcon("Paperclip");
export const Pencil = createIcon("Pencil");
export const PencilLine = createIcon("PencilLine");
export const PieChart = createIcon("PieChart");
export const Pin = createIcon("Pin");
export const PinOff = createIcon("PinOff");
export const Plug = createIcon("Plug");
export const PlugZap = createIcon("PlugZap");
export const Plus = createIcon("Plus");
export const Presentation = createIcon("Presentation");
export const Puzzle = createIcon("Puzzle");
export const RefreshCw = createIcon("RefreshCw");
export const RotateCcw = createIcon("RotateCcw");
export const RotateCw = createIcon("RotateCw");
export const ScrollText = createIcon("ScrollText");
export const Search = createIcon("Search");
export const Send = createIcon("Send");
export const Settings = createIcon("Settings");
export const Settings2 = createIcon("Settings2");
export const Shield = createIcon("Shield");
export const ShieldCheck = createIcon("ShieldCheck");
export const Shuffle = createIcon("Shuffle");
export const SlidersHorizontal = createIcon("SlidersHorizontal");
export const Smartphone = createIcon("Smartphone");
export const Sparkles = createIcon("Sparkles");
export const Split = createIcon("Split");
export const Square = createIcon("Square");
export const SquareCheck = createIcon("SquareCheck");
export const SquarePen = createIcon("SquarePen");
export const Terminal = createIcon("Terminal");
export const Trash2 = createIcon("Trash2");
export const TriangleAlert = createIcon("TriangleAlert");
export const UserRound = createIcon("UserRound");
export const Users = createIcon("Users");
export const Workflow = createIcon("Workflow");
export const Wrench = createIcon("Wrench");
export const X = createIcon("X");
export const Zap = createIcon("Zap");
export const ZoomIn = createIcon("ZoomIn");
export const CollabNodes = createIcon("CollabNodes");
export const PanelRight = createIcon("PanelRight");
