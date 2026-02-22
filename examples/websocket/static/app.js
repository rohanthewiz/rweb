// WebSocket demo client — manages echo and chat connections

// --- Shared helper ---

function addMessage(container, message, type) {
    const msgDiv = document.createElement('div');
    msgDiv.className = 'message ' + type;
    const timestamp = new Date().toLocaleTimeString();
    msgDiv.innerHTML = message + ' <span class="timestamp">' + timestamp + '</span>';
    container.appendChild(msgDiv);
    container.scrollTop = container.scrollHeight;
}

// --- Echo WebSocket Client ---

let echoWs = null;
const echoMessages = document.getElementById('echoMessages');
const echoStatus = document.getElementById('echoStatus');
const echoInput = document.getElementById('echoInput');
const echoSendBtn = document.getElementById('echoSend');
const echoConnectBtn = document.getElementById('echoConnect');

function connectEcho() {
    if (echoWs) {
        echoWs.close();
        return;
    }

    const wsUrl = 'ws://' + window.location.host + '/ws/echo';
    echoWs = new WebSocket(wsUrl);

    echoWs.onopen = function() {
        addMessage(echoMessages, 'Connected to echo server', 'system');
        echoStatus.textContent = 'Connected';
        echoStatus.className = 'status connected';
        echoConnectBtn.textContent = 'Disconnect';
        echoSendBtn.disabled = false;
    };

    echoWs.onmessage = function(event) {
        addMessage(echoMessages, event.data, 'received');
    };

    echoWs.onclose = function() {
        addMessage(echoMessages, 'Disconnected from echo server', 'system');
        echoStatus.textContent = 'Disconnected';
        echoStatus.className = 'status disconnected';
        echoConnectBtn.textContent = 'Connect';
        echoSendBtn.disabled = true;
        echoWs = null;
    };

    echoWs.onerror = function(error) {
        addMessage(echoMessages, 'Error: ' + error, 'system');
    };
}

function sendEchoMessage() {
    if (echoWs && echoWs.readyState === WebSocket.OPEN) {
        const message = echoInput.value.trim();
        if (message) {
            echoWs.send(message);
            addMessage(echoMessages, 'You: ' + message, 'sent');
            echoInput.value = '';
        }
    }
}

echoConnectBtn.onclick = connectEcho;
echoSendBtn.onclick = sendEchoMessage;
echoSendBtn.disabled = true;
echoInput.onkeypress = function(e) {
    if (e.key === 'Enter') sendEchoMessage();
};

// --- Chat WebSocket Client ---

let chatWs = null;
const chatMessages = document.getElementById('chatMessages');
const chatStatus = document.getElementById('chatStatus');
const chatInput = document.getElementById('chatInput');
const chatSendBtn = document.getElementById('chatSend');
const chatConnectBtn = document.getElementById('chatConnect');

function connectChat() {
    if (chatWs) {
        chatWs.close();
        return;
    }

    const wsUrl = 'ws://' + window.location.host + '/ws/chat';
    chatWs = new WebSocket(wsUrl);

    chatWs.onopen = function() {
        addMessage(chatMessages, 'Connected to chat server', 'system');
        chatStatus.textContent = 'Connected';
        chatStatus.className = 'status connected';
        chatConnectBtn.textContent = 'Disconnect';
        chatSendBtn.disabled = false;
    };

    chatWs.onmessage = function(event) {
        try {
            const msg = JSON.parse(event.data);
            const content = msg.sender + ': ' + msg.content;
            addMessage(chatMessages, content, msg.type === 'system' ? 'system' : 'received');
        } catch (e) {
            addMessage(chatMessages, event.data, 'received');
        }
    };

    chatWs.onclose = function() {
        addMessage(chatMessages, 'Disconnected from chat server', 'system');
        chatStatus.textContent = 'Disconnected';
        chatStatus.className = 'status disconnected';
        chatConnectBtn.textContent = 'Connect';
        chatSendBtn.disabled = true;
        chatWs = null;
    };

    chatWs.onerror = function(error) {
        addMessage(chatMessages, 'Error: ' + error, 'system');
    };
}

function sendChatMessage() {
    if (chatWs && chatWs.readyState === WebSocket.OPEN) {
        const message = chatInput.value.trim();
        if (message) {
            const msg = {
                type: 'chat',
                content: message,
                sender: 'You',
                timestamp: new Date().toISOString()
            };
            chatWs.send(JSON.stringify(msg));
            addMessage(chatMessages, 'You: ' + message, 'sent');
            chatInput.value = '';
        }
    }
}

chatConnectBtn.onclick = connectChat;
chatSendBtn.onclick = sendChatMessage;
chatSendBtn.disabled = true;
chatInput.onkeypress = function(e) {
    if (e.key === 'Enter') sendChatMessage();
};
