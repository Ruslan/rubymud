# Colors & Overlays Guide

Use this when you need to understand how text is styled and enriched before being displayed in the UI.

## ANSI Colors
MUDs send text using standard ANSI escape codes. The backend normalizes these codes and stores the "plain" version of the text in `log_entries.plain_text` while keeping the original formatting info in `log_overlays`.

## The Overlay Model (XML-like)
Instead of storing pre-rendered HTML, `rubymud` uses a structured overlay system. Think of it as a set of XML-like tags layered on top of a stable plain-text string.

### `log_overlays` Table
Each overlay record points to a `log_entry` and defines:
- **`start_offset` / `end_offset`**: Range in the `plain_text` where the overlay applies.
- **`overlay_type`**: `style_span`, `button`, `link`, `match`, etc.
- **`payload_json`**: Details for the overlay (e.g., color values, command to execute for a button).

### Style Spans
Styles are resolved into `fg` (foreground), `bg` (background), and attributes like `bold`, `italic`, `underline`. 

## Benefits
1. **Stable Search**: You can search `plain_text` without worrying about escape codes or HTML tags.
2. **Dynamic Rendering**: The UI can change how it renders a "button" or a "highlight" without needing to migrate historical log data.
3. **Additive**: Multiple plugins can add overlays to the same line of text without interfering with the source.
