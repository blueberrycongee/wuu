import { ArrowRight, Circle, CircleDot, Pencil, Square, SquareCheck, X } from "lucide-react";
import { useEffect, useMemo, useState } from "react";
import type {
  UserQuestionAnswer,
  UserQuestion,
  UserQuestionOption,
  UserQuestionRequest,
} from "../shared/protocol";
import { useI18n } from "./i18n";

const APPROVAL_ALLOW_ONCE = "Allow once";
const APPROVAL_ALLOW_SESSION = "Allow for this session";
const APPROVAL_DENY = "Deny";

function approvalKind(question: UserQuestion): "command" | "file" | "permissions" | undefined {
  if (question.id === "approval.command_execution") return "command";
  if (question.id === "approval.file_change") return "file";
  if (question.id === "approval.permissions") return "permissions";
  return undefined;
}

function approvalEngineName(pluginID: string): string | undefined {
  if (!pluginID.startsWith("agent-engine-")) return undefined;
  const engine = pluginID.slice("agent-engine-".length).trim();
  if (!engine) return undefined;
  return engine.charAt(0).toUpperCase() + engine.slice(1);
}

function approvalQuestionText(
  request: UserQuestionRequest,
  question: UserQuestion,
  t: ReturnType<typeof useI18n>["t"],
): { header: string; question: string } | undefined {
  const engine = approvalEngineName(request.plugin_id);
  const kind = approvalKind(question);
  if (!engine || !kind) return undefined;
  const values = { engine };
  const questionKeys = {
    command: "userQuestion.approvalCommandQuestion",
    file: "userQuestion.approvalFileQuestion",
    permissions: "userQuestion.approvalPermissionsQuestion",
  } as const;
  return {
    header: t("userQuestion.approvalHeader", values),
    question: t(questionKeys[kind], values),
  };
}

function approvalOptionText(
  request: UserQuestionRequest,
  question: UserQuestion,
  option: UserQuestionOption,
  t: ReturnType<typeof useI18n>["t"],
): UserQuestionOption {
  if (!approvalEngineName(request.plugin_id) || !approvalKind(question)) return option;
  const labels: Record<string, { label: Parameters<typeof t>[0]; description: Parameters<typeof t>[0] }> = {
    [APPROVAL_ALLOW_ONCE]: {
      label: "userQuestion.approvalAllowOnce",
      description: "userQuestion.approvalAllowOnceDescription",
    },
    [APPROVAL_ALLOW_SESSION]: {
      label: "userQuestion.approvalAllowSession",
      description: "userQuestion.approvalAllowSessionDescription",
    },
    [APPROVAL_DENY]: {
      label: "userQuestion.approvalDeny",
      description: "userQuestion.approvalDenyDescription",
    },
  };
  const keys = labels[option.label];
  if (!keys) return option;
  return { label: t(keys.label), description: t(keys.description) };
}

type Props = {
  request: UserQuestionRequest;
  onAnswer: (answer: UserQuestionAnswer) => Promise<void>;
  onCancel: () => Promise<void>;
  onHold?: () => Promise<void>;
  onCustom?: (text?: string) => Promise<void> | void;
};

export function UserQuestionCard({ request, onAnswer, onCancel, onHold, onCustom }: Props): JSX.Element {
  const { t } = useI18n();
  const [selected, setSelected] = useState<Record<string, string[]>>({});
  const [custom, setCustom] = useState<Record<string, string>>({});
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState("");
  const [remainingSeconds, setRemainingSeconds] = useState<number | null>(null);
  const [drafting, setDrafting] = useState(false);
  const offer = request.mode === "offer";
  const offerQuestion = offer ? request.questions[0] : undefined;
  const offerLead = offerQuestion
    ? approvalQuestionText(request, offerQuestion, t)?.header
      || offerQuestion.header
      || offerQuestion.question
    : "";
  const complete = useMemo(
    () => request.questions.every((question) =>
      (selected[question.id]?.length ?? 0) > 0 ||
      (question.allow_custom && (custom[question.id]?.trim().length ?? 0) > 0)),
    [custom, request.questions, selected],
  );

  useEffect(() => {
    if (!offer || !request.expires_at || submitting || drafting) {
      if (drafting || submitting || !offer) setRemainingSeconds(null);
      return;
    }
    const expiresAt = Date.parse(request.expires_at);
    if (!Number.isFinite(expiresAt)) {
      setRemainingSeconds(null);
      return;
    }
    let timeout = 0;
    const tick = (): void => {
      const remainingMs = expiresAt - Date.now();
      if (remainingMs <= 0) {
        setRemainingSeconds(0);
        void onCancel();
        return;
      }
      setRemainingSeconds(Math.max(1, Math.ceil(remainingMs / 1000)));
      timeout = window.setTimeout(tick, Math.min(1000, remainingMs));
    };
    tick();
    return () => window.clearTimeout(timeout);
  }, [drafting, offer, onCancel, request.expires_at, request.request_id, submitting]);

  function keepOffer(): void {
    if (!offer || submitting) return;
    void onHold?.();
  }

  function toggle(questionID: string, label: string, multiSelect: boolean): Record<string, string[]> {
    const values = selected[questionID] ?? [];
    const next = multiSelect
      ? values.includes(label)
        ? values.filter((value) => value !== label)
        : [...values, label]
      : [label];
    const nextSelected = { ...selected, [questionID]: next };
    setSelected(nextSelected);
    return nextSelected;
  }

  function answersFrom(nextSelected: Record<string, string[]>): UserQuestionAnswer {
    return {
      answers: request.questions.map((question) => ({
        id: question.id,
        selected: nextSelected[question.id] ?? [],
        ...(custom[question.id]?.trim()
          ? { custom: custom[question.id].trim() }
          : {}),
      })),
    };
  }

  async function submit(nextSelected = selected): Promise<void> {
    if (submitting) return;
    const ready = request.questions.every((question) =>
      (nextSelected[question.id]?.length ?? 0) > 0 ||
      (question.allow_custom && (custom[question.id]?.trim().length ?? 0) > 0));
    if (!ready) return;
    setSubmitting(true);
    setError("");
    try {
      await onAnswer(answersFrom(nextSelected));
    } catch (cause) {
      setError(
        cause instanceof Error ? cause.message : t("userQuestion.sendFailed"),
      );
      setSubmitting(false);
    }
  }

  async function chooseOption(questionID: string, label: string, multiSelect: boolean): Promise<void> {
    const nextSelected = toggle(questionID, label, multiSelect);
    if (offer && !multiSelect) {
      await submit(nextSelected);
    }
  }

  async function submitOfferCustom(): Promise<void> {
    if (!offer || !offerQuestion) return;
    const value = (custom[offerQuestion.id] ?? "").trim();
    if (!value) return;
    setSubmitting(true);
    setError("");
    try {
      keepOffer();
      await onCustom?.(value);
    } catch (cause) {
      setError(
        cause instanceof Error ? cause.message : t("userQuestion.sendFailed"),
      );
      setSubmitting(false);
    }
  }

  function startOfferCustom(): void {
    if (!offer || submitting) return;
    keepOffer();
    setDrafting(true);
    void onCustom?.();
  }

  function cancelQuestion(): void {
    setSubmitting(true);
    setError("");
    void onCancel().catch((cause) => {
      setError(
        cause instanceof Error ? cause.message : t("userQuestion.cancelFailed"),
      );
      setSubmitting(false);
    });
  }

  if (offer && offerQuestion) {
    const approvalText = approvalQuestionText(request, offerQuestion, t);
    const header = approvalText?.header || offerQuestion.header;
    const prompt = approvalText?.question || offerQuestion.question;
    const countdown = remainingSeconds != null && remainingSeconds > 0
      ? t("userQuestion.countdown", { seconds: remainingSeconds })
      : null;
    return (
      <section
        aria-label={t("userQuestion.offerAriaLabel")}
        className="user-question-card user-question-card-offer"
      >
        <div className="user-question-offer-title">
          <p className="user-question-prompt">{header || prompt}</p>
          <button
            aria-label={t("userQuestion.cancel")}
            className="user-question-close"
            disabled={submitting}
            onClick={cancelQuestion}
            type="button"
          >
            <X />
          </button>
        </div>
        {header ? <p className="user-question-body">{prompt}</p> : null}
        {offerQuestion.detail ? <p className="user-question-detail">{offerQuestion.detail}</p> : null}
        {offerQuestion.options?.length ? (
          <div
            className="user-question-options user-question-options-offer"
            role={offerQuestion.multi_select ? "group" : "radiogroup"}
            aria-label={offerLead}
          >
            {offerQuestion.options.map((option, index) => {
              const displayOption = approvalOptionText(request, offerQuestion, option, t);
              const active = selected[offerQuestion.id]?.includes(option.label) ?? false;
              return (
                <button
                  aria-checked={active}
                  className="user-question-option user-question-option-offer"
                  data-active={active || undefined}
                  disabled={submitting}
                  key={option.label}
                  onClick={() => void chooseOption(offerQuestion.id, option.label, Boolean(offerQuestion.multi_select))}
                  role={offerQuestion.multi_select ? "checkbox" : "radio"}
                  type="button"
                >
                  <span className="user-question-option-index" aria-hidden="true">{index + 1}</span>
                  <span className="user-question-option-content">
                    <span className="user-question-option-label">{displayOption.label}</span>
                    {displayOption.description ? (
                      <span className="user-question-option-description">
                        {displayOption.description}
                      </span>
                    ) : null}
                  </span>
                  <ArrowRight className="user-question-option-go" aria-hidden="true" />
                </button>
              );
            })}
          </div>
        ) : null}
        {error ? <span className="user-question-error" role="alert">{error}</span> : null}
        <div className={`user-question-offer-footer${drafting ? " is-drafting" : ""}`}>
          {drafting ? (
            <>
              <span className="user-question-offer-draft-icon" aria-hidden="true">
                <Pencil />
              </span>
              <input
                aria-label={t("userQuestion.customAriaLabel", { question: offerLead })}
                autoFocus
                className="user-question-offer-input"
                disabled={submitting}
                onChange={(event) => {
                  const value = event.currentTarget.value;
                  setCustom((current) => ({ ...current, [offerQuestion.id]: value }));
                }}
                onKeyDown={(event) => {
                  if (event.key === "Enter") {
                    event.preventDefault();
                    void submitOfferCustom();
                  }
                }}
                placeholder={t("userQuestion.customPlaceholder")}
                value={custom[offerQuestion.id] ?? ""}
              />
            </>
          ) : (
            <button
              className="user-question-offer-custom"
              disabled={submitting}
              onClick={startOfferCustom}
              type="button"
            >
              <Pencil aria-hidden="true" />
              <span>{t("userQuestion.custom")}</span>
            </button>
          )}
          {drafting ? (
            <button
              className="user-question-submit"
              disabled={submitting || !(custom[offerQuestion.id]?.trim())}
              onClick={() => void submitOfferCustom()}
              type="button"
            >
              {submitting ? t("userQuestion.sending") : t("userQuestion.submit")}
            </button>
          ) : (
            <button
              className="user-question-skip"
              disabled={submitting}
              onClick={cancelQuestion}
              type="button"
            >
              {countdown ?? t("userQuestion.skip")}
            </button>
          )}
        </div>
      </section>
    );
  }

  return (
    <section
      aria-label={t("userQuestion.kicker")}
      className="user-question-card"
    >
      <p className="user-question-kicker">{t("userQuestion.kicker")}</p>
      {request.questions.map((question) => {
        const approvalText = approvalQuestionText(request, question, t);
        const header = approvalText?.header || question.header;
        const prompt = approvalText?.question || question.question;
        const lead = header || prompt;
        return (
          <div className="user-question-field" key={question.id}>
            <p className="user-question-prompt">{lead}</p>
            {header ? (
              <p className="user-question-body">{prompt}</p>
            ) : null}
            {question.detail ? (
              <p className="user-question-detail">{question.detail}</p>
            ) : null}
            {question.options?.length ? (
              <div
                className="user-question-options"
                role={question.multi_select ? "group" : "radiogroup"}
                aria-label={lead}
              >
                {question.options.map((option) => {
                  const displayOption = approvalOptionText(request, question, option, t);
                  const active = selected[question.id]?.includes(option.label) ?? false;
                  return (
                    <button
                      aria-checked={active}
                      className="user-question-option"
                      data-active={active || undefined}
                      data-multi={question.multi_select ? "true" : "false"}
                      key={option.label}
                      onClick={() => void chooseOption(question.id, option.label, Boolean(question.multi_select))}
                      role={question.multi_select ? "checkbox" : "radio"}
                      type="button"
                    >
                      <span className="user-question-option-indicator" aria-hidden="true">
                        {question.multi_select ? (
                          active ? <SquareCheck /> : <Square />
                        ) : active ? (
                          <CircleDot />
                        ) : (
                          <Circle />
                        )}
                      </span>
                      <span className="user-question-option-content">
                        <span className="user-question-option-label">{displayOption.label}</span>
                        {displayOption.description ? (
                          <span className="user-question-option-description">
                            {displayOption.description}
                          </span>
                        ) : null}
                      </span>
                    </button>
                  );
                })}
              </div>
            ) : null}
            {question.allow_custom && !offer ? (
              <input
                aria-label={t("userQuestion.customAriaLabel", { question: lead })}
                className="user-question-custom"
                onChange={(event) => {
                  const value = event.currentTarget.value;
                  setCustom((current) => ({ ...current, [question.id]: value }));
                }}
                placeholder={t("userQuestion.customPlaceholder")}
                value={custom[question.id] ?? ""}
              />
            ) : null}
          </div>
        );
      })}
      <div className="user-question-actions">
        {error ? <span className="user-question-error" role="alert">{error}</span> : null}
        <button
          className="user-question-cancel"
          disabled={submitting}
          onClick={cancelQuestion}
          type="button"
        >
          {t("userQuestion.cancel")}
        </button>
        <button
          className="user-question-submit"
          disabled={!complete || submitting}
          onClick={() => void submit()}
          type="button"
        >
          {submitting ? t("userQuestion.sending") : t("userQuestion.continue")}
        </button>
      </div>
    </section>
  );
}
