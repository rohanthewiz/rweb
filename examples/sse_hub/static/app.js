// SSE client and message sending logic.
// EventSource connects to /events; hub.Broadcast sends JSON-wrapped data
// as "message" events, so we only need onmessage — no addEventListener required.

const messages = document.getElementById('messages');
const statusEl = document.getElementById('status');
const msgInput = document.getElementById('msgInput');
const sendBtn  = document.getElementById('sendBtn');

function addMsg(text, type) {
    const div = document.createElement('div');
    div.className = 'msg ' + type;
    const ts = new Date().toLocaleTimeString();
    div.innerHTML = text + '<span class="timestamp">' + ts + '</span>';
    messages.appendChild(div);
    messages.scrollTop = messages.scrollHeight;
}

const evtSource = new EventSource('/events');

evtSource.onopen = function() {
    statusEl.textContent = 'Connected';
    statusEl.className = 'status connected';
    addMsg('Connected to SSE hub', 'system');
};

evtSource.onmessage = function(e) {
    try {
        const payload = JSON.parse(e.data);
        addMsg('[' + payload.type + '] ' + payload.data, payload.type);
    } catch (err) {
        addMsg(e.data, 'system');
    }
};

evtSource.onerror = function() {
    statusEl.textContent = 'Disconnected — reconnecting...';
    statusEl.className = 'status disconnected';
};

// Send a custom message via POST — the server broadcasts it to all clients
sendBtn.onclick = function() {
    const msg = msgInput.value.trim();
    if (!msg) return;

    fetch('/send', {
        method: 'POST',
        headers: {'Content-Type': 'application/x-www-form-urlencoded'},
        body: 'message=' + encodeURIComponent(msg)
    });
    msgInput.value = '';
};

msgInput.onkeypress = function(e) {
    if (e.key === 'Enter') sendBtn.onclick();
};
