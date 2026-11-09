import "./SidebarActivityIndicator.css";

export function SidebarActivityIndicator({ className }: { className: string }): JSX.Element {
  return (
    <span className={`sidebar-activity ${className}`} aria-hidden="true">
      <svg viewBox="0 0 12 12" focusable="false">
        <circle cx="2" cy="2" r="1.1" />
        <circle cx="6" cy="2" r="1.1" />
        <circle cx="10" cy="2" r="1.1" />
        <circle cx="2" cy="6" r="1.1" />
        <circle cx="6" cy="6" r="1.1" />
        <circle cx="10" cy="6" r="1.1" />
        <circle cx="2" cy="10" r="1.1" />
        <circle cx="6" cy="10" r="1.1" />
        <circle cx="10" cy="10" r="1.1" />
      </svg>
    </span>
  );
}
