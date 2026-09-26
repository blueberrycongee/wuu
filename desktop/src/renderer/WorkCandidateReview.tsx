import { useCallback, useState, useSyncExternalStore } from "react";
import type { ChannelWork, ChannelWorkArtifact, ChannelWorkCandidateResult } from "../shared/protocol";
import { desktopPluginHost } from "./plugins/DesktopPluginRuntime";
import { RichContent } from "./RichContent";
import { SelectMenu } from "./SelectMenu";
import { ArrowUpRight, ChevronDown, ChevronRight, FileDiff } from "./WuuIcons";
import { useI18n } from "./i18n";
import { showErrorToast } from "./Toast";

export function WorkCandidateReview({ work, artifact, onOpenSession }: {
  work: ChannelWork; artifact: ChannelWorkArtifact; onOpenSession?: (id: string) => void;
}): JSX.Element {
  const { t } = useI18n();
  const [open, setOpen] = useState(false);
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
  const report = review?.candidate.report;
  return <section className="work-candidate-review" aria-busy={busy || undefined}>
    <button type="button" className="work-candidate-toggle" aria-expanded={open} onClick={() => {
      setOpen(!open);
      if (!open && !review) void load();
    }}>
      <FileDiff aria-hidden="true" />
      <span>{t("channels.candidate.title")}</span>
      {disposition ? <span className="work-candidate-disposition">{t(`channels.candidate.${disposition}`)}</span> : null}
      {open ? <ChevronDown aria-hidden="true" /> : <ChevronRight aria-hidden="true" />}
    </button>
    {open && review && report ? <div className="work-candidate-body">
      {review.stale ? <p className="work-candidate-notice" role="status">{t("channels.candidate.stale")}</p> : null}
      <RichContent text={report.result} />
      <dl className="work-candidate-facts">
        <div>
          <dt>{t("channels.candidate.verification")}</dt>
          <dd>{work.candidate_artifact_ref === artifact.id && work.verification
            ? `${t(`channels.verification.${work.verification.decision}`)}${work.verification.report ? ` — ${work.verification.report}` : ""}`
            : t("channels.candidate.unverified")}</dd>
        </div>
        <div>
          <dt>{t("channels.candidate.checks")}</dt>
          <dd>{report.evidence_refs.length || report.unresolved_items.length ? <ul>
            {report.evidence_refs.map((ref, index) => <li key={`evidence-${index}`}>{ref}</li>)}
            {report.unresolved_items.map((item, index) => <li key={`unresolved-${index}`}>{item}</li>)}
          </ul> : t("channels.candidate.noChecks")}</dd>
        </div>
        {report.implicit_choices.length ? <div>
          <dt>{t("channels.candidate.choices")}</dt>
          <dd><ul>{report.implicit_choices.map((choice, index) => <li key={index}>{choice}</li>)}</ul></dd>
        </div> : null}
      </dl>
      <pre className="work-candidate-diff" tabIndex={0} aria-label={t("channels.candidate.diff")}><code>{review.candidate.diff || t("channels.candidate.noDiff")}</code></pre>
      <div className="channel-card-actions">
        {commands.length > 1 ? <SelectMenu className="work-candidate-publisher" ariaLabel={t("channels.candidate.publisher")} value={selected ? `${selected.pluginId}:${selected.id}` : ""}
          onChange={setPublisher} options={commands.map(command => ({ value: `${command.pluginId}:${command.id}`, label: command.title }))} flip /> : null}
        <button type="button" className="secondary" disabled={disabled} onClick={() => void decide("discard")}>{t("channels.candidate.discard")}</button>
        <button type="button" disabled={disabled || !selected || !review.candidate.revision} title={!selected ? t("channels.candidate.installPublisher") : selected.title} onClick={() => void decide("publish")}>{t("channels.candidate.publish")}</button>
        <button type="button" className="primary" disabled={disabled || !review.candidate.revision} onClick={() => void decide("apply")}>{t("channels.candidate.apply")}</button>
      </div>
      <div className="work-candidate-links">
        {onOpenSession ? <button type="button" className="channel-card-link" onClick={() => onOpenSession(review.candidate.session_id)}>{t("channels.candidate.session")}<ArrowUpRight aria-hidden="true" /></button> : null}
        {publishedURL ? <a className="channel-card-link" href={publishedURL} target="_blank" rel="noreferrer">{t("channels.candidate.openPR")}<ArrowUpRight aria-hidden="true" /></a> : null}
        <button type="button" className="channel-card-link" disabled={busy} onClick={() => void load()}>{t("channels.candidate.refresh")}</button>
      </div>
    </div> : open ? busy ? <p className="work-candidate-notice">{t("channels.candidate.loading")}</p>
      : <button type="button" className="channel-card-link" onClick={() => void load()}>{t("channels.candidate.refresh")}</button> : null}
  </section>;
}
