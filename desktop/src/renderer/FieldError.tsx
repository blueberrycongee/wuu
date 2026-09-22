import { CircleAlert } from "./WuuIcons";
import type { ReactElement, ReactNode } from "react";

export interface FieldErrorProps {
  children?: ReactNode;
  className?: string;
  id?: string;
}

export function FieldError({ children, className, id }: FieldErrorProps): ReactElement | null {
  if (!children) return null;

  return (
    <p className={`field-error${className ? ` ${className}` : ""}`} id={id} role="alert">
      <CircleAlert aria-hidden="true" className="icon-sm" />
      <span>{children}</span>
    </p>
  );
}
