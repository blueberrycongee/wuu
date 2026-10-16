// Original Wuu artwork on a 24-unit grid. Open contours, soft corners and
// occasional solid capsules keep small controls legible beside the Wuu face.
// This source is shared by React controls and the main-process browser overlay.
export type IconNode = readonly [
  tag: "path" | "rect" | "circle" | "ellipse",
  attributes: Readonly<Record<string, string | number>>,
];

const p = (d: string): IconNode => ['path', {d}];
const r = (x: number, y: number, width: number, height: number, rx = 2.5): IconNode => ['rect',{x,y,width,height,rx}];
const c = (cx: number, cy: number, r: number): IconNode => ['circle',{cx,cy,r}];
const dot = (cx: number, cy: number, r = 1): IconNode => ['circle',{cx,cy,r,fill:'currentColor',stroke:'none'}];
const fill = (d: string): IconNode => ['path',{d,fill:'currentColor',stroke:'none'}];
const rot = (a: readonly IconNode[], n: number): IconNode[] => a.map(([t,v])=>[t,{...v,transform:`rotate(${n} 12 12)`}]);
const mirror = (a: readonly IconNode[]): IconNode[] => a.map(([t,v])=>[t,{...v,transform:'translate(24 0) scale(-1 1)'}]);
const ring = c(12,12,8.1);
const frame = r(4,4,16,16,3.2);
const check = p('m5.5 12 4.1 4.2 8.9-9');
const smallCheck = p('m8 12 2.7 2.8 5.4-5.6');
const arrow = [p('M12 19V5m-5.1 5.1L12 5l5.1 5.1')];
const chevron = [p('m9.1 6.5 5.2 5.5-5.2 5.5')];
const folder = p('M6.7 4.6h2.1c.8 0 1.4.3 1.9.9l1 1.2c.4.5 1 .8 1.7.8h4a2.9 2.9 0 0 1 2.9 2.9v6.2a2.9 2.9 0 0 1-2.9 2.9H6.7a2.9 2.9 0 0 1-2.9-2.9V7.5a2.9 2.9 0 0 1 2.9-2.9Z');
const file = p('M14 3.5H7.8a2.8 2.8 0 0 0-2.8 2.8v11.4a2.8 2.8 0 0 0 2.8 2.8h8.4a2.8 2.8 0 0 0 2.8-2.8V8.5L14 3.5Zm0 .5v3a1.5 1.5 0 0 0 1.5 1.5h3');
const chat = p('M7.1 4.3h9.8a3.1 3.1 0 0 1 3.1 3.1v6.7a3.1 3.1 0 0 1-3.1 3.1H10l-4.7 2.5V17A3 3 0 0 1 4 14.5V7.4a3.1 3.1 0 0 1 3.1-3.1Z');
const clock = [ring,p('M12 7v5.2l3.5 2')];
const alert = [ring,p('M12 7.3v5.5'),dot(12,16.2)];
const triangle = [p('m10 4.8-6.8 12A2.1 2.1 0 0 0 5 20h14a2.1 2.1 0 0 0 1.8-3.2l-6.8-12a2.3 2.3 0 0 0-4 0Z'),p('M12 9v4.2'),dot(12,16.5)];
const ellipsis = [dot(5.8,12,1.25),dot(12,12,1.25),dot(18.2,12,1.25)];
const search = [c(10.5,10.5,6.7),p('m15.5 15.5 4.5 4.5')];
const pencil = [p('m5 15.5 10.9-11a2.5 2.5 0 0 1 3.6 3.6l-11 10.9-4.2.7.7-4.2Zm9-9 3.5 3.5')];
const sliders = [p('M7.3 4v3m0 6v7M16.7 4v7m0 6v3'),r(4.8,7,5,6,2),r(14.2,11,5,6,2)];
const globe = [ring,p('M4.5 12h15M12 4.1c-2.1 2-3.2 4.6-3.2 7.9s1.1 5.9 3.2 7.9c2.1-2 3.2-4.6 3.2-7.9S14.1 6.1 12 4.1')];
const shield = p('M12 3.5c2.3 1.6 4.8 2.2 7.3 2.6v5.1c0 4.3-2.7 7.5-7.3 9.3-4.6-1.8-7.3-5-7.3-9.3V6.1c2.5-.4 5-1 7.3-2.6Z');
const eye = [p('M3 12c2.2-3.5 5.1-5.9 9-5.9s6.8 2.4 9 5.9c-2.2 3.5-5.1 5.9-9 5.9S5.2 15.5 3 12Z'),c(12,12,2.6)];
const panel = [r(3.5,4,17,16,3),p('M9.2 4.5v15')];
const panelArrow = (side: "left" | "right", open: boolean): IconNode[] => {
 const a: IconNode[] = [r(3.5,4,17,16,3),p('M9.2 4.5v15'),p(open?'m13.2 9.3 2.7 2.7-2.7 2.7':'m16 9.3-2.7 2.7 2.7 2.7')];
 return side==='right'?mirror(a):a;
};
const retry = [p('M5.6 7.8a7.6 7.6 0 1 1-1 6.2M5.6 3.7v4.1h4.1')];
const refresh = [p('M5.5 8a7.5 7.5 0 0 1 13-1M18.5 3.5V7H15'),p('M18.5 16a7.5 7.5 0 0 1-13 1M5.5 20.5V17H9')];
const loader = [p('M19.6 12a7.6 7.6 0 1 1-7.6-7.6')];
const commit = [p('M3.8 12h4.8m6.8 0h4.8'),c(12,12,3.4)];
const plug = [p('M8.3 3.8v4.7m7.4-4.7v4.7M5.5 8.5h13V11a6.5 6.5 0 0 1-13 0V8.5ZM12 17.5v2.7')];
const pin = [p('M8.2 4h7.6l-.6 6.2 2.7 3.1v1.6H6.1v-1.6l2.7-3.1L8.2 4ZM12 15.5v4.8')];
const squarePen = [p('M11.4 4H7.2A3.2 3.2 0 0 0 4 7.2v9.6A3.2 3.2 0 0 0 7.2 20h9.6a3.2 3.2 0 0 0 3.2-3.2v-4.2'),p('m11.2 10.8 6.4-6.4a1.7 1.7 0 0 1 2.4 2.4l-6.4 6.4-3.1.7.7-3.1Z')];
export const iconArtwork = {
 Activity:[p('M3.5 12h3.8l2.4-6.2 4.4 12.4 2.6-6.2h3.8')],
 AlertCircle:alert,AlertTriangle:triangle,
 Archive:[r(3.5,4.2,17,4.5,1.5),p('M5 9v7.9a3 3 0 0 0 3 3h8a3 3 0 0 0 3-3V9M9.5 12.5h5')],
 ArrowDown:rot(arrow,180),ArrowLeft:rot(arrow,-90),ArrowRight:rot(arrow,90),ArrowUp:arrow,
 ArrowUpRight:[p('M6 18 18 6M8.5 6H18v9.5')],
 BarChart3:[p('M5.2 19.5v-7m6.8 7V4.5m6.8 15V9'),p('M3.8 20h16.4')],
 Bell:[p('M6 10.3a6 6 0 0 1 12 0v3c0 1.3.7 2.5 1.8 3.7H4.2C5.3 15.8 6 14.6 6 13.3v-3ZM9.8 20h4.4')],
 Blocks:[r(4,4,6,6,1.8),r(4,14,6,6,1.8),r(14,14,6,6,1.8),p('m17 3.4 3.6 3.6-3.6 3.6L13.4 7 17 3.4Z')],
 Bookmark:[p('M8 3.8h8a2.5 2.5 0 0 1 2.5 2.5v14l-6.5-3.7-6.5 3.7v-14A2.5 2.5 0 0 1 8 3.8Z')],
 BookOpen:[p('M12 6.1c-2.7-1.4-5.2-1.9-8-1.3v13.4c2.8-.6 5.3-.1 8 1.3 2.7-1.4 5.2-1.9 8-1.3V4.8c-2.8-.6-5.3-.1-8 1.3Zm0 0v13.4')],
 // Bot is Wuu's ball identity, not a generic robot pictogram.
 Bot:[c(12,12,8.5),['rect',{x:8.1,y:8.5,width:2.3,height:6,rx:1.15,fill:'currentColor',stroke:'none'}],['rect',{x:13.6,y:8.5,width:2.3,height:6,rx:1.15,fill:'currentColor',stroke:'none'}]],
 Brain:[p('M12 5.3c-1.4-2.6-5.5-2-5.5 1.1-3.2.7-3.9 4.6-1.6 6.6-1.2 3.3.7 6.7 4 6.7 1.5 0 2.6-.8 3.1-1.9.5 1.1 1.6 1.9 3.1 1.9 3.3 0 5.2-3.4 4-6.7 2.3-2 1.6-5.9-1.6-6.6 0-3.1-4.1-3.7-5.5-1.1Zm0 0v12.5M6.5 10c2 0 3 1 3 2.7m8-2.7c-2 0-3 1-3 2.7')],
 Bug:[r(7,7.5,10,12.5,4.7),p('m8.5 4 1.8 3.5M15.5 4l-1.8 3.5M3.8 9.5 7 11m10 0 3.2-1.5M3.8 16H7m10 0h3.2M6 21l2-2m8 0 2 2M12 12.5v7')],
 CalendarDays:[r(4,5.5,16,14.5,2.8),p('M8 3.5v4m8-4v4M4.5 10h15'),dot(8.3,14),dot(12,14),dot(15.7,14),dot(8.3,17)],
 Camera:[p('M7.5 7 9.2 4.5h5.6L16.5 7h1.1a2.9 2.9 0 0 1 2.9 2.9v7a2.9 2.9 0 0 1-2.9 2.9H6.4a2.9 2.9 0 0 1-2.9-2.9v-7A2.9 2.9 0 0 1 6.4 7h1.1Z'),c(12,13,3.4)],
 Check:[check],ChevronDown:rot(chevron,90),ChevronLeft:rot(chevron,180),ChevronRight:chevron,ChevronUp:rot(chevron,-90),
 ChevronsDown:[p('m7.4 5 4.6 4.6L16.6 5m-9.2 9.4L12 19l4.6-4.6')],
 ChevronsUpDown:[p('m8.2 8 3.8-3.8L15.8 8m-7.6 8 3.8 3.8 3.8-3.8')],
 Circle:[ring],CircleAlert:alert,CircleCheck:[ring,smallCheck],CircleDot:[ring,dot(12,12,2.4)],
 CircleHelp:[ring,p('M9.2 8.5a2.8 2.8 0 0 1 5.5.7c0 1.9-2.7 2-2.7 4.1'),dot(12,16.6)],
 ClipboardList:[p('M8.2 5.5H7a2.8 2.8 0 0 0-2.8 2.8v9A2.8 2.8 0 0 0 7 20.1h10a2.8 2.8 0 0 0 2.8-2.8v-9A2.8 2.8 0 0 0 17 5.5h-1.2'),r(8.2,3.3,7.6,4.5,1.6),p('M8.5 12h7m-7 4h5')],
 Clock:clock,Clock3:clock,
 Code2:[p('m7 6.5-4.2 5.5L7 17.5m10-11 4.2 5.5-4.2 5.5M14 4l-4 16')],
 Copy:[r(8.2,8.2,11.6,11.6,2.8),p('M15.5 4.2H7A2.8 2.8 0 0 0 4.2 7v8.5')],
 CornerDownRight:[p('M5.2 4.5v7a4 4 0 0 0 4 4h10.3m-4.5-4.5 4.5 4.5L15 20')],
 CornerUpLeft:[p('M18.8 19.5v-7a4 4 0 0 0-4-4H4.5M9 4 4.5 8.5 9 13')],
 Cpu:[r(6.3,6.3,11.4,11.4,2.3),r(9.6,9.6,4.8,4.8,1),p('M9 3v3m6-3v3M9 18v3m6-3v3M3 9h3m-3 6h3m12-6h3m-3 6h3')],
 Database:[['ellipse',{cx:12,cy:6.5,rx:7.5,ry:2.7}],p('M4.5 6.5v10.8c0 3.6 15 3.6 15 0V6.5M4.5 12c0 3.6 15 3.6 15 0')],
 Download:[p('M12 3.8V15m-4.4-4.4L12 15l4.4-4.4M4.5 16v1.5a2.5 2.5 0 0 0 2.5 2.5h10a2.5 2.5 0 0 0 2.5-2.5V16')],
 Ellipsis:ellipsis,
 ExternalLink:[p('M14.5 4.2h5.3v5.3m-.2-5.1-8.5 8.5M9.5 4.5H7A2.8 2.8 0 0 0 4.2 7.3v9.5A2.8 2.8 0 0 0 7 19.6h9.5a2.8 2.8 0 0 0 2.8-2.8v-2.3')],
 Eye:eye,
 EyeOff:[p('m4 4 16 16M9.2 6.5a9.8 9.8 0 0 1 2.8-.4c3.9 0 6.8 2.4 9 5.9a20.7 20.7 0 0 1-2.4 3M5.7 8.3A20.7 20.7 0 0 0 3 12c2.2 3.5 5.1 5.9 9 5.9 1.1 0 2.2-.2 3.2-.6M9.5 11.2a2.6 2.6 0 0 0 3.3 3.3')],
 FileDiff:[file,p('M8.5 12h5m-2.5-2.5v5M8.5 17h7')],FilePlus:[file,p('M8.5 14.5h7m-3.5-3.5v7')],
 FileText:[file,p('M8.5 12.8h7m-7 4h5')],FileX:[file,p('m9.5 12 5 5m0-5-5 5')],
 Film:[r(3.8,4,16.4,16,2.6),p('M7.7 4.5v15m8.6-15v15M4 9h3.5M4 15h3.5m9-6H20m-3.5 6H20')],
 FlaskConical:[p('M8.7 3.5h6.6M10 4v5.2l-5.5 9a1.7 1.7 0 0 0 1.5 2.3h12a1.7 1.7 0 0 0 1.5-2.3l-5.5-9V4M7.9 14h8.2')],
 FoldVertical:[p('M12 3.5V8m-3.5-3.5L12 8l3.5-3.5M12 20.5V16m-3.5 3.5L12 16l3.5 3.5M4.5 12h15')],
 Folder:[folder],FolderMinus:[folder,p('M9 14h6')],
 FolderOpen:[p('M3.8 16.6V7.5a2.9 2.9 0 0 1 2.9-2.9h2.1c.8 0 1.4.3 1.9.9l1 1.2c.4.5 1 .8 1.7.8h4a2.9 2.9 0 0 1 2.9 2.7'),p('m3.8 16.6 1.6-4.4c.3-.8 1.1-1.4 2-1.4h12.1a1.4 1.4 0 0 1 1.3 1.9l-1.6 4.8a2.9 2.9 0 0 1-2.8 2H6.7a2.9 2.9 0 0 1-2.9-2.9Z')],
 FolderPlus:[folder,p('M9 14h6m-3-3v6')],FolderX:[folder,p('m9.8 11.8 4.4 4.4m0-4.4-4.4 4.4')],
 Gauge:[p('M5.7 18.5a8.2 8.2 0 1 1 12.6 0H5.7ZM12 5.8v1.4M5.8 12h1.4m9.6 0h1.4M12 14.5l3-4.7'),dot(12,14.5,1.25)],
 GitBranch:[p('M6.5 7v10m11-10v2.5a4 4 0 0 1-4 4h-7'),c(6.5,4.8,2.2),c(6.5,19.2,2.2),c(17.5,4.8,2.2)],
 GitCommit:commit,GitCommitHorizontal:commit,
 GitCompare:[p('M6.3 16V4.5M3.5 7.3l2.8-2.8 2.8 2.8m8.6.7v11.5m-2.8-2.8 2.8 2.8 2.8-2.8'),c(6.3,19,2.1),c(17.7,5,2.1)],
 GitPullRequest:[p('M6.3 7.1v9.8m11.4 0V9a4 4 0 0 0-4-4h-1.5m2.5-2.5L12.2 5l2.5 2.5'),c(6.3,5,2.1),c(6.3,19,2.1),c(17.7,19,2.1)],
 Globe:globe,Globe2:globe,
 GripHorizontal:[dot(6,8.5,1.1),dot(12,8.5,1.1),dot(18,8.5,1.1),dot(6,15.5,1.1),dot(12,15.5,1.1),dot(18,15.5,1.1)],
 Hammer:[p('M6 4.5h9.2L20 9.3l-3.4 3.4-4.4-4.4H9L5.2 12V7.2L6 4.5ZM12 11.7l-7.2 7.2a1.8 1.8 0 0 0 2.6 2.6l7.2-7.2')],
 Hand:[p('M7.2 12V6a1.6 1.6 0 0 1 3.2 0v5m0-6.6a1.6 1.6 0 0 1 3.2 0V11m0-5a1.6 1.6 0 0 1 3.2 0v6m0-2.8a1.6 1.6 0 0 1 3.2 0v5.7c0 3.4-2.1 5.9-5.5 5.9h-1.7c-2.1 0-3.4-.7-4.6-2.2l-4.2-5.1a1.7 1.7 0 0 1 2.5-2.2l.7.7Z')],
 Hash:[p('m9.3 4-2.1 16m9.6-16-2.1 16M4.5 8.5h15m-15 7h15')],
 ImagePlus:[p('M12 4.3H7.1A2.8 2.8 0 0 0 4.3 7.1v9.8a2.8 2.8 0 0 0 2.8 2.8h9.8a2.8 2.8 0 0 0 2.8-2.8V12M4.7 16.5l4.6-4.2 7.5 7.2M16 6h5.5m-2.8-2.8v5.5'),dot(9,8.3,1.1)],
 Images:[r(8,8,12,12,2.7),p('M15 4H6.7A2.7 2.7 0 0 0 4 6.7V15M8.5 17l4.2-4 6.6 6.1'),dot(16.3,11.5,1)],
 Inbox:[p('m4 11.5 2.3-6.1c.3-.8 1-1.3 1.9-1.3h7.6c.9 0 1.6.5 1.9 1.3l2.3 6.1v5.4a3 3 0 0 1-3 3H7a3 3 0 0 1-3-3v-5.4Zm.3.7H8l1.5 2.8h5l1.5-2.8h3.7')],
 Info:[ring,p('M11.3 11.2h1.2v5'),dot(12,7.6)],
 KeyRound:[c(8.7,8.7,4.7),p('m12 12 8 8m-4.7-4.7 2.4-2.4m.4 5.2 2.4-2.4')],
 Laptop:[r(5,4.5,14,11.5,2.4),p('m5 16-2 3.5h18L19 16M10.2 17.7h3.6')],
 Layers:[p('m12 4 8.5 4.5L12 13 3.5 8.5 12 4ZM4 12.4l8 4.2 8-4.2M4 16.4l8 4.2 8-4.2')],
 LayoutDashboard:[r(4,4,6.5,9.3,1.9),r(14,4,6,5.4,1.9),r(4,16.6,6.5,3.4,1.4),r(14,12.7,6,7.3,1.9)],
 LayoutGrid:[r(4,4,6.2,6.2,1.8),r(13.8,4,6.2,6.2,1.8),r(4,13.8,6.2,6.2,1.8),r(13.8,13.8,6.2,6.2,1.8)],
 List:[p('M9 6h10.5M9 12h10.5M9 18h10.5'),dot(4.5,6),dot(4.5,12),dot(4.5,18)],
 ListTodo:[p('m3.8 6.1 1.7 1.8 3.2-3.6M12 6.5h8m-16.2 9.6 1.7 1.8 3.2-3.6M12 16.5h8')],
 ListTree:[p('M4.5 4v12.5a2.5 2.5 0 0 0 2.5 2.5h3M4.5 8H10M14 5h5.5m-5.5 7h5.5M14 19h5.5')],
 Loader2:loader,LoaderCircle:loader,
 Lock:[r(5,10,14,10.5,2.7),p('M8 9.7V7a4 4 0 0 1 8 0v2.7M12 14v2.5')],
 LogOut:[p('M9.5 4H7A2.8 2.8 0 0 0 4.2 6.8v10.4A2.8 2.8 0 0 0 7 20h2.5M11 12h9m-4-4 4 4-4 4')],
 Mail:[r(3.8,5,16.4,14,2.8),p('m4.4 6 6.2 5.1c.9.8 1.9.8 2.8 0L19.6 6')],
 Maximize2:[p('M14.5 4.8h4.7v4.7M19 5l-5 5M9.5 19.2H4.8v-4.7M5 19l5-5')],
 MessageCircle:[p('M20 11.5a8 8 0 0 1-8 8c-1.3 0-2.5-.3-3.6-.8l-4.1 1.1 1.1-4.1A8 8 0 1 1 20 11.5Z'),p('M8.4 10.2h7.2M8.4 13.7h4.5')],
 MessageSquare:[chat,p('M8 9h8M8 12.7h5')],MessageSquarePlus:[chat,p('M8.6 10.8h6.8M12 7.4v6.8')],
 MessagesSquare:[p('M6.5 4.3h6.8a2.5 2.5 0 0 1 2.5 2.5v5a2.5 2.5 0 0 1-2.5 2.5H8.2l-4 2.2V6.8a2.5 2.5 0 0 1 2.3-2.5ZM19.8 9.8v9.9l-4.1-2.2h-5.4')],
 Minimize2:[p('M19.2 9.5h-4.7V4.8M14.5 9.5l5-5M4.8 14.5h4.7v4.7M9.5 14.5l-5 5')],
 Minus:[p('M5 12h14')],
 Monitor:[r(3.5,4.3,17,12,2.8),p('M12 16.5v3.3M8 19.8h8')],
 Moon:[p('M9.4 3.9a8.3 8.3 0 1 0 10.7 10.7A7.9 7.9 0 0 1 9.4 3.9Z')],
 MoreHorizontal:ellipsis,
 Network:[r(9,3.5,6,5.3,1.6),r(3.5,16,6,5,1.6),r(14.5,16,6,5,1.6),p('M12 9v3.5M6.5 16v-3.5h11V16')],
 NotebookPen:[p('M12.5 4H7A2.7 2.7 0 0 0 4.3 6.7v10.6A2.7 2.7 0 0 0 7 20h9.5M8 4.5v15'),p('m12.2 12.8 6-6a1.7 1.7 0 0 1 2.4 2.4l-6 6-3.1.7.7-3.1Z')],
 PackagePlus:[p('m12 3.5 8 4.3-8 4.3-8-4.3 8-4.3ZM4 8v9.5l8 4.1v-9.5M20 8v4M8.3 5.5l7.9 4.3M16.5 18.5H22m-2.8-2.8v5.5')],
 PanelLeft:panel,PanelLeftClose:panelArrow('left',false),PanelLeftOpen:panelArrow('left',true),PanelRightClose:panelArrow('right',false),PanelRightOpen:panelArrow('right',true),
 Paperclip:[p('m9.2 11.9 5.9-5.9a3 3 0 0 1 4.2 4.2L11 18.5a4.8 4.8 0 0 1-6.8-6.8l8-8m-5.4 9.2 8.1-8.1')],
 Pencil:pencil,PencilLine:[...pencil,p('M13.3 20h6.5')],
 PieChart:[p('M9.5 4.2a8.1 8.1 0 1 0 10.3 10.3H9.5V4.2ZM13.5 3.7v6.8h6.8a8 8 0 0 0-6.8-6.8Z')],
 Pin:pin,
 PinOff:[p('m4 4 16 16M10.5 4h5.3l-.5 5.5M8.5 11.2l-2.4 2.1v1.6h7M12 16.8v3.5')],
 Play:[fill('M8 5.5c0-.7.7-1.1 1.3-.8l10 6.5a1 1 0 0 1 0 1.6l-10 6.5c-.6.3-1.3-.1-1.3-.8v-13Z')],
 Plug:plug,PlugZap:[p('M6.8 4v4.5m7-4.5v4.5M4.5 8.5h11.5V11a5.8 5.8 0 0 1-11.5 0V8.5Zm5.8 8.5v3.3m9.5-16.5-2.5 5h4l-2.6 5')],
 Plus:[p('M12 5v14M5 12h14')],
 Quote:[p('M9.3 7.4H6.8a2 2 0 0 0-2 2v2.4a2 2 0 0 0 2 2h2.5V7.4Zm0 6.4c0 2-1.1 3.4-3 4.5M19.2 7.4h-2.5a2 2 0 0 0-2 2v2.4a2 2 0 0 0 2 2h2.5V7.4Zm0 6.4c0 2-1.1 3.4-3 4.5')],
 Project:[p('M7.3 11.4 4.7 10.3C3.2 9.7 2.9 8.6 3.5 7.7 4.1 6.7 5.2 6.6 6.5 7.2L10.3 9l2.3-2.7c1-1.2 2.5-1.2 3.3-.2 1 1.3-.5 2.7-.2 4.1.3 1.2 1.9 2.1 2.2 4.1l2.7.7-1.5 6-3.4-.7-6.5-1.5c-2.4-.6-3.4-2-3-4.1'),p('M7.2 14.6c-1.1 0-1.5-1.2-.7-2.4 1-1.5 3.3-1.9 4.9-.6 1.2 1 1.7 2.9.9 3.5-.4.3-.9.2-1.5 0M17.9 14.3l-.8 3.3')],
 Presentation:[r(3.5,4,17,12.5,2.8),p('M12 16.5v4M8 20.5h8M7.5 12l2.8-3 3.3 2 3-3.5')],
 Puzzle:[p('M8.8 4.5h.2a3 3 0 0 1 6 0h4.5V9a3 3 0 0 0 0 6v4.5H15a3 3 0 0 0-6 0H4.5V15a3 3 0 0 0 0-6V4.5h4.3Z')],
 RefreshCw:refresh,RotateCcw:retry,RotateCw:mirror(retry),
 ScrollText:[p('M7 7.5H4V6a2.5 2.5 0 0 1 5 0v11.5a2.5 2.5 0 0 0 5 0V16h6v1.5a3 3 0 0 1-3 3h-5.5M6.5 3.5H17A2.5 2.5 0 0 1 19.5 6v6.2M12.5 7.5h3.8m-3.8 4h3.8')],
 Search:search,
 Send:[p('M12 4 20 19l-8-3.4L4 19 12 4Zm0 1v10.5')],
 Settings:sliders,Settings2:sliders,
 Shield:[shield],ShieldCheck:[shield,p('m8.3 11.6 2.7 2.7 4.7-5')],
 Shuffle:[p('M4 6h2.5c3.5 0 7.5 12 11 12H20m-3.5-3.5L20 18l-3.5 2.5M4 18h2.5c1.4 0 2.7-1.9 4-4.4M13.4 9.8C15 7.2 16 6 17.5 6H20m-3.5-2.5L20 6l-3.5 3.5')],
 SlidersHorizontal:rot(sliders,90),
 Smartphone:[r(6.8,3,10.4,18,2.9),p('M10.3 5.7h3.4M10.3 18.3h3.4')],
 Sparkles:[p('M10.3 4.5c.8 4.7 2.2 6.1 6.9 6.9-4.7.8-6.1 2.2-6.9 6.9-.8-4.7-2.2-6.1-6.9-6.9 4.7-.8 6.1-2.2 6.9-6.9ZM18.6 3v4.5m-2.3-2.3h4.5M19 17v4m-2-2h4')],
 Split:[p('M6 20V7.2a3 3 0 0 1 3-3h1M7.5 2 10 4.2 7.5 7M6 15.7a5 5 0 0 1 5-5h4a3 3 0 0 0 3-3V4m-2.7 2.7L18 4l2.7 2.7')],
 Square:[frame],SquareCheck:[frame,smallCheck],SquarePen:squarePen,
 Terminal:[p('m4.5 6.5 5.2 5.5-5.2 5.5M12.8 17h6.7')],
 Trash2:[p('M4 6.5h16M9 6.5V4h6v2.5M6 7l.8 10.8a2.5 2.5 0 0 0 2.5 2.3h5.4a2.5 2.5 0 0 0 2.5-2.3L18 7M10 10.5v6m4-6v6')],
 TriangleAlert:triangle,
 UserRound:[c(12,7.5,3.2),p('M5 20v-1.2c0-3.2 2.3-5.3 7-5.3s7 2.1 7 5.3V20')],
 Users:[c(8.8,7.4,3.1),p('M3 20v-1.5c0-3.2 2-5 5.8-5s5.8 1.8 5.8 5V20M15.5 4.7a3.2 3.2 0 0 1 0 6.3m2 3c2.2.5 3.5 2 3.5 4.5V20')],
 Workflow:[r(3.5,3.5,6.5,5.5,1.7),r(14,14.5,6.5,6,1.7),p('M6.8 9.5v5a3 3 0 0 0 3 3H14M13 6.2h4.5V11')],
 Wrench:[p('m14 4 .7 4.4 4.4.7 1.2-3a6 6 0 0 1-7.8 7.8l-6.1 6.1a1.9 1.9 0 0 1-2.7-2.7l6.1-6.1A6 6 0 0 1 17.6 3.4L14 4Z')],
 X:[p('m6 6 12 12M18 6 6 18')],
 Zap:[p('m13.2 3.2-8.7 10.3H12l-1.2 7.3 8.7-10.3H12l1.2-7.3Z')],
 ZoomIn:[...search,p('M7.8 10.5h5.4m-2.7-2.7v5.4')],
 CollabNodes:[p('m10.8 7.1-4.2 8.4m6.6-8.4 4.2 8.4M8 18h8'),c(12,5,2.3),c(5.5,18,2.3),c(18.5,18,2.3)],
 PanelRight:mirror(panel),
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
