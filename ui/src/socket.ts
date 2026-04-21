export function createSocket() {
  const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:';
  return new WebSocket(`${protocol}//${window.location.host}/ws`);
}

export function sendSocketCommand(socket: WebSocket, value: string, source: string) {
  socket.send(JSON.stringify({ method: 'send', value, source }));
}

export function requestSocketVariables(socket: WebSocket) {
  socket.send(JSON.stringify({ method: 'variables' }));
}
