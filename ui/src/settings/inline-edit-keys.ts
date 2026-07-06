// Shared keyboard behavior for inline row-edit panels across Settings sections.
//
// Recommended UX (docs/dev/planned/settings-inline-editing.md):
//   - Esc cancels the inline edit.
//   - Ctrl+Enter / Cmd+Enter saves (works inside multiline textareas too).
//   - Plain Enter in a single-line <input> also saves, for quick edits.
//
// Usage: <div class="inline-edit-panel" use:inlineEditKeys={{ save, cancel }}>

interface InlineEditKeysParams {
  save: () => void | Promise<void>;
  cancel: () => void;
}

export function inlineEditKeys(node: HTMLElement, params: InlineEditKeysParams) {
  let current = params;

  function onKeydown(event: KeyboardEvent) {
    if (event.key === 'Escape') {
      event.preventDefault();
      current.cancel();
      return;
    }
    if (event.key === 'Enter') {
      const target = event.target as HTMLElement | null;
      const isTextarea = target?.tagName === 'TEXTAREA';
      const isSelect = target?.tagName === 'SELECT';
      // Ctrl/Cmd+Enter saves from anywhere (including textareas); plain Enter
      // saves only from single-line inputs, so it never swallows a newline or
      // hijacks native select/textarea behavior.
      const saveViaModifier = event.ctrlKey || event.metaKey;
      const saveViaPlainEnter = !isTextarea && !isSelect && target?.tagName === 'INPUT';
      if (saveViaModifier || saveViaPlainEnter) {
        event.preventDefault();
        current.save();
      }
    }
  }

  node.addEventListener('keydown', onKeydown);

  return {
    update(next: InlineEditKeysParams) {
      current = next;
    },
    destroy() {
      node.removeEventListener('keydown', onKeydown);
    },
  };
}
