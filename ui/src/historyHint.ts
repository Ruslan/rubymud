export type MatchSegments = {
  before: string;
  match: string;
  after: string;
};

export function findCaseInsensitiveMatchSegments(value: string, query: string): MatchSegments | null {
  if (!query) {
    return null;
  }

  const haystack = value.toLocaleLowerCase();
  const needle = query.toLocaleLowerCase();
  const index = haystack.indexOf(needle);
  if (index === -1) {
    return null;
  }

  return {
    before: value.slice(0, index),
    match: value.slice(index, index + query.length),
    after: value.slice(index + query.length),
  };
}

function appendHighlightedMatch(target: HTMLElement, query: string, match: string) {
  const segments = findCaseInsensitiveMatchSegments(match, query);
  if (!segments) {
    target.appendChild(document.createTextNode(match));
    return;
  }

  target.appendChild(document.createTextNode(segments.before));
  const highlighted = document.createElement('span');
  highlighted.className = 'reverse-search-match';
  highlighted.textContent = segments.match;
  target.appendChild(highlighted);
  target.appendChild(document.createTextNode(segments.after));
}

export function appendReverseSearchHintContent(target: HTMLElement, query: string, match: string | null) {
  target.replaceChildren();

  const prefix = document.createElement('span');
  prefix.className = 'reverse-search-prefix';
  prefix.textContent = 'reverse-i-search: ';
  target.appendChild(prefix);

  const queryEl = document.createElement('span');
  queryEl.className = 'reverse-search-query';
  queryEl.textContent = query || '…';
  target.appendChild(queryEl);

  target.appendChild(document.createTextNode(' → '));

  if (!match) {
    const empty = document.createElement('span');
    empty.className = 'reverse-search-empty';
    empty.textContent = 'No history match';
    target.appendChild(empty);
    return;
  }

  appendHighlightedMatch(target, query, match);
}
