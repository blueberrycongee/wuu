# Keyboard shortcuts

Enable **Settings → Keyboard shortcuts → Vim navigation** to navigate without leaving the keyboard. It is off by default and saved on this device. The title bar shows **NORMAL** for navigation and **INSERT** when an input or editor owns the keyboard. Click that indicator, or press `?` in navigation mode, to open the reference.

## Reading and conversations

| Key | Action |
|---|---|
| `j` / `k` | Scroll down / up |
| `Ctrl+d` / `Ctrl+u` | Scroll half a page down / up |
| `gg` / `G` | Go to the top / bottom |
| `i` | Focus the conversation composer |
| `Esc` | Leave the composer or cancel a pending sequence |
| `/` | Search conversations |
| `gt` / `gT` | Next / previous open conversation or draft, wrapping at the ends |
| `Space n` | Start a conversation in the current workspace; return to its existing draft if one is open |
| `?` | Show keyboard shortcuts |

Reading keys scroll the current task conversation pane, or the settings page while settings are open. `gt` and `gT` follow the order of conversations and drafts opened in this window; use `/` for other saved conversations. Switching conversations retains drafts through the same navigation path as the sidebar.

## Workspace tools

Press and release Space, then the second key. A hint panel lists the available commands. Complete Space and `g` sequences within two seconds. Escape, clicking, changing focus, or switching away from the window cancels the sequence. Holding a prefix key does not complete a command.

| Key | Action |
|---|---|
| `Space s` | Show or hide the sidebar |
| `Space w` | Show or hide the workspace panel |
| `Space f` | Open files |
| `Space t` | Open the terminal |
| `Space d` | Review changes |
| `Space b` | Open the browser |
| `Space ,` | Open general settings |

Commands use the tools available to the current window and connection. Pop-out conversation windows do not open workspace panels.

## Input and focus

Vim navigation is for the desktop workbench. It does not turn the composer or code editor into a Vim text editor. Text fields, IME composition, dialogs, menus, terminals, code editors, and embedded browser pages keep their own keyboard handling. Touch-only navigation is unchanged.

An Escape already used to dismiss a composer menu or stop generation takes priority. Press Escape again after that action to leave the composer. Escape inside a terminal or code editor remains in that tool; click the conversation to move focus out before using workbench navigation. Space on a focused button still activates it; Escape releases button focus.

Existing input shortcuts remain available: Enter sends, Shift+Enter inserts a line break, and Command/Ctrl+Enter queues a draft when supported. Command/Ctrl+P opens conversation search. Copy, paste, selection, undo, and Tab navigation retain their normal behavior. See [conversations](conversations.md#while-a-task-is-running) for sending and stopping details.

Movement keys follow the [Vim quick reference](https://vimhelp.org/quickref.txt.html); the Space commands are Wuu's application navigation bindings.
