import type {
  ButtonHTMLAttributes,
  CSSProperties,
  HTMLAttributes,
  InputHTMLAttributes,
  TextareaHTMLAttributes,
} from 'react';

function cx(...parts: Array<string | false | null | undefined>) {
  return parts.filter(Boolean).join(' ');
}

type ButtonVariant = 'default' | 'primary' | 'ghost' | 'danger' | 'tab';

export function UIPanel({
  className,
  raised,
  ...props
}: HTMLAttributes<HTMLDivElement> & { raised?: boolean }) {
  return <div className={cx('ui-panel', raised && 'ui-panel--raised', className)} {...props} />;
}

export function UIButton({
  className,
  variant = 'default',
  active,
  block,
  style,
  ...props
}: ButtonHTMLAttributes<HTMLButtonElement> & {
  variant?: ButtonVariant;
  active?: boolean;
  block?: boolean;
}) {
  return (
    <button
      className={cx('ui-button', `ui-button--${variant}`, active && 'is-active', block && 'is-block', className)}
      style={style}
      {...props}
    />
  );
}

export function UIInput({ className, ...props }: InputHTMLAttributes<HTMLInputElement>) {
  return <input className={cx('ui-input', className)} {...props} />;
}

export function UITextArea({ className, ...props }: TextareaHTMLAttributes<HTMLTextAreaElement>) {
  return <textarea className={cx('ui-input', 'ui-textarea', className)} {...props} />;
}

export function UILabel({ className, ...props }: HTMLAttributes<HTMLLabelElement>) {
  return <label className={cx('ui-label', className)} {...props} />;
}

export function UIKicker({ className, ...props }: HTMLAttributes<HTMLDivElement>) {
  return <div className={cx('ui-kicker', className)} {...props} />;
}

export function UIModal({
  children,
  onClose,
  width,
  className,
}: {
  children: React.ReactNode;
  onClose: () => void;
  width?: CSSProperties['width'];
  className?: string;
}) {
  return (
    <div className="ui-modal-wrap">
      <div className="ui-overlay" onClick={onClose} />
      <div className={cx('ui-modal', 'panel-reveal', className)} style={width ? { width } : undefined}>
        {children}
      </div>
    </div>
  );
}