export function createSocket(sessionID?: number) {
  const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:';
  // @ts-ignore - Injected by Go server
  const token = window.API_TOKEN || '';
  let url = `${protocol}//${window.location.host}/ws?token=${token}`;
  if (sessionID) {
    url += `&session_id=${sessionID}`;
  }
  return new WebSocket(url);
}

export function sendSocketCommand(socket: WebSocket, value: string, source: string) {
  socket.send(JSON.stringify({ method: 'send', value, source }));
}

export function requestSocketVariables(socket: WebSocket) {
  socket.send(JSON.stringify({ method: 'variables' }));
}
