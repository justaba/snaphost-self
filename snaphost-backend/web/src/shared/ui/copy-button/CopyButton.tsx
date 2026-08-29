import styles from './CopyButton.module.css';

import { useState } from 'react';
import { Check, Copy } from 'lucide-react';

import { Button, type ButtonSize, type ButtonVariant } from '@/shared/ui/button';
import { useToast } from '@/shared/ui/toast';

export interface CopyButtonProps {
  value: string;
  label?: string;
  copiedLabel?: string;
  ariaLabel?: string;
  variant?: ButtonVariant;
  size?: ButtonSize;
}

/** Copies a value and says so for two seconds. Falls back to a toast when the
 *  clipboard is unavailable (http origins, denied permission), so the user is
 *  told to select the text rather than left wondering. */
function CopyButton({
  value,
  label = 'Копировать',
  copiedLabel = 'Скопировано',
  ariaLabel,
  variant = 'secondary',
  size = 'sm',
}: CopyButtonProps) {
  const [copied, setCopied] = useState(false);
  const { toast } = useToast();

  const copy = async () => {
    try {
      await navigator.clipboard.writeText(value);
      setCopied(true);
      window.setTimeout(() => setCopied(false), 2000);
    } catch {
      toast('Не удалось скопировать — выделите значение вручную.', 'error');
    }
  };

  return (
    <Button
      variant={variant}
      size={size}
      className={styles.root}
      onClick={copy}
      aria-label={ariaLabel ?? label}
      iconLeft={copied ? <Check size={15} /> : <Copy size={15} />}
    >
      {copied ? copiedLabel : label}
    </Button>
  );
}

export default CopyButton;
