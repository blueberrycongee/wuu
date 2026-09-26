# Changelog

This file starts the maintained release record. Earlier GitHub Releases may not
have complete change notes.

The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).
Versioning rules are documented in [the release guide](docs/en/project/release.md).

## [Unreleased]

### Added

- The model popover has an independent Fast mode toggle and reset for supported
  provider models and native or ACP engines. Conversation and draft selections
  preserve speed separately from reasoning effort; `/fast` uses the same setting.

- Click the conversation title in the title bar to rename it. An existing
  conversation saves immediately. A new conversation keeps the name when the
  first message creates the session.

- Video output cards and workspace video files open an inline player with
  playback, seeking, volume, and fullscreen controls. Unsupported codecs show
  a message while keeping the download action available.

- The empty conversation home shows a usage overview under the greeting:
  sessions, tokens, and active days recorded in local Wuu history, with a
  daily activity heatmap for the past year. New installs show zero totals.
  The new `usage/overview` app-server method reads only token usage records,
  not conversation content. After 20 seconds without input, the greeting
  mascot plays short bouncing-ball, snake, and breakout scenes on the heatmap,
  rotating without consecutive repeats. Each scene ends on the heatmap: the
  ball settles into today, the snake bites its tail and the days it ate grow
  back, and the last breakout shot drills through the wall before it rebuilds.
  Any input stops the scene, and reduced motion turns it off; the underlying
  activity data is never changed.

### Changed

- Optional programmatic tool calling now runs each program in a fresh JavaScript
  process with the session filesystem sandbox. A default-off global switch and
  model-family overrides control availability. Nested calls retain normal tool
  permissions and recording; image/audio results are attached automatically.
  The previous persistent code runtime and execution/wait tools are retired.

- Project conversation lists show five recent entries plus active, running,
  unread, and up to three recently read conversations. Recently read entries
  expire after two minutes. Expanding includes all history inside an eight-row,
  font-responsive scroll area, keeping other projects in place.

- Creating an agent asks only for its model and name. The name step no longer
  shows switch-model and project controls above the input, and the random-name
  action sits inside the name input. The first conversation opens in the
  project chosen for the new conversation, shown in its header; the model stays
  editable in the agent's settings.

- The Extensions page follows the settings layout: a titled page with its
  actions beside the title, then plugins, official skills, and your skills as
  bordered lists in one column. Plugins come first and show their status on a
  shared axis; each row has a one-line description and opens its details. Text
  and icons follow the UI font size, a search hides lists without matches, and
  the plugin detail shows the same status label as its row.

- Desktop motion shares one duration ladder and one reduced-motion signal.
  The in-app Motion preference and the OS setting both collapse token-driven
  transitions, entrances, exits, drag movement, and frame-driven motion;
  spinners keep turning because they report ongoing work. Closing the
  conversation search overlay and the sidebar rename sheet no longer keeps a
  full-window click target for the whole exit.

- The macOS DMG installer window has a Retina-ready background with English
  and Chinese drag-to-install instructions: a slingshot beside the app fires
  Wuu along a dotted arc into the Applications folder.

- Collaboration now centers on project-bound DMs, with visible task controls,
  managed-session takeover and return, project and identity memory editing, and
  persistent conversation timers. The composer's project control chooses where a
  new conversation or a new agent's first conversation opens, and the
  conversation header shows the project. Memory and timers open from the header
  in a side panel. Group navigation is hidden while data remains.
- Work execution uses isolated Git worktrees, versioned shared decisions and
  structured reports. Host-managed candidates and independent verification expose
  reviewable diffs with apply, optional Git-extension PR, and discard actions.
  Progress deadlines, revision checks and private-history boundaries protect
  continuing work from stale updates and silent stalls.

- Collaboration tools follow each admitted session role: conversations read and
  coordinate, execution sessions can write, and verification sessions only read.
  Continuing identities refresh their role instructions on every turn.

- Settings pages are grouped by task: Agents & models, App, Extensions, and
  Data. Each page uses one column, with its title and page actions on the
  first line and settings in bordered groups. Model providers and agents open
  in place under their own row. Page identifiers are unchanged, including
  `advanced` for the runtime page.

- Pasted or attached images, videos, PDFs, and folded long text now wait in a
  single-row tray that slides out from behind the top of the composer instead
  of growing the input box. Cards show a thumbnail, or a title with size or
  line count; removing one lets the rest slide into place, and the input keeps
  its size throughout. Overflowing trays scroll horizontally with faded edges.
  Unsupported attachment types and import failures use the shared capsule
  notification instead of a persistent composer status line.

- `bash` starts background processes with a single `run_in_background` flag
  instead of its seven background actions. A new `process` tool reads output
  from, writes input to, stops, lists, and updates running processes,
  including switching a long-lived service to `completion_mode=detached`.
  Older transcripts that used `bash` background actions still render on the
  desktop.

- Command results reach the model as terminal-style text: the output, plus an
  exit code, full-log path, timeout hand-off, or sandbox denial only when they
  apply. Terminal color codes and progress redraws are stripped, and each
  output stream keeps its head and tail instead of only the tail. `process`
  results and background completion notifications use the same plain text.
  Clients and durable records keep the JSON envelope.

- `read_file` and `bash` results are bounded at 8192 estimated tokens instead
  of 2048, so a typical source file or document is read in one call.

### Fixed

- Keep edited-away conversation branches out of resumed conversations and forks,
  including subsequent model input, while preserving valid pre-compaction history
  and the append-only audit transcript.

- Safe mode stops approved desktop plugin modules from loading or activating,
  while keeping the extension inventory available for recovery and management.

- Remote Web clients reconnect when the relay stops responding during authentication,
  and cancel pending authentication immediately when stopped or suspended.

- Git status and staging snapshots preserve literal filenames, including spaces,
  Unicode, quotes, backslashes, and newlines. Returned rename paths identify the
  destination, and staging or unstaging a backslash path no longer selects other
  files through Git pathspec escaping. Sensitive-path protections remain in place.

- Startup permission migration skips symbolic links inside the Wuu data directory,
  preserving external files and executable permissions. Symlink migration markers
  are replaced without modifying their targets.

- Workspace file reads, saves, and directory expansion preserve leading and
  trailing whitespace and literal POSIX backslashes in filenames, preventing
  a selected file from being confused with another file in the workspace.

- Overdue automations keep their scheduled occurrence until the session service
  is ready. Interrupted dispatches reuse their run record after restart, and
  completing a dispatch preserves a newly edited schedule.

- Git review compares symbolic link target paths without reading target contents,
  including untracked and dangling links and file/link type changes.

- Git changes, statistics, and file previews work before the first commit,
  including staged files and edits made after staging.

- Sidebar scroll fades remain tied to the list's own scroll position while
  conversations stream, pause, finish, or switch. Streaming paint reduction
  applies only to nested reasoning and process details.

- The new-agent setup header drags the window across its full width again, and
  its height follows the window title bar after page zoom and at large UI font
  sizes.

- Plugin workspace delivery includes committed, staged, and unstaged tracked
  changes since workspace creation. Status and previews use the same baseline;
  conflicts and unsupported untracked files preserve the workspace. Automatic
  cleanup retains committed work and workspaces whose baseline is unavailable.

- Work delivery reads structured reports from the final answer, so commentary
  before tool calls no longer causes completed executions to fail validation.

- Collaboration conversations can receive due room reminders, include them in
  unread counts, and clear their wake state after consumption.

- Collaboration replies no longer become held drafts just because a thread
  reply is newer than the conversation's main timeline.

- Sending a message keeps one local waiting timer across admission, events,
  snapshots, and conversation switches without changing server timestamps.
  Follow-up messages can queue during new-conversation creation. Stop uses an
  immediate progress icon until execution ends and retains pending input without
  automatically dispatching it after cancellation.

- Terminal run details preserve output, exit status, and log links when a
  command's model-facing output is JSON, rather than treating that output as
  execution metadata.

- History search finds literal quotes, paths, line breaks, and HTML characters
  in deduplicated tool results, including model text and multi-part output.

- Ready Agent Core entries show an external agent's detected executable path in
  its override field without repeating it in the status or help text.

- Model choices in desktop settings keep a stable order and selected styling.
  The catalog no longer shows ineffective per-model remove controls or selection
  animations, and provider remove controls keep their icon centered and color
  stable on hover.

- OpenAI-compatible Chat Completions requests preserve tool calls, reasoning,
  and participant names in adjacent messages, preventing orphaned tool results
  in both ordinary and streaming conversations.

- Deleting an agent archives sessions still under its management in a separate
  Agent archive, keeping them out of workspace and unread lists. Previously
  orphaned sessions are reconciled, and user-taken-over sessions stay available.
  Agent deletion updates navigation immediately and reconciles cleanup errors.

- Preserve sidebar folder and collaboration folds when returning from settings,
  including after switching between light and dark themes.

- `apply_patch` accepts LF and CRLF patches for CRLF files while preserving
  their line endings and whether the file ends with a newline.

- `apply_patch` with `then_run` now shows the model the follow-up command's
  outcome; the parent result previously kept the patch-only view.

- Link context menus in messages list only their actions instead of repeating
  the full URL, so the menu no longer spreads across the message. Context menus
  stay inside the window, truncate long labels, and scroll long lists. Opening
  one closes any other open context menu, including the composer edit menu, and
  hides hover tooltips until it closes.

## [2026.9.25] - 2026-09-25

### Changed

- The composer model picker adapts to larger UI text and supports keyboard
  navigation, focused search, and clearer engine-managed model guidance.

- Desktop titlebars, tab bars, and the sidebar place their first and last
  icons 20px from the pane edge on both sides. In the message column, tables
  start on the same edge as paragraphs, while code blocks, the composer, and
  message bubbles share one 16px inner inset. Derived text, icon, and line
  sizes land on whole pixels, sidebar group headings keep one height, and
  message headings step more clearly above body text.

### Fixed

- Reduce long conversation switching stalls by assembling core responses once
  per message and reusing resume responses instead of transmitting history twice.

- Message table headers no longer render bold, and narrow tables keep words
  whole instead of breaking them mid-word.

- The workspace file tree now fits a narrow panel when the panel opens after
  launch, so the file preview keeps a usable width instead of wrapping one
  character per line.

## [2026.9.24] - 2026-09-24

### Added

- Conversation image previews support previous/next buttons, left/right arrow
  keys, and a position counter for displayed uploads, tool images, and message
  images. Navigation stops at the first and last image.

### Changed

- Short bash results omit the duplicate combined output from model context when
  the complete stdout and stderr reproduce it exactly. Original results and all
  diagnostic evidence remain intact.

- Text file reads show a line number on the first line of each page and at
  every tenth file line. Each line retains a separator so source indentation
  and literal pipes can be copied without display metadata.

- The embedded browser no longer shows a bottom domain and page-title bar,
  leaving more room for the page. Direct page input or navigation pauses the
  current task; sending the next instruction in the conversation resumes browser
  work without separate control-transfer buttons or status labels.

- While a conversation loads, the loading indicator covers only the
  conversation area. The sidebar, title bar, and environment panel stay
  visible and usable, and the sidebar marks the conversation being opened.

- Composer project and branch selectors use lightweight, arrow-free pills
  instead of a full-width background. Their menus have roomier spacing,
  aligned icon and selection columns, and separate branch status text.

- Sending a message moves only the message bubble into place. The new turn's
  in-progress status no longer travels with it; it fades in as the bubble lands,
  for both the first message and follow-ups.

### Fixed

- Composer feedback no longer crowds the send toolbar. Redundant queue-edit,
  commit, and pull-request confirmations are removed; errors and restrictions
  remain readable above the input in both main and split conversations.

- Desktop conversation refresh and session resume discard obsolete cached
  messages when a full completed turn arrives, preventing duplicate replies
  after a missed completion notification.

- Invalid external configuration edits report an error without erasing the last
  valid model inventory. Unchanged invalid files no longer trigger repeated
  parsing and logging, and corrected files recover automatically.

- Desktop development now uses a separate data directory per checkout and forwards
  explicit `WUU_HOME` overrides on macOS, preventing experimental settings from
  breaking an installed app.

- Files with Chinese names or filename whitespace now preserve their paths,
  line counts, and contents in desktop Git previews before and after staging,
  including modifications, deletions, and renames. Commit message generation
  also receives the original filenames.

- Named agents can be deleted even when they have task history. Deletion stops
  their execution and removes their local identity state while retaining shared
  channel history and completed task attribution.

- Switching conversations no longer shifts messages after they appear. A
  conversation left mid-history reopens at the same reading position with
  Jump to latest already shown.

- Scrolling down to the latest message no longer bounces back when Jump to
  latest disappears.

## [2026.9.23] - 2026-09-23

### Added

- Agents can inspect local PNG, JPEG, static GIF, and WebP images with
  `read_file`, including generated session artifacts, without a composer
  attachment. Image results retain the existing file scope and model capability
  checks. Code Mode forwards emitted images as visual observations.

- Image previews support smooth trackpad pinch zoom, two-finger panning, drag,
  fit-to-window and actual-size views, rotation, keyboard controls, and a desktop
  Save As dialog that preserves the original image.

- Browser previews show an animated completion badge when the visible task
  finishes successfully, and clear it when work resumes. Hover reveals preview
  controls even while another application is active.

- Model selection now includes GPT-6 Sol/Luna (including Fast mode) and
  Claude Opus 5.5, with current limits, pricing, and reasoning controls.
  Claude Fable 5.1 and Opus 5.5 use always-on adaptive thinking, preserve
  readable progress, and recover from invalidated thinking after context
  changes. Tool-closing requests use their supported automatic tool choice.
  New CLI configurations start with GPT-6 Sol and Claude Opus 5.5.

- SuperGrok and xAI API catalogs now include Grok 4.7 (`grok-4.7`), with the
  documented 500k context window and low/medium/high/xhigh reasoning efforts.
  New SuperGrok providers default to it; Grok Build lists it alongside 4.5 and
  4.6.

- Desktop conversations can open the workspace browser from `/browser`, the
  workspace tool list, message links, turn sources, and compact browser
  activity rows. Modifier-clicks still use the system browser, and the panel
  does not steal focus from another workspace tool or a foreground Agent
  browser.

- Embedded browser automation is available by default. The active conversation
  previews the Agent's page in a floating card inside the conversation column.
  Dragging the card snaps it to a corner of that column, clear of the composer.
  The page keeps its layout size and is zoomed so the whole page fits in
  the card. Dragging an edge or corner changes the card, and the zoom follows.
  Pointer events on the card move or resize it; they do not click the page.
  The workspace panel opens when the user opens the browser or expands the
  card, and does not open or close on its own. Set `WUU_ENABLE_BROWSER=0` to
  hide the tool.

### Changed

- The subscription dashboard stays development-only and is not exposed in
  production Settings or extension-provided settings navigation.

- Folder, conversation, and new-conversation icons use simpler contours and
  consistent rounded frames, matching the sidebar toggle controls. Single and
  stacked conversation bubbles share the same visual style.

- Information, add, and expand/restore icons use balanced optical insets beside
  sidebar toggles and close icons. Sidebar glyphs follow their control's size,
  including compact titlebar menus.

- Conversation images use compact tool thumbnails and bounded, proportional
  previews in replies, with consistent space before and after images.

- TODO capsule hover cards use roomier line and task spacing, preserve paragraph
  breaks, and keep long task descriptions at regular weight for easier reading.

- Dark desktop surfaces use fine inset highlights for depth. Menus, dialogs,
  and edge drawers retain black shadows to separate overlapping content.

- Desktop and shared Web controls use Wuu's original SVG icon family, with
  consistent optical sizing across navigation, tools, settings, skills,
  extension marks, empty states, and browser overlays. A set of twelve
  muted Morandi SVG variants is available for optional colored surfaces.

- Browser previews and the workspace panel share a clearer animated pointer,
  with consistent movement and input feedback at every preview size.
  Taking control clears the pointer and cancels pending input.

- Desktop conversations use quieter titles and branding, tighter sidebar group
  spacing, and smaller reading/composer gaps. Workspace previews start with five
  recent sessions while keeping current, running, and unread sessions visible.

- Process summaries have clearer text and keyboard focus, with more space between
  expanded tool rows. Floating navigation follows the UI font size and shared
  menu elevation. Environment panels retain normal text and control sizes in
  narrow windows. Conversations share the standard settings page's content
  width and insets, with a near-white composer surface in the light theme.

- The embedded browser can read the page it is showing. Observation returns
  the headings, paragraphs, lists, and tables together with the controls it
  can click, and a long page continues from the returned offset. Ending a
  browsing turn keeps the pages named in `keep`. A page the site opens is
  tracked with the other tabs and closed when it is not kept.

- The browser preview card does not paint page scrollbars. The card can be
  moved and resized, but not scrolled or taken over. Scrollbars return when
  the page is docked in the workspace panel.

- The workspace browser and the agent page are the same tab. Opening the
  browser shows that page, and the address bar, back, forward, and reload
  drive it. A pointer travels to the target before a click or scroll is
  sent. Closing the panel keeps the page in the hidden host.

### Fixed

- New Harness conversations appear in the sidebar while creation is pending.
  Stop remains available through background refreshes, restores the submitted
  input, and cleans up an unused session if creation finishes after cancellation.
  Concurrent drafts no longer share a single pending placeholder.

- Inline artifact image previews fit the image's original proportions instead of
  adding white margins inside a fixed 4:3 frame, without cropping or stretching.
  Click-to-enlarge still opens the complete original image.

- Conversation model changes no longer replace the selected model with workspace
  defaults. Model, reasoning-effort, and permission update failures use the shared
  top notification instead of duplicating errors in the composer status line.

- Composer submissions no longer wait for cached conversation refreshes. Main
  and split views accept normal follow-ups after a final answer, preserve the
  original workspace across asynchronous preparation, and retain failed input
  without overwriting newer drafts.

- Responses image-generation results now appear as assistant image attachments
  and remain available when reopening the conversation, including replies with
  no text.

- Managed session reports preserve completed, failed, and interrupted execution
  outcomes. Control changes and ordinary evidence messages no longer claim a
  successful completion; rejected session operations report failure.

- Collaboration handles pending human directions before background session
  reports, including after a restart and when an inbox spans multiple batches.
  Earlier session results remain queued in their original room and task.

- Conversation spacing accounts for the docked workspace panel as it opens or
  resizes, keeping the scrollbar next to the message flow.

- Expanding a browser preview opens its exact tab in the owning session. Missing
  previews report an error instead of displaying a previous session’s page.

- Browser previews stay hidden after switching away from their conversation,
  including when panel visibility or background activity updates refresh them.
  Returning to the owning conversation restores its preview.
- Ordinary background browser actions show a watch-only preview in their active
  conversation without requiring explicit visibility promotion. Further actions
  keep it visible; docking the same page in the workspace panel hides the card.
  A hidden or minimized desktop window stays hidden; its preview returns when
  the window is shown or restored.

- Switching desktop sessions keeps caught-up process folds stable through the
  first paint. Collaboration conversations restore the message being read,
  cancel outgoing scroll motion, and retain recent history when returning from
  a Harness session, avoiding a blank reload and a jump to the latest message.

- Desktop surfaces share titlebar geometry and sidebar-control alignment, while
  compact channel headers retain their native safe-area clearance.
  Sidebar toggles now retain their position and size when switching to Settings
  or opening a narrow-window drawer. Workspace headers use the same geometry
  across window widths. Desktop zoom preserves native window-control alignment
  while controls fit; larger zoom or font sizes expand the row to avoid clipping.

- The desktop app detects external engines installed in the usual user
  locations, including `~/.local/bin`, Homebrew, and version-manager shims,
  when it is opened from Finder or the Dock. Those launches do not receive
  the terminal PATH.

- File tools no longer treat source files such as `credentials.go` as credential
  stores only because the name contains `credential` or `secret`. Credential
  stores such as `credentials.json` and `secrets.yaml` stay protected in every
  permission mode, and refusals no longer suggest chat approval or a mode
  switch that cannot lift the guard.

- Subscription request details retain their recorded provider after model-service
  changes and never reuse earlier token usage for a later failed request.

- Searches without ripgrep no longer fail on long generated lines. The fallback
  respects Git ignore rules when Git is available and counts matching lines
  consistently across search modes.

- File reads and edit-error snippets separate line numbers from source with a
  visible delimiter, preserving indentation for exact edits. Failed edits can
  identify tab/space differences without applying an ambiguous replacement.

- Switching browser tabs keeps the floating preview window, including its
  chosen corner and size, instead of briefly closing and resetting it.

- The browser preview no longer moves to avoid the jump-to-latest and status
  row; composer avoidance remains in place.

- Browser preview positioning uses current layout measurements during window
  resizing without an extra animation-frame delay or stale intermediate moves.

- Conversation switches restore the reading turn instead of following background
  output. History loading preserves in-flight scrolling without double-applying
  native anchoring or counting new output below the reader. Cached streams catch
  up before scroll measurement and no longer replay pending text fades on reveal.

- Browser observations now read link and button text from Chromium's layout
  snapshot, including nested and repeated text, instead of returning unnamed
  links. The screenshot tool description clarifies that it saves a UI preview
  rather than returning image content to the model.

- Moving the pointer onto the workspace browser no longer takes the page
  from the agent. A press, a context menu, or typing does.

- Browser previews start from a desktop-sized page viewport, preserve its
  proportions in narrow columns, and keep the resize input layer attached
  throughout a drag. Page navigation reapplies the preview scale.

- Switching sessions no longer jumps the message stream or replays catch-up
  motion. A paused reading position stays where it was, and a conversation
  that changed while hidden appears in its current layout.

- Opening the workspace terminal no longer covers the app with the startup
  animation. Progress stays inside the right panel, the terminal is prepared
  while the app is idle, and switching to another workspace tab keeps the
  session.

- Opening Automations, or another primary plugin page, no longer lets the
  empty-session wallpaper, greeting, or composer show through that page.

- The conversation titlebar no longer draws a line along its bottom edge.

- Resizing the window no longer remeasures every folded block, turn, and
  truncated label on each size change. The open conversation still stays
  pinned to its latest line while the frame moves.
- Resizing the window reflows the open conversation. Sessions kept behind
  it no longer wrap again on every size change, and off-screen turns keep
  the height they already had.

- A background session that keeps calling tools, including an ACP session
  driving another harness, no longer re-sorts the whole sidebar on every
  tool event. Those updates are applied together. Dragging the macOS window
  keeps the vibrancy material instead of swapping in an opaque fill.

- Session lists no longer run `git status` for every worktree. Summary
  lists, which the desktop sidebar refreshes on a timer, keep the worktree
  path and skip that checkout scan. Thread indexes are also reused until
  the file changes, instead of being parsed twice per list.
- Live tool output is joined once per visual update. Previously each output
  chunk copied the whole result accumulated so far.
- A running mascot keeps animating after its morph settles, at about 30
  frames per second instead of every display frame.

- Sending a message keeps the same glide into the reading position. Steady
  frames of that motion only move the viewport; the conversation is measured
  again when layout actually changes, and the turn rail, jump control, and
  history preload catch up when the glide lands.

- Streaming turns spend less time on decorative paint. While a running
  conversation stays pinned to the latest line, the live text wave and
  scroll-edge fades pause, and stream colors are reused until the theme
  changes. Code blocks over 8,000 characters stay plain text until
  highlighted, and blocks over 100,000 characters are not highlighted.
  The collaboration directory slows its refresh when the room list is
  unchanged or the window is hidden.

- Narrow windows no longer place a close button on the session drawer.
  Dismiss it from the dimmed backdrop, the titlebar sidebar button, or by
  moving the pointer off the drawer. While the drawer is open that button
  stays above the sliding rail, so hovering it does not flicker the drawer
  shut, and the title keeps its place.

- Long grok-4.6 sessions compact or install a smaller context window after a
  classified `input_too_large` overflow, instead of surfacing the 400 when
  local usage still sits under the compact threshold.

- Sessions whose provider omits token usage reconcile assistant text,
  tool-call arguments, and tool schemas into the local context estimate
  before the next request. Proactive compact or a fresh context window runs
  when that estimate reaches the threshold, including after a length
  truncation. A context size saved without provider usage is not reused as
  ground truth after a restart.

- Collaboration-created ordinary workspace sessions now keep their project id
  on `thread/started`, so they stay in the workspace sidebar instead of
  vanishing until a later list or reload.

- Keep desktop process rows still when switching sessions: publish the
  frozen aggregated tool-call layout on the first visible frame, and keep
  the status ball from shoving the summary with its enter/exit motion.

- Restore a desktop conversation from its distance to the latest content
  after a session switch, instead of a raw scroll offset that jumps when
  estimated turn heights settle.

- Keep a running desktop conversation from re-anchoring when it becomes
  visible again. Catch-up stream text lands without firing a new stream
  frame.

- Ignore stale ACP JSON-RPC results whose id does not match the in-flight
  request, so a Grok prompt that is already waiting on the model is not
  aborted as an internal Wuu error.

- Packaged macOS builds re-apply the traffic-light position after the window
  is shown, so the lights stay aligned with the 48px titlebar instead of
  remaining at AppKit's default inset.

### Changed

- Queue a running-turn follow-up with Command+Enter (Ctrl+Enter on Windows
  and Linux). Tab now only moves focus, including while a draft is waiting.

- Room conversations publish only through `chat_send` or a room-targeted
  `collaboration_send`. Assistant text stays private after the turn ends, so
  waiting copy no longer becomes a public bubble.

- Plugins can be enabled or disabled while a conversation is running. Later
  conversations use the new generation; a conversation that already started
  keeps the generation it pinned until it rebuilds. Removing a plugin still
  waits until running work finishes.

- Clicking a field or control no longer paints an extra outline. A focus ring
  appears only when moving focus with the keyboard.

- The desktop sidebar bell keeps recently opened conversations visible while
  the attention view stays open, instead of dropping them as soon as they are
  marked read. The bell badge still counts only running and unread sessions.

- The composer access menu maps Standard, Read only, and Unconfined onto ACP
  agents' advertised permission modes, and hides Read only when the agent
  does not publish a distinct plan or read-only setting.

## [2026.9.21] - 2026-09-21

### Added

- Added Cursor, Devin, Grok, Hermes, Pi, OpenCode, and Antigravity external
  engines, with executable detection, per-engine settings, session recovery,
  native permission handling, and explicit cancellable ACP sign-in.

- Drag the Collaboration sidebar heading to reorder it alongside Pinned,
  Folders, and Workspace, with the order preserved across restarts.

- Desktop appearance settings can import a device-local background image across
  the sidebar and workspace, with five image effects and adjustable strength.

- Preview delivered file snapshots in desktop workspace tabs beside the
  conversation. A single supported delivery can open after successful completion
  when the panel is available, without taking over manual panel or browser use.

- Named Agents can explicitly attach selected room images, PDFs and supported
  videos to execution-session create/send handoffs, preserving source evidence
  and rejecting inaccessible, missing or unsupported media instead of losing it.

- Added optional Action Fusion to `apply_patch`: `then_run` runs a known
  validation command after the complete patch succeeds, using the normal bash
  permissions, logs and managed-process lifecycle. Command failures keep the patch.

- Added catalog and transport support for DeepSeek V4.1 Flash (`deepseek-flash`),
  Qwen3.8 Max/Flash, and Kimi For Coding's current 1M-context coding model,
  including the `kimi-k2.8-preview` alias used by the latest models.dev snapshot.

- Added **Approve for me** as a Standard-mode permission option. High-risk native
  tool calls are reviewed before they run; the workspace boundary is unchanged,
  and a review timeout or failure is not treated as a denial.
- Enabled cross-session coordination through Peers, with attributed message bodies,
  source navigation, and expandable existing chat bubbles on Desktop and native mobile.

### Changed

- Publish the macOS preview from GitHub Actions without a release certificate,
  retaining ad-hoc integrity signatures and documenting Gatekeeper limitations.
- Use the UTC release date (`YYYY.M.D`) for product versions.

- Reveal desktop scrollbars only while scrolling, then fade them out; hovering
  tool and reasoning details no longer reveals their scrollbars.

- Keep the desktop conversation's input box and message column on the pane's
  centerline with the sidebar open or collapsed, instead of leaving a wider
  margin on the right where the scrollbar gutter is reserved.

- Animate the Automations editor split open and closed, instead of snapping
  the form pane in and out.

- Lay out the empty desktop workspace panel as a left-aligned icon-and-label
  list instead of a centered icon stack.

- Show each engine's existing SVG brand mark on the desktop composer model
  picker, engine choices, and first-run runtime cards.

- Drop the engine name from the composer model chip, leaving the SVG mark
  with the model name.

- Keep recently visited desktop conversations mounted when switching sessions,
  so the incoming thread does not rebuild and flash after the click.

- Keep collaboration rooms still while the desktop window is being resized, so
  the message stream and inspector do not flicker with the frame.

- Place desktop sidebar group disclosure chevrons immediately after the Pinned,
  Folders, Workspace, and Collaboration headings instead of in the trailing
  action column.

- Size click-open desktop overlay cards — sidebar context menus, the composer
  permission picker, and other dropdowns — one step below reading text, with
  medium weight, instead of mixing compact and reading sizes.

- Size desktop sidebar row marks — navigation, plugins, collaboration avatars,
  folders, and heading plus buttons — to one glyph column and the UI type scale.

- Show pasted text in a user message as one attachment row — file icon, the first
  line of the paste as its title, and the collapse control — instead of repeating
  a "pasted text" label and a character count under it.

- Hide phone-connection and account entries in all production desktop builds,
  including local packages, while retaining them in development mode.

- Settle large tool results, including extension results, into recoverable pages
  before recording them. Preserve original payloads and media, keep search
  snapshot cursors, and avoid batch-wide text cuts that could erase recovery links.

- Keep desktop tool-call summaries on one line, truncating overflow with an
  ellipsis while preserving the activity icon and diff counts.

- Show only the current TODO items in the composer status hover card, without
  repeating the TODO title, progress count, or tool explanation.

- Reuse loaded sidebar history when reopening desktop conversations, even after
  a workspace refresh has replaced the active conversation catalog with summaries.
  Cross-workspace activation now displays the resumed conversation without waiting
  for live and archived conversation lists, while preserving drafts and live updates.

- Refine the Agent editor with opaque surfaces, aligned borderless fields and
  inset actions that stay visible while scrolling. The expanded avatar picker
  uses even option grids and one-click colors, with custom hue adjustment retained.

- Fetch lightweight conversation summaries for desktop lists without copying
  loaded histories, reducing IPC payloads and app-server allocation overhead.
  Recover missed completion events with targeted snapshots for stale loaded
  conversations, including both visible panes.

- Keep conversation search results title-only on the left, with matching snippets
  and conversation details in the right preview pane.

- Drop the blue focus ring from the conversation search field. Typing focus is
  the caret inside the already-framed dialog.

- Nudge the desktop process mascot's reading shape right for optical alignment
  with conversation text, without moving the summary text column.

- Use a simpler split-arrow icon for desktop conversation forks, consistently
  across sidebar markers, message actions, and worktree notices.

- Let desktop environment-panel content use the full width below the close
  button instead of reserving an empty column beside TODO and Git rows.

- Make working notes and summary-free context windows built-in defaults, preserving
  existing session notes without the Context Notes extension. Include Peers in
  first-run extension choices. Recommend only TODO and Automation by default.

- Remove the redundant workspace path row from the terminal panel so terminal
  content starts directly below the workspace tabs.

- Tighten the base communication contract so process text and answers share one
  speaking style, prefer short paragraphs over status one-liners, and avoid stock
  AI phrasing.

- Remember each provider's and engine's last composer model and effort, including
  Settings workspace defaults, so switching back does not reset to the catalog default.

- Relax conversation reading rhythm, fold code and tables into the same spacing
  system, and lengthen scroll fades so list and message edges read as a gradient.

- Separate in-menu search from the options below with a hairline and smaller
  search icons.

- Drop the sidebar `harness` descriptor so the brand lockup shows only `wuu`.

- Unify named-agent and group-chat navigation with the desktop session sidebar,
  nesting managed sessions beneath their named agent instead of their workspace.

- Narrow outgoing desktop conversation bubbles to 516px or 80% of the message
  area and use a mist-blue palette in light and dark themes.

- Temporarily hide the Collaboration sidebar's Agent management shortcut while
  retaining agent management capabilities and other entry points.

- Temporarily hide conversation file-change summary cards and the turn navigation
  rail while retaining edit history and the underlying review components.
- Present generated files with the same turn output-summary card as file changes,
  and keep that receipt hidden in the conversation while retaining snapshots and
  inline image previews.

- Reduce excess whitespace between conversation turns while preserving message
  action space and query-to-reply spacing.

- Share spacing and density roles across desktop menus, forms, cards, dialogs,
  and plugin UI; let controls grow with text and compact file-change summaries.

- Tighten sidebar spacing with consistent row, heading, and group intervals;
  keep the pinned drop target within the group spacing.

- Keep the conversation composer 12px from the bottom across viewport widths
  and input states, while retaining device safe-area clearance.
- Use compact corners for embedded message-flow code, output and diff cards,
  and smaller code-copy icons while preserving their click targets.

- Use natural letter spacing across desktop UI and prose and remove global
  proportional CJK alternates, preserving code and terminal character alignment.
- Default new desktop UI preferences to 14.5px while preserving saved sizes;
  remember each user's page zoom and scale message action controls with text.
- Align titlebar and workspace controls, lighten message bubbles, and unify
  sidebar and account-menu row heights, hover corners, and spacing.
- Unified desktop navigation spacing, replaced workspace tool cards with a
  centered list, and aligned file-tree text with UI font preferences.
- Refined desktop conversation typography, prompt widths, message corners, and
  action spacing while preserving saved text sizes and responsive reading space.
- Use a light neutral background and dark text for outgoing messages in light mode,
  including their inline code, links, and edit controls.
- Removed the room-header plans and memory panel while preserving Agent capabilities,
  saved plans, memory, and task controls.
- Made sidebar list expansion controls lighter and kept them grouped with their
  conversations, with consistent expand/collapse spacing and keyboard focus.
- Set the desktop interface's default scale to one zoom-out step below actual
  size, preserving saved font preferences and native browser alignment.
- Simplified iOS conversation activity into desktop-shared tool summaries and
  mascot motion, with an inline stop control and consistent, dynamically scaled
  typography across messages, composers, and process rows.
- Let conversation inputs grow with their contents up to the same height as
  manual expansion, shrink when text is removed, and scroll once full.
- Simplified the native iOS conversation directory with secondary archive/history
  actions in its menu and project names instead of full workspace paths.
- Kept native iOS tab switching on directory pages. Conversation pages, including
  empty sessions, use native back navigation and reserve the bottom edge for the
  composer; directory search also hides the tab bar.
- Matched native iOS connection feedback to the desktop bouncing Wuu letters and
  blinking caret, with green branding, typed status text, and reduced-motion support.

- Send and arrival scrolling in desktop conversations settle on one trajectory that
  keeps a fixed share of the remaining distance per frame, so streaming output, a
  collapsing composer, and late layout changes extend the same motion instead of
  restarting it. Programmatic scrolling no longer stops at a fixed deadline.

### Removed

- Removed the `yield_turn` tool. Sessions use normal provider completion to end
  without an outward reply, including room discussions and peer follow-ups.

- Removed stale message-flow screenshots and the disposable output-card demo from
  tracked artifacts.

### Fixed

- Treat Grok ACP prompt-stall as total wire silence after `session/prompt`,
  matching the Zeron watchdog: session boilerplate does not count as life,
  but Grok's `_x.ai/session_notification` queue bookkeeping does, so extra-high
  reasoning after that first frame can stay quiet. A wedged agent now surfaces
  as an unresponsive-engine error instead of an internal Wuu error.

- Restore desktop sessions against the submitted message's current position
  when history is remounted or reflowed, instead of reopening in stale blank
  space below the messages.

- Keep live `steer` inputs in the same reading flow as queued messages, including
  messages sent from the pending drawer, without replaying the submission glide.

- Keep queued messages in the normal reading flow instead of replaying the send
  glide when they start. Queue receipt fades locally, and appending turns no
  longer collapses mounted history or replaces measured heights with estimates.

- Reduce send-animation style recalculation by keeping the first-message spacer
  local to the content wrapper and ignoring subpixel reservation feedback.

- Keep conversation scrolling aligned with text reflow during window resizing,
  update composer clearance while dragging, and restore session history against
  the incoming composer's viewport rather than the previous session's height.

- Apply deferred conversation layout before lifting the window-resize freeze, so
  dragging the desktop frame does not jump after the chrome is already still.

- Keep Agent editor identity mascots unclipped so hats and other accessories
  are not cut by the circular preview, and sit the appearance badge outside
  the face.

- Park a first query and its in-progress timer on the composer with one lead
  spacer, then consume that spacer on the submit glide, so the bubble and timer
  rise together in document flow instead of fighting a scroll transform.

- Send ACP image attachments as local file paths in the prompt instead of
  failing the turn when the agent does not advertise image blocks. Grok and
  similar agents read those files with their own tools.

- Keep sessions that are still running in one sidebar position while they stream:
  the running section and the phone list now order running sessions by creation
  time, with settled sessions following by recency, instead of moving a row every
  time an item is projected.

- Keep a paused reader's place when earlier conversation history pages in: the
  manual prepend offset is only applied while the viewport still sits where the
  page was requested, so native scroll anchoring or a deliberate scroll during
  the load no longer gets the inserted height added a second time.

- Discover models advertised by ACP engines such as Grok on `session/new`, so
  the composer lists `grok-4.6` / `grok-4.5` instead of only Agent default.
  Launch Grok with `--no-auto-update` and `--no-leader` so that probe is not
  attached to a shared CLI leader. Complete a Grok turn on
  `x.ai/session/prompt_complete` when the prompt RPC hangs, decline ACP
  filesystem and terminal host capabilities, and look up Devin, Hermes, and Pi
  in their native install directories.

- Reveal desktop wallpaper through the Automation plugin and embedded workspace
  plugin pages, while keeping overlapping plugin views opaque.

- Reveal desktop wallpaper through empty sessions, loading panes, skills,
  collaboration, and split-conversation canvases, and under the rounded
  composer, instead of covering them with the conversation paper fill.

- Allow saving Agent commit attribution while conversations run, applying it to
  active conversations after their work settles. Keep settings-row errors below
  controls so long messages do not squeeze labels into a narrow column.

- Keep enabled settings switches compact by using the shared switch track instead
  of painting the taller click target as a second background.

- Keep settings save and provider-removal failures in Settings instead of the
  conversation composer, and preserve conversation progress and errors after
  successful settings changes.

- Honor explicit model selections when resuming or forking `wuu exec` sessions.
  Preserve the busy-session exit code when a resume includes a model selection.
  Include stdin in CLI deadlines, settle runs on SIGTERM and disconnected output,
  and allow cancellation when stdout is stalled. Bound pending events and reject
  human interaction requests in noninteractive execution.

- Preserve conversation following when tool/reasoning details expand or collapse.
  Restore submission reading space temporarily occupied by details, while actual
  response growth and deliberate scrolling still consume it.

- Preserve final Responses replies and refusals over SSE and WebSocket when
  text deltas are missing or incomplete, without duplicating streamed content
  or issuing extra model requests. Reconcile corrections across tool-call
  boundaries and keep WebSocket continuation history aligned with final replies.

- Retry provider-reported `request_timeout`, `408`, and `504` stream errors
  within the existing retry budget and tool replay safety checks, rather than
  stopping the conversation after the first request.

- Preserve completed tool batches' text budget through provider requests and
  history replay, while retaining media and structured recovery data (#284).

- Make sidebar groups and nested workspace folders expand and collapse in one
  continuous motion, without inheriting a parent's temporary height or jumping
  at the end. Rapid reversals preserve rows, and reduced motion closes immediately.

- Honor explicit Responses turn-continuation signals across HTTP and WebSocket
  without retrying ordinary empty completions. Bound consecutive tool-free
  continuations, including Anthropic pauses, and reject unfinished compact summaries.

- Refresh `grep` and `glob` first pages from current files after edits, including
  external changes and reopened sessions, while keeping continuation pages bound
  to their exact result snapshot (#277).

- Make hook exit code 2 reliably block even with JSON output, pass rewritten tool
  arguments to subsequent hooks, retain all post-tool context across later hook
  failures, and route rich error results to failure hooks without changing the
  original tool outcome.

- Bound command-hook output and pipe draining, terminate running hook descendants
  on cancellation, and preserve cancellation and deadline error identities.

- Prevent matching pre-tool hooks from retaining early or concurrent execution
  privileges derived from tool arguments they can rewrite.

- Avoid a data race between streamed tool execution and final-call metadata
  updates by keeping each started call's scheduling decision stable.

- Preserve byte-range recovery for archived tool output when escaped text or
  short lines exceed the line-projection budget, so continuation neither skips
  remaining output nor switches to unrelated line ranges.

- Bound structured tool-result previews when field names or numeric values are
  unusually large, retaining complete data and a snapshot-bound recovery cursor.

- Treat Chat Completions stream error payloads and error finish reasons as failed
  attempts, preserving provider details and recovery limits without executing
  unfinished tool drafts (#263).

- Accept SSE response events up to 16 MiB across OpenAI-compatible and Anthropic
  streams, matching the Responses WebSocket limit. Oversized events now retain a
  local receive-limit diagnostic instead of an internal error, without futile
  retries or transport fallback.

- Preserve top-level Responses stream errors and recover recognized transient
  failures within existing retry budgets and tool replay safety checks. Failed
  streams now retain retry counts and stopping reasons across session reloads,
  with expandable desktop diagnostics instead of misleading network labels (#279).

- Recover crashed desktop renderers with bounded automatic retries and a native
  reload/close fallback. Stop orphaned window terminals and avoid sending events
  to unavailable frames while the window recovers.

- Honor cron weekday `7` as Sunday, including lists and stepped ranges, so
  Automation creation, updates, and recurring scheduling no longer reject or
  skip selected Sundays (#272). Previously saved next-run times are preserved;
  on desktop, edit a field such as the task name, then choose **Save changes**
  to recalculate immediately. The cron expression can stay unchanged; saving
  is disabled until the task is edited. Without saving, a one-shot task keeps
  its old deadline. Paused tasks stay paused, including after saving.

- Keep collaboration-managed sessions out of ordinary sidebar groups after
  switching projects by preserving their management state in persisted session
  lists and metadata updates.

- Remove the remaining `request_handoff` model tool after retiring `/handoff`,
  preventing agents from invoking the removed command through the tool runtime.

- Keep Automation and other primary plugin views in sidebar navigation instead
  of reviving the removed conversation tab strip. Preserve view switching,
  closing, return navigation, and durable view recovery across desktop restarts.

- Preserve fresh-context recovery instructions through checkpoints, turn completion
  and session reloads, preventing unintended system-prefix changes (#265).

- Accept normal provider completion without final text instead of retrying or
  failing it. Preserve transport failures, abnormal stops, and truncation metadata.
  Peer results join active work when possible, with late receipts retained for
  a follow-up rather than one queued turn per result.

- Stop streaming auto-follow from pulling messages back to the bottom when a
  keyboard, touch, or scrollbar gesture takes control before native scroll delivery.
  Preserve following after a plain scroll-surface click without scrolling.

- Reject `apply_patch` file sections that reuse a normalized path, including move
  sources and destinations, before writing any files. This prevents later sections
  from silently overwriting earlier edits; dry runs reject the same conflicts (#258).

- Preserve peer request correlation after uncertain sends, recover queued replies
  after host restart without reviving user-cancelled inputs, and retain completed
  results instead of replacing them with timeout messages. Peer discovery and
  sends now consistently stay within the current workspace.

- Align expanded tool details and TODO text in one column, center their icons on
  the first text line at any UI font size, and wrap long TODO content safely.

- Count tool results with a denser JSON token estimator so JSON-heavy history
  triggers compact before the provider window, and force-trim older history
  after a context overflow if compact does not shrink the request.
- Highlight the current running session in the sidebar bell view using the same
  selection and pending-switch states as the workspace session list.
- Preserve bottom-follow when expanding or collapsing conversation tool groups,
  without pulling readers away from an explicitly paused reading position.
- Position locally queued and steered messages when they enter the conversation,
  reserving response space unless the user has since scrolled or changed threads.
- Keep the ordinary conversation viewport clear of the composer and visible
  status capsules, without reserving empty capsule rows or duplicating bottom spacing.

- Prevented overlapping automation timer ticks from leaving a run stuck in
  starting before the session send settled.
- Restore draggable width adjustment for Collaboration conversation settings,
  with keyboard controls, saved width, and bounds that preserve chat space.

- Prevented streamed replies from jumping at completion by preserving prose
  geometry, reserving action space, and allowing active text fades to finish.
- Outline inline image artifacts with a subtle border and rounded corners so
  light images remain distinct from the conversation background.

- Center code-block text within balanced vertical padding and align copy actions
  with the first code line across code sizes and Markdown previews.

- Fixed duplicate sidebar navigation nodes after forking worktree conversations
  by preserving workspace identity and reconciling cached sidebar membership.

- Prevented transient busy indicators when switching cached desktop conversation
  tabs, while retaining send protection until background selection completes.
- Kept newly created Named Agent-managed sessions visible in the Harness sidebar
  immediately, without requiring a restart or losing their management relationship.
- Preserved Named Agent room images through wake delivery and history reads, using
  the same model media policy as ordinary conversations without duplicating attachments.
- Fixed selecting an automatically discovered Grok Build connection in existing
  and new conversations without changing workspace defaults.
- Kept the extension catalog visible after a failed reload and routed reload
  feedback through the shared top notice, with localized notices for busy tasks.
- Kept inline images and subsequent streamed text in chronological order without
  moving text across images at completion, and reserved stable image preview space.
- Kept streaming process text visible when providers revise a provisional final
  answer into commentary before tool calls, including DeepSeek V4 Pro.
- Corrected external-engine token accounting for reasoning, cache writes, and
  replayed usage notifications.
- Preserved terminal failure details and distinguished interrupted external turns
  from successful completion.
- Read stream-result error arrays and limit subtypes, and rejected malformed
  terminal payloads instead of reporting success.
- Answered reverse-RPC requests with string identifiers without losing their
  original identifiers or numeric precision.
- Positioned Collaboration message history before paint to avoid a visible scroll
  jump on entry, while preserving the reading position during message updates.
- Recovered external-engine conversations whose saved agent session can no longer
  be loaded, instead of failing every later turn: the turn continues in a new
  agent session and says so in the transcript. Applied to the ACP engines and
  OpenCode, and the agent's stderr tail now accompanies a failed turn so the
  cause (an unconfigured provider, a refused sign-in) is visible.
- Applied the unconfined permission mode to an ACP engine's own no-prompts mode
  when it advertises one, so an agent such as Devin no longer keeps a default
  that auto-accepts edits behind Wuu's approval path.

## [2026.9.2] - 2026-09-15

### Changed

- Native conversation views no longer present an explicitly cancelled, interrupted turn as a new failure; partial answers and actual execution failures remain visible.

- Native agent composers preserve command text instead of applying keyboard spelling corrections or iOS sentence capitalization.

- Enabled desktop phone access recovers after temporary startup failures or a remote-host exit, while respecting explicit disablement, account changes, and app shutdown.

- Remote hosts sharing one Wuu home now hold a process lock, preventing Dev and packaged desktops from repeatedly replacing each other at the relay. The lock is released by the OS after an exit or crash.

- Native phones reconnect independently of history synchronization, retain visible messages during recovery, and keep a healthy connection when a saved conversation is unavailable.

- Restored the last authorized computer, workspace, conversation, and collaboration
  room after restarting the native iOS and Android apps.

- Bundled macOS Computer Use with persistent self-signed release identity checks
  and guidance for manual upgrades from GitHub Releases.
- Waited for core and computer-use preview processes to stop before desktop exit.

- Unified skill and plugin catalog artwork with original, theme-aware capability
  icons while preserving custom plugin artwork; removed the Herbarium test theme.
- Tightened the new-automation form into grouped cards with hairline rows.
- Defaulted new Codex subscription configs to GPT-6 Astra and kept the 1.05M
  catalog window on live Codex discovery instead of the GPT-5 400k clamp.
- Asked the Agent to skip tool-call preambles unless it has a finding,
  interpretation, or blocker to add.

## [2026.9.1] - 2026-09-12

### Changed

- Switched product releases to UTC Calendar Versioning (`YYYY.M.N`).
- Synchronized desktop and active native app metadata from the root product
  version source.

## [0.18.0] - 2026-08-16

### Added

- Added workspace session reordering with persisted sidebar order.
- Added scope-bound host data subscriptions and a cross-runtime bundle contract
  for extensions.
- Exposed thread titles and final assistant replies to Stop hooks.

### Changed

- Refined long-paste folding and preserved its layout across tab switches.
- Expanded skin-overridable theme tokens for skill mark palettes.
- Updated supported model reasoning tiers and context-window metadata.
- Simplified plugin onboarding and clarified runtime state in the skills catalog.

### Fixed

- Refreshed media policy when switching models.
- Kept live token counts stable and limited their animation to visible changes.
- Preserved sidebar ordering while projects load and allowed collaboration
  sessions in the ordered list.
- Kept the composer and conversation flow aligned with the real scrollbar
  gutter.
- Derived completed turn duration from boundary timestamps when providers omit
  an explicit duration.
- Normalized Windows SQLite file DSN paths.

## [0.17.0] - 2026-08-14

### Added

- Added a read-only plugin host data query service so plugins can surface
  conversation context, with queries filtered by type and turn.
- Exposed active conversation thread IDs to plugins across message and
  timeline surfaces.
- Added live thinking token counts, persistent token totals, and execution
  state markers to desktop process rows.
- Added a first-launch model provider setup flow that guides users to
  configure a provider when none is set.
- Added macOS system notifications for finished turns and user questions.
- Added an inline panel form for user questions and expiration of unanswered
  questions after five minutes.
- Added sidebar activity aggregation across workspaces and plugin development
  reporting for how changes take effect.

### Changed

- Reworked process preview rendering to synthesize summaries from tool
  activity and signal turn completion structurally instead of a commentary
  phase.
- Replaced the plan plugin with the todo plugin.
- Hot-reloaded effective configuration changes in the app server.

### Removed

- Removed the Goal capability and its bundled plugin.
- Removed the bundled single-pass model-loop driver.
- Removed like/dislike feedback buttons from message actions.

### Fixed

- Retried HTTP/2 stream resets as transient network failures.
- Kept plugin-wrapped turns on the conversation grid and cleaned stale plugin
  binaries and empty command directories.
- Stabilized streaming text, context compaction notices, and composer
  alignment in the desktop UI.
- Cleared the composer after IME sends and exited cleanly when the dev
  renderer disappears.
- Resolved fork projection origins by provider source ID.

## [0.16.0] - 2026-08-12

### Added

- Added versioned plugin services for Go and TypeScript plugins, including
  generation-safe registration, execution identity, cancellation, host calls,
  settings pages, workbench views, conversation presentation, and theme
  surfaces.
- Added replaceable model-loop drivers with a bundled single-pass driver and
  persisted per-session driver checkpoints.
- Added execution-bound user questions so tools and plugins can pause for a
  response and resume the correct turn.
- Added richer plugin development and inspection workflows, including source
  watching, compatibility checks, artwork, runtime diagnostics, and live
  execution visibility.

### Changed

- Moved Goal, Subagent, Memory, Dream, and Automation capabilities behind the
  first-party plugin runtime, reducing core coupling while preserving their
  desktop experience.
- Strengthened plugin activation and replacement with scoped storage,
  generation leases, transactional lifecycle updates, service draining, and
  explicit capability error policies.
- Refined the desktop conversation, composer, workspace tabs, settings,
  Mermaid previews, context displays, plugin catalog, and theme contracts.
- Removed experimental voice input, computer use, and embedded browser
  capabilities from production desktop builds while retaining explicit
  development opt-ins.

### Fixed

- Preserved `PostToolUse` hook context across concurrent tool batches and
  isolated request-only context by tool call.
- Kept queued, steered, interrupted, and plugin-submitted turns attached to
  the correct session and execution lifetime.
- Prevented stale plugin generations, failed registrations, and unavailable
  services from replacing or leaking into the active runtime.
- Improved desktop responsiveness during streaming, hidden conversations,
  long process output, sidebar activity, and context compaction.
- Tightened sandbox, authorization, hook, and plugin-tool execution boundaries
  so unsupported or unavailable protection fails closed.

## [0.15.0] - 2026-08-05

### Added

- Added a local plugin package platform with desktop and CLI workflows for
  installing, reviewing, approving, updating, and removing packages.
- Added plugin-provided runtime tools, persisted plugin settings, approved
  desktop modules, and replaceable renderer surfaces.

### Changed

- Made plugin activation and updates atomic and fingerprint-gated, so failed or
  unapproved package generations cannot replace the active runtime.
- Enforced filesystem and process boundaries for plugin and shell execution.
- Simplified agent memory storage by removing legacy compatibility paths.

### Fixed

- Kept document drawer transitions, queued messages, and subagent wait status
  accurate during long-running work.
- Reduced repeated workspace permission scans and isolated expensive long-turn
  rendering to improve desktop responsiveness.
- Prevented failed runtime registrations and stale plugin policy from surviving
  package updates or removal.

## [0.14.0] - 2026-08-05

### Added

- Released collaboration as a standard desktop capability, with persistent
  rooms, mentions and replies, room tasks, global named Agents, and a shared
  collaboration graph.
- Added editable Agent details, dedicated Agent workspaces and memory access,
  plus reset and model-change controls that preserve runtime identity.
- Added usage trend and model charts, persisted skill-call metrics, and model
  catalog refresh from Settings.
- Added reviewable workspace diffs with a file tree and full-width Monaco diff
  editor for inspecting changes without leaving the app.

### Changed

- Rebuilt room creation, group details, member management, and Agent settings
  into denser session-based workflows that stay available across tab switches.
- Improved background-agent orchestration so parent sessions remain visibly
  active while delegated work, steering, and follow-up waits are still running.
- Simplified tool result continuation and aligned model effort and media support
  with the active provider catalog.
- Refined the home composer spacing while preserving a comfortable writing area,
  the full conversation composer, and attachment workflow.

### Fixed

- Kept running and unread session state live across folded workspaces, so opening
  another workspace reveals its current status immediately.
- Preserved room views, replies, scroll position, message identity, archived
  rooms, and Agent activity across navigation and development restarts.
- Stabilized sidebar actions, session tabs, menus, composer sizing, operation
  errors, and long-running conversation rendering.
- Corrected installed CLI version detection, provider credential reporting, and
  automation-safe JSONL output.
- Reconciled held steer responses after their requests settle, preventing stale
  pending composer state from surviving a completed or failed steer.

## [0.13.1-nightly.3] - 2026-08-02

### Added

- Added collaboration rooms, global Agents, and room tasks to the unified
  sidebar and session-tab workflow.
- Added reset support for running named agents, including cross-process wake
  recovery.

### Changed

- Simplified the channel canvas to a single-column conversation layout and
  moved numeric room unread counts onto the sidebar session-status axis.
- Unified provider media admission so text-only agents no longer receive image
  inputs they cannot process.

### Fixed

- Kept rooms read while they remain visible, preventing a message received in
  the active room from appearing unread after switching away.
- Preserved clean Markdown reply summaries and allowed their author controls to
  mention agents without accidentally opening the thread.
- Kept channel-room tab context, composer alignment, response status, and agent
  activity accurate across layout and app-server changes.

## [0.13.1-nightly.2] - 2026-08-01

### Added

- Added agent activity snapshots to the collaboration graph: hovering an agent
  shows its effective model, recent attributable file changes, language mix,
  and token usage.
- Added persisted unread counts to the channel list.

### Changed

- Simplified the agent preview card and mention selection styling to a quiet,
  flat treatment.
- Grouped nearby consecutive channel messages from the same author under one
  identity header.

### Fixed

- Accepted large attachment requests so sending multiple images no longer
  fails silently.
- Preserved Markdown rendering in collapsed long channel messages.
- Disabled runtime slash commands in channel composers.
- Cascaded task owner candidates to the selected room's agent members.
- Scoped agent response status to its room, and kept the mention picker,
  avatar validation, and room setup controls aligned.

## [0.13.1-nightly.1] - 2026-08-01

### Changed

- Enabled group chat in packaged desktop builds so the existing
  channels, room management, mentions, replies, and collaboration views can be
  tested as a prerelease.
- Marked semver prerelease tags as GitHub prereleases instead of promoting them
  as the latest stable release.

## [0.13.0] - 2026-08-01

### Added

- Added a desktop Automations center with workspace-bound creation,
  project-bound tasks, friendly schedule controls, and a responsive resizable
  detail pane.
- Expanded group chat with room renaming and dissolution, dynamic group
  avatars, a dedicated room details sidebar, a searchable member picker, and a
  continuously arranged collaboration graph.
- Added richer channel conversations with current-room `@` mentions, active
  Agent response status, compact consecutive messages, reply previews, and a
  resizable thread panel.
- Added searchable localized time zones, unified hover tooltips, an embedded
  browser proxy, and confirmable AI-generated commit messages to the desktop
  app.

### Changed

- `unconfined` permission mode now lifts only the path boundary and keeps a
  protection floor in every mode. Sensitive paths (`.env`, SSH private keys,
  credential configuration) can no longer be written through file tools or
  staged/committed through git in `unconfined`; reads of sensitive files
  reach the model with common secret patterns masked; command-output
  redaction stays on. The app's own credential files under the wuu home
  (`auth.json`, `credentials.json`, `remote.json`, `phone.json`) are now
  unreadable and unwritable through agent tools in every mode — previously
  `standard` mode could overwrite them through the runtime-metadata
  exemption. The sensitive-path list also covers `id_rsa`/`id_ed25519`/
  `id_ecdsa` key files, and output redaction now masks PEM private-key
  blocks.

- Background command records now add durable owning-conversation and app-server
  host-generation data. The owning conversation is taken from host state rather
  than a model argument. This is additive record data only: no lifecycle cleanup
  or cascade behavior is implemented yet, and legacy `lifecycle` data continues
  to parse and round-trip.

- Slash command rows now lead with the command to type (`/review`) and carry a
  short summary beside it, so built-in commands and skills read the same way.

- Subagent wake and completion updates now merge into quieter turn-level groups
  so delegated work stays readable without fragmenting the conversation.
- Channel and automation catalog layouts now share a denser, more consistent
  reading axis and simpler detail surfaces.

### Removed

- Removed Wuu's provider-neutral `wuu_tool_search` progressive tool loading.
  Appending schemas to the top-level `tools` array mid-conversation invalidated
  the provider prompt-cache prefix past the insertion point, so every load
  risked another cold prefix. Loading is now `native` where the provider and
  model support deferred discovery and `flat` everywhere else, which keeps the
  request prefix stable for the fixed cost of the full tool schema. The local
  search executor stays: provider-native discovery still needs it to search the
  catalog and return loadable schemas. Existing configs setting
  `agent.tool_loading` to `wuu_tool_search` (or the `tool_search` alias, or
  `tool_search: true`) keep starting and now resolve to `auto`, printing a
  one-time deprecation notice. Explicit `native` on a provider or model without
  native discovery now falls back to `flat` with a visible notice instead of
  silently selecting Wuu progressive loading.

### Fixed

- Fixed the composer slash command panel hiding every skill until the user typed
  a search query, showing skill rows without their `/<name>` command, and
  labelling the skills catalog entry `Browse Skills` while it opened a catalog
  tab.
- Fixed Git attribution in Windows Git Bash shells when Git resolves without an
  `.exe` suffix.
- Fixed a white screen on the Automations tab when no automation records
  existed, caused by the Go app-server returning `null` for an empty task list
  while the renderer called `.some()` on it.
- Fixed interrupted and replayed subagent completion handling, stale child
  status after restart, and several orchestration recovery paths.
- Fixed Codex model discovery blocking or overwriting a model selection made
  while the model list was still loading.
- Fixed channel composer, status, member-picker, message-flow, and empty-room
  layout regressions across narrow and resized views.
- Fixed paused automation drafts, responsive automation details, stale preload
  settings, off-screen desktop menus, and unclickable toast actions.

## [0.12.2] - 2026-07-25

### Added

- Added conversation search across workspaces so matching sessions can be found
  without first switching to their project.
- Added in-app PDF previews to the workspace file viewer.

### Changed

- Redesigned the desktop skills catalog with clearer grouping, compact summaries,
  simpler headings, and distinctive deterministic artwork for custom skills.

### Fixed

- Preserved composer focus when starting a conversation in another project.
- Kept PDF pages white while making the surrounding viewer and toolbar follow the
  active light or dark theme.

## [0.12.1] - 2026-07-25

### Changed

- Simplified the main Agent's delegated-work guidance so completed subagent tasks
  are always integrated and verified before the overall task is reported complete,
  while per-message result-card instructions stay out of the stable base prompt.
- Restored concise progress updates before non-trivial tool use and during longer
  work so users can see what the Agent is doing without narrating every action.

## [0.12.0] - 2026-07-25

### Added

- Added the agent collaboration workspace with room chat, message attachments,
  Markdown rendering, room membership context, a draggable relationship graph,
  a task board, threaded replies, and jump-to-latest controls.
- Added native macOS voice input with saved preferences, optional BYOK text
  polishing, live recording feedback, and a responsive waveform beside the
  composer send control.
- Added desktop skill preview dialogs that show the underlying Skill content and
  keep the preview body scrolling inside a stable modal.
- Added cross-session Dream memory consolidation and stable continuation for
  large projected tool results.

### Removed

- Removed the Agent Templates section from the desktop skills catalog and the
  underlying runtime discovery in `internal/agenttemplate/`. The desktop
  rendered Claude Code-style `.claude/agents/*.md` files when present, but
  nothing in the desktop UI or in `spawn_agent` ever consumed the
  discovery to trigger work, so the section was effectively dead UI.
  Restoring it later is a small change once a real spawn/invoke path
  exists. Drops the `agent-template/list` IPC method, the `agent_template`
  extension kind, the `AgentTemplate*` protocol types, and the
  `agent_template_count` field on initialize.

### Fixed

- Fixed resumed goals so they start a continuation turn instead of only changing
  the banner status, and clarified idle active goals as ready to continue.
- Returned a recovery-focused error when the goal tool tries to complete a
  blocked or paused goal before the user resumes it.
- Improved channel reliability and reading behavior across repeated agent wakes,
  missed inbox checks, resize, message polling, scrolling, and composer overlap.
- Kept voice transcripts stable through polishing and direct-send flows, avoided
  duplicate transcription, and steered running turns consistently from voice,
  Enter, and the send button.
- Let completed Agent command output use the full terminal workspace width so
  long paths and diff statistics no longer wrap against an empty terminal list.
- Fixed responsive session actions, side-chat composer restoration, hero composer
  focus after send failures, conversation status alignment, plan progress width,
  and the Windows conversation search shortcut hint.
- Centered the skills catalog content within its scroll region on the desktop.
- Removed count badges from the skills catalog section headings on the desktop.

## [0.11.1] - 2026-07-23

### Fixed

- Restored the standard left-sidebar toggle in full-panel workspaces, including
  hover preview, pinned expansion, and native macOS titlebar hit testing.

## [0.11.0] - 2026-07-23

### Added

- Added a cloud agent core host contract, configurable Dream settings,
  subagent model aliases, and named-agent group chat support in the core.
- Added native macOS file actions and a dockable workspace file tree with
  document-focused turn and final-answer views.

### Changed

- Consolidated pending and document-run messages into clearer composer drawers
  with consistent progress, selection, and action behavior.
- Made project and conversation switching immediate, and allowed projects to be
  switched directly while browsing files without leaving the full-panel view.

### Fixed

- Fixed IME confirmation and composition timing so confirmed text sends once,
  while queued input remains available during agent delivery.
- Fixed workspace navigation details including real file-menu app icons,
  file-tree context actions, sidebar restoration, scrolling intent, and stream
  cursors around fenced code blocks.

### Removed

- Removed the Agent Templates section from the desktop skills catalog and the
  underlying runtime discovery in `internal/agenttemplate/`. The desktop
  rendered Claude Code-style `.claude/agents/*.md` files when present, but
  nothing in the desktop UI or in `spawn_agent` ever consumed the
  discovery to trigger work, so the section was effectively dead UI.
  Restoring it later is a small change once a real spawn/invoke path
  exists. Drops the `agent-template/list` IPC method, the `agent_template`
  extension kind, the `AgentTemplate*` protocol types, and the
  `agent_template_count` field on initialize.

## [0.10.1] - 2026-07-21

### Added

- Added drag-and-drop from the workspace file tree into composers as path
  references, plus external file drops through the existing attachment checks
  and a 20 MB limit for PDF attachments.

### Fixed

- Preserved the selected final assistant response when deriving a conversation,
  including histories with compaction, provider checkpoints, or retired context
  artifacts, and unified history item projection so derivation and message
  editing use the same canonical source mapping as the visible transcript.
- Redrew the system-theme preview as one aligned split-color window, removing
  overlapping and clipped light/dark layers in Settings.

## [0.10.0] - 2026-07-21

### Added

- Added scheduled progress rechecks for managed background processes
  (`recheck_minutes` on `bash` start/update) so long silent tasks such as
  downloads wake the agent with periodic status snapshots until completion.

### Changed

- Made `bash` background observation bounded and event-driven:
  `read_background` waits return early on new output or process exit with
  pacing for continuously producing processes, and foreground commands that
  hit their timeout now keep running as managed background processes with
  their output attached instead of being killed.

## [0.9.0] - 2026-07-21

### Added

- Added durable app-server Run control for `wuu exec`, including persisted run
  state, structured-output validation, cancellation, and resume support.
- Added read-only agent tools and live agent process items to side threads.

### Changed

- Routed `wuu exec` through the app-server execution model and made JSONL
  terminal states and structured error categories reliable for automation.
- Refined desktop conversation alignment, composer focus behavior, and floating
  status controls.

### Fixed

- Rejected unsupported attachments and unknown machine-input fields before a
  run starts, while preserving the original intent of valid image inputs.

## [0.8.0] - 2026-07-20

### Changed

- Background subagent completions are now steered into the current parent turn
  and injected before the next model step, instead of waiting for the whole turn
  to end. This reduces redundant exploration while the subagent is finishing.

## [0.7.2] - 2026-07-20

### Added

- Added collapsed-sidebar preview when hovering the titlebar sidebar toggle.

### Changed

- Aligned `apply_patch` completion messages and client file-change records with
  Codex while removing redundant patch journals and metadata.

### Fixed

- Preserved trailing Markdown emphasis markers during streamed rendering.

## [0.7.1] - 2026-07-19

### Changed

- Reused composer controls in the user message edit bubble for consistent styling.

## [0.7.0] - 2026-07-19

### Added

- Added Kimi K3 Anthropic compatibility, including K3-specific thinking and empty-signature handling.
- Added context-overflow detection, recovery, and user-visible display for BYOK providers.
- Added dynamic request context compaction, reducing how much transient context enters model requests.
- Added a redesigned Settings page using a flat chassis with instant-apply controls and hover drawers.
- Added token-speed display via hover tooltip on narrow composer bars.
- Added settled agent runs in the terminal workspace surface.
- Added support for diversified subagent name pools.
- Added the ability to switch projects while background runs are still active.
- Added a bilingual user guide covering core desktop and agent workflows.

### Changed

- Unified terminal session history into a single shared history model.
- Dropped derived context ledgers from the default model projection.
- Simplified the sidebar wordmark color treatment.
- Scoped environment Git configuration to the active session workspace.
- Memoized conversation turns on server-event re-renders to improve idle performance.
- Reduced idle renderer activity across the desktop conversation view.
- Kept process summaries neutral and hidden from the main conversation stream.
- Unified composer actions into a single plus menu.

### Fixed

- Interrupted turns now show answer actions and prevent stacked query overlap.
- Turn edit summary cards now appear before answer action buttons.
- Background completion wakeups are now waited on correctly before proceeding.
- Model-aware summary output limits are recovered after compaction.
- Messages can be edited after history compaction.
- Conversation layout is settled before reveal, preventing flicker on tab switch.
- Stream follow pauses during text selection.
- Stable single-line retry error display is shown while a turn retries or recovers.
- First query is shown before thread creation completes.
- Zero-usage model buckets are hidden from the settings usage panel.
- Synchronous shell process trees are stopped correctly.
- Terminal quota replays are stopped during recovery.
- OpenAI Responses WebSocket cache TTL is extended.
- Usage number overflow is prevented in heatmap and inline displays.
- Usage heatmap intensity is restored after theme changes.

## [0.6.0] - 2026-07-17

### Added

- Embedded browser activities can now be automated through a dedicated backend
  and shown in a live macOS picture-in-picture preview.
- The desktop interface now supports English and Simplified Chinese.
- Interrupted turns preserve queued follow-up messages for the next turn.
- Git commits can optionally include WUU Agent attribution.

### Changed

- Runtime model selection is now conversation-scoped, and side chats and
  automatic titles inherit the conversation's pinned model.
- Desktop settings use a grouped-list layout, hover drawers animate in and out,
  and the macOS sidebar preserves the wallpaper tint.
- The composer stop control now uses a smaller solid glyph on a neutral surface.
- The retired `inception` context-rewrite tool has been removed while legacy
  session artifacts remain readable.

### Fixed

- OpenAI Responses WebSocket failures now use reason-specific SSE fallback
  windows, allowing transient failures to recover without a ten-minute pin.
- Truncated tool arguments and empty Anthropic thinking blocks no longer break
  provider history recovery.
- Composer focus handoffs, held-message deduplication, and jump-to-latest state
  remain stable across sends, interrupts, and conversation switches.
- Embedded browser PiP startup and window reparenting no longer race.
- Production desktop builds disable reload and developer-tools shortcuts.
- Git action locks are scoped to the active worktree.

## [0.5.2] - 2026-07-16

### Fixed

- Inline code in message conversations and workspace Markdown previews now
  uses a dark theme-aware surface instead of retaining the light chip background.

## [0.5.1] - 2026-07-16

### Changed

- The message-flow font-size preview now uses the real conversation renderer,
  so it matches message typography, spacing, and Markdown output.

### Fixed

- Theme changes now stay synchronized across all desktop windows and embedded
  terminals, editors, and Mermaid diagrams.
- Bare URLs followed by CJK punctuation no longer absorb the punctuation into
  the link target.

## [0.5.0] - 2026-07-16

### Added

- Scheduled automations now run through persisted threads and turns, support
  new-thread and heartbeat modes, and retain bounded run history.
- The core and desktop source now include Windows-aware process, shell-path,
  window-chrome, and packaging support. GitHub Releases remain macOS-only.

### Changed

- The desktop conversation and composer stay centered at readable widths on
  wide windows, while environment and group panels scale responsively.
- Internal process notifications are hidden from conversations, and command
  and search activity summaries more accurately describe completed work.

### Fixed

- Failed and interrupted turns preserve completed assistant and tool history
  across reloads, including partial response text already shown before a stop.
- Scheduled runs avoid overlapping or unbounded queued work, expired paused
  schedules are removed, and corrupt run-history files no longer block startup.
- Desktop side-panel headers retain their intended layout and typography.

## [0.4.1] - 2026-07-16

### Changed

- GitHub Releases now contain only the unsigned macOS arm64 desktop DMG and ZIP
  packages; standalone CLI archives and the npm installer are no longer
  published.

### Fixed

- The desktop app now always runs its bundled private `wuu-core` and no longer
  installs, replaces, or falls back to a standalone `wuu` CLI. A CLI installed
  separately from source can coexist with the app and use a different version.

## [0.4.0] - 2026-07-15

### Added

- Large built-in tool results are settled once at execution time into bounded,
  artifact-backed projections with an explicit recovery path to the complete
  result.
- Structured and rich tool results now keep bounded semantic indexes through
  provider requests and checkpoint compaction, including stable content
  identity and representative values.
- Compacted media references preserve the media type, decoded size, SHA-256
  identity, and image dimensions when available.

### Changed

- Request-time historical tool-output pruning has been removed. Context growth
  is now handled by stable result projection followed by checkpoint compaction,
  so ordinary tool-loop requests keep an append-only cacheable prefix.
- Archived desktop sessions are grouped by project, and conversation search,
  model menus, memory controls, and narrow usage tables use a quieter layout.
- Release versions are synchronized from `VERSION`, validated in CI, and
  published with the matching changelog section.
- The npm wrapper installs the GitHub Release matching its own package version
  instead of silently resolving the latest release.
- Public evaluation claims now have a dedicated, CI-validated evidence format
  under `evals/`; private and exploratory runs remain under ignored `bench/`.

### Fixed

- Rich tool results survive session persistence instead of losing structured
  content or attachment metadata after a restart.
- Compaction and provider projection preserve meaningful mixed structured
  results without duplicating complete payloads into active model context.
- Queued desktop turns no longer hide the preceding final reply, and sidebar
  session loading no longer drops visible history after compaction.

## [0.1.0] - 2026-07-10

### Added

- Unsigned macOS arm64 Electron desktop preview DMG/ZIP artifacts on GitHub
  Releases
- `LICENSE` (MIT) so the project is unambiguously open source
- `CONTRIBUTING.md`, `SECURITY.md`, and `CODE_OF_CONDUCT.md` for open-source
  governance
- `CHANGELOG.md` to track user-visible changes
- `.github/CODEOWNERS`, `.github/ISSUE_TEMPLATE/` (bug report, feature
  request), and `.github/PULL_REQUEST_TEMPLATE.md`
- `.gitignore` entries for `coverage/`, `*.log`, `.idea/`, `.vscode/`, and
  `.env*` environment files
