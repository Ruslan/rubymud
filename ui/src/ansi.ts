import { AnsiUp } from 'ansi_up';

export type AnsiTheme = 'classic' | 'high-contrast' | 'tango-dark' | 'dracula' | 'gruvbox-dark';

const boldNormalToBrightForeground: Record<string, string> = {
  'ansi-black-fg': 'ansi-bright-black-fg',
  'ansi-red-fg': 'ansi-bright-red-fg',
  'ansi-green-fg': 'ansi-bright-green-fg',
  'ansi-yellow-fg': 'ansi-bright-yellow-fg',
  'ansi-blue-fg': 'ansi-bright-blue-fg',
  'ansi-magenta-fg': 'ansi-bright-magenta-fg',
  'ansi-cyan-fg': 'ansi-bright-cyan-fg',
  'ansi-white-fg': 'ansi-bright-white-fg',
};

function isBoldElement(el: HTMLElement): boolean {
  return el.classList.contains('ansi-bold') || /font-weight\s*:\s*(bold|[6-9]00|700)/i.test(el.getAttribute('style') || '');
}

export function promoteBoldAnsiForeground(html: string): string {
  if (!html || !html.includes('ansi-')) {
    return html;
  }

  const template = document.createElement('template');
  template.innerHTML = html;

  template.content.querySelectorAll<HTMLElement>('[class*="ansi-"]').forEach((el) => {
    if (!isBoldElement(el)) {
      return;
    }

    for (const [normalClass, brightClass] of Object.entries(boldNormalToBrightForeground)) {
      if (el.classList.contains(normalClass)) {
        el.classList.replace(normalClass, brightClass);
        break;
      }
    }
  });

  return template.innerHTML;
}

export function normalizeAnsiTheme(theme: string | undefined | null): AnsiTheme {
  switch (theme) {
    case 'high-contrast':
    case 'tango-dark':
    case 'dracula':
    case 'gruvbox-dark':
      return theme;
    default:
      return 'classic';
  }
}

export function currentAnsiTheme(): AnsiTheme {
  return normalizeAnsiTheme(document.documentElement.dataset['ansiTheme']);
}

export function applyAnsiTheme(theme: string | undefined | null) {
  document.documentElement.dataset['ansiTheme'] = normalizeAnsiTheme(theme);
}

export function renderAnsiHtml(ansiUp: AnsiUp, text: string, theme: AnsiTheme = 'classic'): string {
  const html = ansiUp.ansi_to_html(text);
  if (theme !== 'high-contrast') {
    return html;
  }
  return promoteBoldAnsiForeground(html);
}
