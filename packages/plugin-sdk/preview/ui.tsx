import * as React from 'react';
import type { PluginUIKit, PluginUIContainerProps } from '../dist/index.js';

export function createPreviewUIKit(): PluginUIKit {
  const Container = ({ children, className, ...props }: PluginUIContainerProps) => <div className={className} {...props}>{children}</div>;
  return {
    WebPage: () => { throw new Error('WebPage requires the Wuu desktop host.'); },
    Avatar: ({ size = 32, status, className }) => <span aria-hidden="true" className={className} data-status={status} style={{ display: 'inline-block', width: size, height: size, borderRadius: '50%', background: 'currentColor', opacity: .3 }} />,
    ConversationView: ({ title, subtitle, avatar, status, actions, searchLabel, searchContent, showHistorySearch = true, loading, children, composer }) => <section aria-busy={loading}>
      <header>{avatar}<h1>{title}</h1>{subtitle && <p>{subtitle}</p>}{status}{actions}</header>
      {showHistorySearch && <details><summary>{searchLabel}</summary>{searchContent}</details>}
      <div>{children}</div><footer>{composer}</footer>
    </section>,
    ConversationMessage: ({ role, content, className, replyPreview, onReply, replyLabel, attachments, timestamp, process, footer }) => <article className={className} data-role={role}>
      {replyPreview}<div style={{ whiteSpace: 'pre-wrap' }}>{content}</div>{attachments}
      {process && <details><summary>{process.label}</summary>{process.content}{process.details}</details>}
      {timestamp && <time>{timestamp}</time>}{onReply && <button onClick={onReply}>{replyLabel ?? 'Reply'}</button>}{footer}
    </article>,
    ConversationAttachment: ({ name, mediaType, size, url, loading, error, loadLabel, downloadLabel, onLoad, onOpen }) => <section>
      <span>{name} · {mediaType} · {size} B</span>{error && <p role="alert">{error}</p>}
      {url ? <a href={url} download={name}>{downloadLabel}</a> : <button disabled={loading} onClick={() => { void onLoad(); }}>{loadLabel}</button>}
      {onOpen && <button onClick={onOpen}>Open</button>}
    </section>,
    ConversationComposer: ({ prompt, placeholder, running, disabled, sendDisabled, attachments, replyContext, onChangePrompt, onAddAttachments, onRemoveAttachment, onSend, onStop }) => <form onSubmit={event => { event.preventDefault(); if (!disabled && !sendDisabled) onSend(prompt); }}>
      {replyContext && <div>{replyContext.label}: {replyContext.preview}<button type="button" onClick={replyContext.onCancel}>{replyContext.cancelLabel}</button></div>}
      <textarea aria-label={placeholder ?? 'Message'} placeholder={placeholder} value={prompt} disabled={disabled} onChange={event => onChangePrompt(event.currentTarget.value)} />
      <input type="file" multiple aria-label="Add attachments" disabled={disabled} onChange={event => onAddAttachments(Array.from(event.currentTarget.files ?? []))} />
      {attachments.map(file => <button type="button" key={file.id} onClick={() => onRemoveAttachment(file.id)}>{file.filename} ×</button>)}
      {running ? <button type="button" disabled={disabled} onClick={onStop}>Stop</button> : <button type="submit" disabled={disabled || sendDisabled}>Send</button>}
    </form>,
    ComposerDrawer: ({ expanded, onExpandedChange, toggleLabel, children, summary }) => <section><button onClick={() => onExpandedChange(!expanded)} aria-expanded={expanded}>{toggleLabel}</button>{summary}{expanded && children}</section>,
    Page: ({ children, density: _density, ...props }) => <main {...props}>{children}</main>,
    Panel: Container, Card: Container, Row: ({ children, ...props }) => <div {...props} style={{ display: 'flex', gap: '.7rem', alignItems: 'center', flexWrap: 'wrap' }}>{children}</div>,
    Stack: ({ children, gap, ...props }) => <div {...props} style={{ display: 'grid', gap: gap === 'large' ? '1.4rem' : gap === 'small' ? '.4rem' : '.8rem' }}>{children}</div>,
    Section: ({ title, description, children, ...props }) => <section {...props}>{title && <h2>{title}</h2>}{description && <p>{description}</p>}{children}</section>,
    Button: ({ children, variant = 'secondary', ...props }) => <button data-variant={variant} {...props}>{children}</button>,
    ToolbarToggle: ({ pressed, ...props }) => <button aria-pressed={pressed} {...props} />,
    TextInput: ({ label, description, ...props }) => <label>{label}<input {...props} />{description && <small>{description}</small>}</label>,
    TextArea: ({ label, description, ...props }) => <label>{label}<textarea {...props} />{description && <small>{description}</small>}</label>,
    Select: ({ label, description, children, ...props }) => <label>{label}<select {...props}>{children}</select>{description && <small>{description}</small>}</label>,
    Checkbox: ({ label, description, ...props }) => <label><input type="checkbox" {...props} />{label}{description && <small>{description}</small>}</label>,
    EmptyState: ({ title, description, actions }) => <section role="status"><h2>{title}</h2><p>{description}</p>{actions}</section>,
    LoadingState: ({ title = 'Loading', description }) => <section role="status">{title}{description}</section>,
    ErrorState: ({ title, description, actions }) => <section role="alert"><h2>{title}</h2><p>{description}</p>{actions}</section>,
    LiveDuration: ({ elapsedMs }) => <time>{Math.round(elapsedMs / 1000)}s</time>,
  };
}
