import * as React from 'react';
import type { PluginUIKit, PluginUIContainerProps } from '../dist/index.js';

export function createPreviewUIKit(): PluginUIKit {
  const Container = ({ children, className, ...props }: PluginUIContainerProps) => <div className={className} {...props}>{children}</div>;
  return {
    WebPage: () => { throw new Error('WebPage requires the Wuu desktop host.'); },
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
