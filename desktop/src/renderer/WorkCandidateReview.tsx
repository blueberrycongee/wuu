import { useCallback, useState, useSyncExternalStore } from "react";
import type { ChannelWork, ChannelWorkArtifact, ChannelWorkCandidateResult } from "../shared/protocol";
import { desktopPluginHost } from "./plugins/DesktopPluginRuntime";
import { RichContent } from "./RichContent";
import { useI18n } from "./i18n";
import { showErrorToast } from "./Toast";

export function WorkCandidateReview({ work, artifact, onOpenSession }: {
  work: ChannelWork; artifact: ChannelWorkArtifact; onOpenSession?: (id: string) => void;
}): JSX.Element {
  const { t } = useI18n();
  const [review, setReview] = useState<ChannelWorkCandidateResult>();
  const [busy, setBusy] = useState(false);
  const [publishedURL, setPublishedURL] = useState("");
  const subscribe = useCallback((listener: () => void) => desktopPluginHost.subscribe(listener), []);
  const getCommands = useCallback(() => desktopPluginHost.getCommands(), []);
  const commands = useSyncExternalStore(subscribe, getCommands, getCommands).filter(command => command.contexts?.includes("work-candidate.publish"));
  const [publisher, setPublisher] = useState("");
  const selected = commands.find(command => `${command.pluginId}:${command.id}` === publisher) ?? commands[0];
  async function load(): Promise<void> {
    setBusy(true);
    try { setReview(await window.wuu.channelWorkCandidate({ work_id: work.id, artifact_id: artifact.id, action: "get" })); }
    catch (error) { showErrorToast(error); }
    finally { setBusy(false); }
  }
  async function decide(action: "apply" | "discard" | "publish"): Promise<void> {
    if (!review) return;
    setBusy(true);
    try {
      if (action === "publish") {
        if (!selected) return;
        const current = await window.wuu.channelWorkCandidate({ work_id: work.id, artifact_id: artifact.id, action: "get" });
        if (current.stale || current.artifact.disposition || current.work_revision !== review.work_revision) throw new Error(t("channels.candidate.refreshRequired"));
        const result = await selected.execute({ candidate: review.candidate, title: work.title }) as { url?: string };
        if (result?.url && /^https?:\/\//.test(result.url)) setPublishedURL(result.url);
      } else {
        setReview(await window.wuu.channelWorkCandidate({ work_id: work.id, artifact_id: artifact.id, action, expected_revision: review.work_revision }));
      }
    } catch (error) { showErrorToast(error); }
    finally { setBusy(false); }
  }
  const disposition = review?.artifact.disposition ?? artifact.disposition;
  const disabled = busy || Boolean(disposition) || review?.stale;
  return <details className="work-candidate-review" onToggle={event => { if (event.currentTarget.open && !review) void load(); }}>
    <summary>{t("channels.candidate.title")}{disposition ? ` · ${t(`channels.candidate.${disposition}`)}` : ""}</summary>
    {review ? <>
      {review.stale ? <p role="status">{t("channels.candidate.stale")}</p> : null}
      <RichContent text={review.candidate.report.result} />
      {review.candidate.report.implicit_choices.length ? <details><summary>{t("channels.candidate.choices")}</summary><ul>{review.candidate.report.implicit_choices.map((choice, index) => <li key={index}>{choice}</li>)}</ul></details> : null}
      <div className="work-candidate-evidence">
        <strong>{t("channels.candidate.checks")}</strong>
        {review.candidate.report.evidence_refs.length ? <ul>{review.candidate.report.evidence_refs.map((ref, index) => <li key={index}>{ref}</li>)}</ul> : <p>{t("channels.candidate.noChecks")}</p>}
        {work.candidate_artifact_ref === artifact.id && work.verification ? <p>{t("channels.candidate.verification")}: {t(`channels.verification.${work.verification.decision}`)} — {work.verification.report}</p> : <p>{t("channels.candidate.unverified")}</p>}
        {review.candidate.report.unresolved_items.length ? <ul>{review.candidate.report.unresolved_items.map((item, index) => <li key={index}>{item}</li>)}</ul> : null}
      </div>
      <button type="button" onClick={() => onOpenSession?.(review.candidate.session_id)}>{t("channels.candidate.session")}</button>
      <pre className="work-candidate-diff" tabIndex={0} aria-label={t("channels.candidate.diff")}><code>{review.candidate.diff || t("channels.candidate.noDiff")}</code></pre>
      <div className="channel-task-actions">
        <button type="button" disabled={disabled || !review.candidate.revision} onClick={() => void decide("apply")}>{t("channels.candidate.apply")}</button>
        {commands.length > 1 ? <select aria-label={t("channels.candidate.publisher")} value={selected ? `${selected.pluginId}:${selected.id}` : ""} onChange={event => setPublisher(event.target.value)}>{commands.map(command => <option key={`${command.pluginId}:${command.id}`} value={`${command.pluginId}:${command.id}`}>{command.title}</option>)}</select> : null}
        <button type="button" disabled={disabled || !selected || !review.candidate.revision} title={!selected ? t("channels.candidate.installPublisher") : selected.title} onClick={() => void decide("publish")}>{t("channels.candidate.publish")}</button>
        <button type="button" disabled={disabled} onClick={() => void decide("discard")}>{t("channels.candidate.discard")}</button>
        <button type="button" disabled={busy} onClick={() => void load()}>{t("channels.candidate.refresh")}</button>
      </div>
      {publishedURL ? <a href={publishedURL} target="_blank" rel="noreferrer">{t("channels.candidate.openPR")}</a> : null}
    </> : busy ? <p>{t("channels.candidate.loading")}</p> : <button type="button" onClick={() => void load()}>{t("channels.candidate.refresh")}</button>}
  </details>;
}
