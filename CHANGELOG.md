# Changelog

This file starts the maintained release record. Earlier GitHub Releases may not
have complete change notes.

The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).
Versioning rules are documented in [the release guide](docs/en/project/release.md).

## [Unreleased]

### Added

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

- Fetch lightweight conversation summaries for desktop lists without copying
  loaded histories, reducing IPC payloads and app-server allocation overhead.
  Recover missed completion events with targeted snapshots for stale loaded
  conversations, including both visible panes.

- Keep conversation search results title-only on the left, with matching snippets
  and conversation details in the right preview pane.

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

- Narrow outgoing desktop conversation bubbles to 560px or 80% of the message
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

### Removed

- Removed the `yield_turn` tool. Sessions use normal provider completion to end
  without an outward reply, including room discussions and peer follow-ups.

- Removed stale message-flow screenshots and the disposable output-card demo from
  tracked artifacts.

### Fixed

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
