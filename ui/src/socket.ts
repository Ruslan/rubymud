export function browserTimezone(): string {
  try {
    return Intl.DateTimeFormat().resolvedOptions().timeZone || '';
  } catch {
    return '';
  }
}

export function createSocket(sessionID?: number) {
  const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:';
  // @ts-ignore - Injected by Go server
  const token = window.API_TOKEN || '';
  let url = `${protocol}//${window.location.host}/ws?token=${token}`;
  if (sessionID) {
    url += `&session_id=${sessionID}`;
  }
  const tz = browserTimezone();
  if (tz) {
    url += `&tz=${encodeURIComponent(tz)}`;
  }
  return new WebSocket(url);
}

export function sendSocketCommand(socket: WebSocket, value: string, source: string, clientCommandID?: string) {
  socket.send(JSON.stringify({ method: 'send', value, source, client_command_id: clientCommandID }));
}

export function requestSocketVariables(socket: WebSocket) {
  socket.send(JSON.stringify({ method: 'variables' }));
}
