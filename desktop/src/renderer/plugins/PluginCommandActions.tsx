import { Tooltip } from "../Tooltip";
import { Fragment, useCallback, useEffect, useMemo, useRef, useState, useSyncExternalStore, type ReactNode } from "react";
import { isPluginLifecycleCancellation } from "../../shared/PluginLifecycle";
import { FloatingMenuPortal, handleFloatingMenuKeyDown, menuOpeningKey, useFloatingMenuFocus } from "../ComposerFloatingMenu";
import { isTouchWebShell } from "../ComposerFocus";
import { AlertCircle, Ellipsis } from "../WuuIcons";
import type { PluginCommandActionContext } from "../../shared/workbench";
import { PublicIcon } from "../PublicIcon";
import { useI18n } from "../i18n";
import type { UICommandActionV1 as PluginCommandAction, UICommandActionResolutionV1 as PluginCommandActionResolution, UIContributionRegistryV1 as PluginHost, RegisteredCommandV1 as RegisteredPluginCommand } from "../../../../packages/plugin-sdk/src/ui-contributions";

const EMPTY_RESOLUTION: PluginCommandActionResolution = Object.freeze({ actions: Object.freeze([]), failures: Object.freeze([]) });

/** Resolve only existing commands; no context or placements means no host-row footprint. */
export function usePluginCommandActions(host: PluginHost, context: PluginCommandActionContext | undefined): readonly PluginCommandAction[] {
  const subscribe = useCallback((listener: () => void) => host.subscribe(listener), [host]);
  const getSnapshot = useCallback(() => host.getCommands(), [host]);
  const commands = useSyncExternalStore(subscribe, getSnapshot, getSnapshot);
  const resolution = useMemo(() => context === undefined ? EMPTY_RESOLUTION : host.resolveCommandActions(context), [host, context, commands]);
  useEffect(() => {
    for (const failure of resolution.failures) host.recordCommandFailure(failure.command, failure.error);
  }, [host, resolution]);
  return resolution.actions;
}

/** Native action-row contributions use the surrounding host's spacing and button styles. */
export function PluginCommandActions({ host, context, buttonClassName = "settings-button settings-button-ghost settings-icon-button" }: {
  host: PluginHost;
  context: PluginCommandActionContext;
  buttonClassName?: string;
}): ReactNode {
  const actions = usePluginCommandActions(host, context);
  const { t } = useI18n();
  const [open, setOpen] = useState(false);
  const [focusFromEnd, setFocusFromEnd] = useState(false);
  const trigger = useRef<HTMLButtonElement | null>(null);
  const menu = useRef<HTMLDivElement | null>(null);
  const target = context.target === "view.title" ? `view:${context.viewId}` : `message:${context.threadId}:${context.turnId}:${context.item.id}`;
  const authority = useMemo(() => ({ active: true, pending: new Set<RegisteredPluginCommand>() }), [host]);
  const [busy, setBusy] = useState<ReadonlySet<RegisteredPluginCommand>>(new Set());
  const [failures, setFailures] = useState<ReadonlyMap<RegisteredPluginCommand, { target: string; error: unknown }>>(new Map());
  useEffect(() => {
    authority.active = true;
    setBusy(new Set());
    setFailures(new Map());
    return () => { authority.active = false; };
  }, [authority]);
  const invoke = (command: RegisteredPluginCommand): void => {
    if (!authority.active || authority.pending.has(command)) return;
    authority.pending.add(command);
    setBusy(new Set(authority.pending));
    setFailures((current) => { const next = new Map(current); next.delete(command); return next; });
    void (async () => host.executeCommandAction(command, context))().catch((error: unknown) => {
      if (!authority.active) return;
      try {
        const commands = host.getCommands();
        if (isPluginLifecycleCancellation(error) || !commands.includes(command)) return;
        host.recordCommandFailure(command, error);
        setFailures((current) => new Map(current).set(command, { target, error }));
      } catch (reason) {
        if (!isPluginLifecycleCancellation(reason)) throw reason;
        authority.active = false;
      }
    }).finally(() => {
      authority.pending.delete(command);
      if (authority.active) setBusy(new Set(authority.pending));
    });
  };
  const button = (action: PluginCommandAction, className: string, labelled = false): ReactNode => <CommandButton
    key={`${action.command.pluginId}:${action.command.id}`} action={action} buttonClassName={className}
    labelled={labelled} busy={busy.has(action.command)} failed={failures.get(action.command)?.target === target}
    onInvoke={() => invoke(action.command)} />;
  const overflow = actions.slice(2);
  useEffect(() => { setOpen(false); setFailures(new Map()); }, [target]);
  useEffect(() => {
    setFailures((current) => {
      const active = host.getCommands();
      if ([...current.keys()].every((command) => active.includes(command))) return current;
      return new Map([...current].filter(([command]) => active.includes(command)));
    });
  }, [host, actions]);
  useEffect(() => { if (overflow.length === 0) setOpen(false); }, [overflow.length]);
  useFloatingMenuFocus(menu, target, open, focusFromEnd);
  const noEnabledOverflow = overflow.every((action) => !action.enabled);
  useEffect(() => {
    if (!open || !noEnabledOverflow || isTouchWebShell()) return;
    // An all-disabled dialog still needs keyboard focus for Escape and Tab.
    const frame = requestAnimationFrame(() => menu.current?.focus({ preventScroll: true }));
    return () => cancelAnimationFrame(frame);
  }, [open, target, noEnabledOverflow]);
  useEffect(() => {
    if (!open) return;
    const dismiss = (event: PointerEvent): void => {
      if (!(event.target instanceof Node) || trigger.current?.contains(event.target) || menu.current?.contains(event.target)) return;
      setOpen(false);
    };
    document.addEventListener("pointerdown", dismiss, true);
    return () => document.removeEventListener("pointerdown", dismiss, true);
  }, [open]);
  if (actions.length === 0) return null;
  return <Fragment>
    {actions.slice(0, 2).map((action) => button(action, buttonClassName))}
    {overflow.length > 0 ? <Fragment>
      <Tooltip asChild content={t("settings.more")}><button ref={trigger} type="button" className={buttonClassName} aria-label={t("settings.more")} aria-haspopup="dialog" aria-expanded={open}
        onClick={() => { setFocusFromEnd(false); setOpen((current) => !current); }}
        onKeyDown={(event) => {
          if (open) { handleFloatingMenuKeyDown(event, () => setOpen(false), trigger.current); return; }
          const direction = menuOpeningKey(event);
          if (direction) { setFocusFromEnd(direction === "end"); setOpen(true); }
        }}><Ellipsis className="icon" aria-hidden="true" /></button></Tooltip>
      {open ? <FloatingMenuPortal anchorRef={trigger} owner="plugin-command-actions" placement="below" align="right" width={240} flip>
        <div ref={menu} className="composer-context-menu composer-plugin-tools-menu" role="dialog" tabIndex={-1} aria-label={t("settings.more")}
          onKeyDown={(event) => handleFloatingMenuKeyDown(event, () => setOpen(false), trigger.current)}>
          {overflow.map((action) => <div className="composer-plugin-tools-item" key={`${action.command.pluginId}:${action.command.id}`}>
            <span className="composer-plugin-tools-label">{action.command.title}</span>
            {button(action, "settings-button settings-button-ghost settings-icon-button", true)}
          </div>)}
        </div>
      </FloatingMenuPortal> : null}
    </Fragment> : null}
  </Fragment>;
}

function CommandButton({ action: { command, enabled }, buttonClassName, busy, failed, labelled, onInvoke }: {
  action: PluginCommandAction;
  buttonClassName: string;
  busy: boolean;
  failed: boolean;
  labelled: boolean;
  onInvoke: () => void;
}): ReactNode {
  const { t } = useI18n();
  const label = failed ? `${command.title}: ${t("error.commandFailed")}` : command.title;
  return <Tooltip asChild content={label} disabled={labelled && !failed}><button type="button" className={buttonClassName} aria-label={label}
    disabled={!enabled || busy} aria-busy={busy || undefined} onClick={onInvoke}>
    {failed ? <AlertCircle className="icon" aria-hidden="true" /> : <PublicIcon name={command.icon} className="icon" />}
  </button></Tooltip>;
}
