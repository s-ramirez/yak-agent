// ── Constants ────────────────────────────────────────────────────────

const CANVAS_W = 800;
const CANVAS_H = 600;
const LERP_SPEED = 6;
const AGENT_RADIUS = 14;
const MIN_TRAVEL_TIME = 0.25;   // seconds: minimum time to reach station
const MIN_DWELL_TIME = 0.35;    // seconds: minimum time working at station
const STATION_W = 80;
const STATION_H = 60;

const AGENT_COLORS = [
    '#e63946', '#457b9d', '#2a9d8f', '#e9c46a',
    '#f4a261', '#264653', '#6a4c93', '#1982c4',
];

// Tool station layout positions.
const STATION_DEFS = {
    'bash':           { x: 120, y: 80,  color: '#4a4e69', icon: 'terminal' },
    'read':           { x: 360, y: 80,  color: '#457b9d', icon: 'book' },
    'write':          { x: 600, y: 80,  color: '#2a9d8f', icon: 'pencil' },
    'edit':           { x: 120, y: 220, color: '#e76f51', icon: 'wrench' },
    'grep':           { x: 360, y: 220, color: '#7209b7', icon: 'search' },
    'ls':             { x: 600, y: 220, color: '#e9c46a', icon: 'folder' },
    'find':           { x: 200, y: 360, color: '#219ebc', icon: 'binoculars' },
    'sessions_spawn': { x: 500, y: 360, color: '#d62828', icon: 'portal' },
};

const IDLE_X = CANVAS_W / 2;
const IDLE_Y = 510;

// ── State ────────────────────────────────────────────────────────────

const agents = new Map();
let colorIndex = 0;
let lastTime = 0;

// ── Agent class ──────────────────────────────────────────────────────

class Agent {
    constructor(id, name) {
        this.id = id;
        this.name = name;
        this.color = AGENT_COLORS[colorIndex++ % AGENT_COLORS.length];
        this.x = IDLE_X + (Math.random() - 0.5) * 60;
        this.y = IDLE_Y + (Math.random() - 0.5) * 30;
        this.targetX = this.x;
        this.targetY = this.y;
        this.state = 'idle';       // idle | moving | working
        this.currentTool = null;
        this.workPulse = 0;
        this.spawnAnim = 1.0;
        this.removing = false;
        this.removeAnim = 0;

        // Action queue: each entry is { type: 'visit'|'idle', tool?, dwellLeft? }
        this.queue = [];
        this.travelTimer = 0;      // time spent moving toward current target
        this.dwellTimer = 0;       // time spent working at current station
        this.arrived = false;      // whether we've reached the current target
    }

    // Queue a visit to a tool station.
    enqueueVisit(toolName) {
        this.queue.push({ type: 'visit', tool: toolName });
        this._advance();
    }

    // Queue a return-to-idle (collapses consecutive idles).
    enqueueIdle() {
        const last = this.queue[this.queue.length - 1];
        // Don't stack idle commands — one pending idle is enough.
        if (last && last.type === 'idle') return;
        this.queue.push({ type: 'idle' });
        this._advance();
    }

    // Start the next queued action if nothing is in progress.
    _advance() {
        if (this.state === 'moving' || (this.state === 'working' && this.dwellTimer < MIN_DWELL_TIME)) {
            return; // still busy with current action
        }
        if (this.queue.length === 0) return;

        const action = this.queue.shift();
        this.arrived = false;
        this.travelTimer = 0;
        this.dwellTimer = 0;

        if (action.type === 'visit') {
            const station = STATION_DEFS[action.tool];
            if (!station) { this._advance(); return; }
            this.targetX = station.x + (Math.random() - 0.5) * 20;
            this.targetY = station.y + STATION_H + 20 + (Math.random() - 0.5) * 10;
            this.state = 'moving';
            this.currentTool = action.tool;
        } else {
            this.targetX = IDLE_X + (this.id.charCodeAt(this.id.length - 1) % 10 - 5) * 12;
            this.targetY = IDLE_Y + (Math.random() - 0.5) * 20;
            this.state = 'moving';
            this.currentTool = null;
        }
    }

    update(dt) {
        const dx = this.targetX - this.x;
        const dy = this.targetY - this.y;
        const dist = Math.sqrt(dx * dx + dy * dy);

        this.x += dx * LERP_SPEED * dt;
        this.y += dy * LERP_SPEED * dt;
        this.travelTimer += dt;

        // Check if arrived at target.
        if (!this.arrived && (dist < 2 || this.travelTimer > MIN_TRAVEL_TIME + 1.0)) {
            this.arrived = true;
            if (this.currentTool) {
                this.state = 'working';
            } else {
                this.state = 'idle';
                this._advance();
            }
        }

        // Dwell at station then advance.
        if (this.state === 'working') {
            this.workPulse += dt * 3;
            this.dwellTimer += dt;
            if (this.dwellTimer >= MIN_DWELL_TIME) {
                this._advance();
            }
        }

        if (this.spawnAnim > 0) {
            this.spawnAnim = Math.max(0, this.spawnAnim - dt * 2);
        }

        if (this.removing) {
            this.removeAnim += dt * 2;
        }
    }
}

// ── Drawing helpers ──────────────────────────────────────────────────

function drawRoom(ctx) {
    // Floor
    ctx.fillStyle = '#16213e';
    ctx.fillRect(0, 0, CANVAS_W, CANVAS_H);

    // Floor tiles
    ctx.strokeStyle = 'rgba(83, 52, 131, 0.15)';
    ctx.lineWidth = 1;
    for (let x = 0; x < CANVAS_W; x += 40) {
        ctx.beginPath();
        ctx.moveTo(x, 0);
        ctx.lineTo(x, CANVAS_H);
        ctx.stroke();
    }
    for (let y = 0; y < CANVAS_H; y += 40) {
        ctx.beginPath();
        ctx.moveTo(0, y);
        ctx.lineTo(CANVAS_W, y);
        ctx.stroke();
    }

    // Idle area label
    ctx.fillStyle = 'rgba(165, 180, 252, 0.3)';
    ctx.font = '11px Courier New';
    ctx.textAlign = 'center';
    ctx.fillText('~ idle area ~', IDLE_X, IDLE_Y - 30);
}

function drawStation(ctx, name, def) {
    const { x, y, color, icon } = def;

    // Station background
    ctx.fillStyle = color;
    ctx.globalAlpha = 0.25;
    ctx.fillRect(x - STATION_W / 2, y - STATION_H / 2, STATION_W, STATION_H);
    ctx.globalAlpha = 1;

    // Station border
    ctx.strokeStyle = color;
    ctx.lineWidth = 2;
    ctx.strokeRect(x - STATION_W / 2, y - STATION_H / 2, STATION_W, STATION_H);

    // Icon
    drawIcon(ctx, icon, x, y - 4, color);

    // Label
    ctx.fillStyle = color;
    ctx.font = 'bold 10px Courier New';
    ctx.textAlign = 'center';
    ctx.fillText(name, x, y + STATION_H / 2 - 4);
}

function drawIcon(ctx, icon, x, y, color) {
    ctx.fillStyle = color;
    ctx.strokeStyle = color;
    ctx.lineWidth = 2;

    switch (icon) {
        case 'terminal':
            ctx.font = 'bold 16px Courier New';
            ctx.textAlign = 'center';
            ctx.fillText('>_', x, y + 6);
            break;
        case 'book':
            ctx.beginPath();
            ctx.moveTo(x - 8, y - 8);
            ctx.lineTo(x, y - 4);
            ctx.lineTo(x + 8, y - 8);
            ctx.lineTo(x + 8, y + 8);
            ctx.lineTo(x, y + 4);
            ctx.lineTo(x - 8, y + 8);
            ctx.closePath();
            ctx.stroke();
            ctx.beginPath();
            ctx.moveTo(x, y - 4);
            ctx.lineTo(x, y + 4);
            ctx.stroke();
            break;
        case 'pencil':
            ctx.beginPath();
            ctx.moveTo(x - 2, y + 10);
            ctx.lineTo(x, y + 12);
            ctx.lineTo(x + 2, y + 10);
            ctx.lineTo(x + 2, y - 8);
            ctx.lineTo(x - 2, y - 8);
            ctx.closePath();
            ctx.fill();
            ctx.beginPath();
            ctx.moveTo(x - 2, y - 8);
            ctx.lineTo(x, y - 12);
            ctx.lineTo(x + 2, y - 8);
            ctx.closePath();
            ctx.fill();
            break;
        case 'wrench':
            ctx.beginPath();
            ctx.arc(x, y - 6, 6, Math.PI * 0.8, Math.PI * 2.2);
            ctx.stroke();
            ctx.beginPath();
            ctx.moveTo(x - 2, y - 2);
            ctx.lineTo(x - 4, y + 10);
            ctx.lineTo(x + 4, y + 10);
            ctx.lineTo(x + 2, y - 2);
            ctx.closePath();
            ctx.fill();
            break;
        case 'search':
            ctx.beginPath();
            ctx.arc(x - 2, y - 2, 7, 0, Math.PI * 2);
            ctx.stroke();
            ctx.beginPath();
            ctx.moveTo(x + 3, y + 3);
            ctx.lineTo(x + 9, y + 9);
            ctx.stroke();
            break;
        case 'folder':
            ctx.beginPath();
            ctx.moveTo(x - 10, y - 4);
            ctx.lineTo(x - 10, y + 8);
            ctx.lineTo(x + 10, y + 8);
            ctx.lineTo(x + 10, y - 4);
            ctx.lineTo(x + 2, y - 4);
            ctx.lineTo(x, y - 8);
            ctx.lineTo(x - 10, y - 8);
            ctx.closePath();
            ctx.stroke();
            break;
        case 'binoculars':
            ctx.beginPath();
            ctx.arc(x - 5, y, 5, 0, Math.PI * 2);
            ctx.stroke();
            ctx.beginPath();
            ctx.arc(x + 5, y, 5, 0, Math.PI * 2);
            ctx.stroke();
            ctx.beginPath();
            ctx.moveTo(x - 5, y - 5);
            ctx.lineTo(x - 5, y - 10);
            ctx.stroke();
            ctx.beginPath();
            ctx.moveTo(x + 5, y - 5);
            ctx.lineTo(x + 5, y - 10);
            ctx.stroke();
            break;
        case 'portal':
            ctx.beginPath();
            ctx.ellipse(x, y, 8, 12, 0, 0, Math.PI * 2);
            ctx.stroke();
            ctx.beginPath();
            ctx.ellipse(x, y, 3, 8, 0, 0, Math.PI * 2);
            ctx.stroke();
            break;
    }
}

function drawAgent(ctx, agent) {
    const r = AGENT_RADIUS;
    let alpha = 1;

    // Spawn animation: grow in
    let scale = 1 - agent.spawnAnim;
    if (agent.removing) {
        alpha = Math.max(0, 1 - agent.removeAnim);
        scale = alpha;
    }

    ctx.save();
    ctx.globalAlpha = alpha;
    ctx.translate(agent.x, agent.y);
    ctx.scale(scale, scale);

    // Working glow
    if (agent.state === 'working') {
        const pulse = 0.3 + Math.sin(agent.workPulse) * 0.15;
        ctx.beginPath();
        ctx.arc(0, 0, r + 6, 0, Math.PI * 2);
        ctx.fillStyle = agent.color;
        ctx.globalAlpha = pulse * alpha;
        ctx.fill();
        ctx.globalAlpha = alpha;
    }

    // Body
    ctx.beginPath();
    ctx.arc(0, 0, r, 0, Math.PI * 2);
    ctx.fillStyle = agent.color;
    ctx.fill();

    // Border
    ctx.strokeStyle = agent.state === 'idle' ? '#555' : '#fff';
    ctx.lineWidth = 2;
    ctx.stroke();

    // Letter
    const letter = agent.id === 'main' ? 'M' : agent.name.charAt(0).toUpperCase();
    ctx.fillStyle = '#fff';
    ctx.font = 'bold 14px Courier New';
    ctx.textAlign = 'center';
    ctx.textBaseline = 'middle';
    ctx.fillText(letter, 0, 1);

    // Context bar placeholder (future)
    ctx.fillStyle = 'rgba(255,255,255,0.2)';
    ctx.fillRect(-r, -r - 8, r * 2, 4);
    ctx.fillStyle = '#06d6a0';
    ctx.fillRect(-r, -r - 8, r * 2 * 0.7, 4); // 70% placeholder

    ctx.restore();

    // Name label
    ctx.globalAlpha = alpha;
    ctx.fillStyle = '#a5b4fc';
    ctx.font = '10px Courier New';
    ctx.textAlign = 'center';
    ctx.fillText(agent.name, agent.x, agent.y + r + 12);
    ctx.globalAlpha = 1;
}

// ── Game loop ────────────────────────────────────────────────────────

const canvas = document.getElementById('room');
const ctx = canvas.getContext('2d');

function resizeCanvas() {
    const container = canvas.parentElement;
    const rect = container.getBoundingClientRect();
    const sidebarWidth = 280;
    const availW = rect.width - sidebarWidth;
    const availH = rect.height;

    const scaleX = availW / CANVAS_W;
    const scaleY = availH / CANVAS_H;
    const scale = Math.min(scaleX, scaleY);

    canvas.style.width = (CANVAS_W * scale) + 'px';
    canvas.style.height = (CANVAS_H * scale) + 'px';
}

window.addEventListener('resize', resizeCanvas);
resizeCanvas();

function gameLoop(timestamp) {
    const dt = Math.min((timestamp - lastTime) / 1000, 0.1);
    lastTime = timestamp;

    // Update agents
    agents.forEach(a => a.update(dt));

    // Remove fully faded agents
    agents.forEach((a, id) => {
        if (a.removing && a.removeAnim >= 1) {
            agents.delete(id);
        }
    });

    // Draw
    ctx.clearRect(0, 0, CANVAS_W, CANVAS_H);
    drawRoom(ctx);

    for (const [name, def] of Object.entries(STATION_DEFS)) {
        drawStation(ctx, name, def);
    }

    agents.forEach(a => drawAgent(ctx, a));

    // Title
    ctx.fillStyle = '#a5b4fc';
    ctx.font = 'bold 14px Courier New';
    ctx.textAlign = 'left';
    ctx.fillText('Yak Agent Room', 12, 20);

    requestAnimationFrame(gameLoop);
}

requestAnimationFrame(gameLoop);

// ── Agent management ─────────────────────────────────────────────────

function getOrCreateAgent(id, name) {
    if (!agents.has(id)) {
        agents.set(id, new Agent(id, name || id));
    }
    return agents.get(id);
}

// Ensure the main agent always exists.
getOrCreateAgent('main', 'orchestrator');

// ── Event log ────────────────────────────────────────────────────────

const logEl = document.getElementById('log');
const statusEl = document.getElementById('status');

function addLogEntry(ev) {
    const entry = document.createElement('div');
    entry.className = 'log-entry';

    if (ev.type === 'agent_spawn' || ev.type === 'agent_done' || ev.type === 'agent_start' || ev.type === 'agent_end') {
        entry.classList.add('spawn');
    }
    if (ev.status === 'error') {
        entry.classList.add('error');
    }

    const time = new Date(ev.ts).toLocaleTimeString();
    let text = '';

    switch (ev.type) {
        case 'tool_start':
            text = `<span class="time">${time}</span> <span class="agent">${ev.agent_name}</span> -> <span class="tool">${ev.tool}</span>`;
            break;
        case 'tool_end':
            text = `<span class="time">${time}</span> <span class="agent">${ev.agent_name}</span> <- <span class="tool">${ev.tool}</span> [${ev.status}]`;
            break;
        case 'agent_spawn':
            text = `<span class="time">${time}</span> spawning <span class="agent">${ev.agent_name}</span>`;
            break;
        case 'agent_done':
            text = `<span class="time">${time}</span> <span class="agent">${ev.agent_name}</span> finished`;
            break;
        case 'agent_start':
            text = `<span class="time">${time}</span> <span class="agent">${ev.agent_name}</span> started`;
            break;
        case 'agent_end':
            text = `<span class="time">${time}</span> <span class="agent">${ev.agent_name}</span> completed`;
            break;
    }

    entry.innerHTML = text;
    logEl.appendChild(entry);
    logEl.scrollTop = logEl.scrollHeight;

    // Keep log size bounded.
    while (logEl.children.length > 200) {
        logEl.removeChild(logEl.firstChild);
    }
}

// ── SSE connection ───────────────────────────────────────────────────

function handleEvent(ev) {
    switch (ev.type) {
        case 'tool_start': {
            const agent = getOrCreateAgent(ev.agent_id, ev.agent_name);
            agent.enqueueVisit(ev.tool);
            break;
        }
        case 'tool_end': {
            const agent = getOrCreateAgent(ev.agent_id, ev.agent_name);
            agent.enqueueIdle();
            break;
        }
        case 'agent_spawn': {
            // The actual subagent will appear when it makes its first tool call.
            break;
        }
        case 'agent_done': {
            // Find the agent by name and start remove animation.
            agents.forEach(a => {
                if (a.name === ev.agent_name && a.id !== 'main') {
                    a.removing = true;
                }
            });
            break;
        }
        case 'agent_start': {
            getOrCreateAgent(ev.agent_id, ev.agent_name);
            break;
        }
        case 'agent_end': {
            const agent = getOrCreateAgent(ev.agent_id, ev.agent_name);
            agent.enqueueIdle();
            if (ev.agent_id !== 'main') {
                agent.removing = true;
            }
            break;
        }
    }
    addLogEntry(ev);
}

function connect() {
    const source = new EventSource('/events');

    source.onopen = () => {
        statusEl.textContent = 'Connected';
        statusEl.className = 'connected';
    };

    source.onmessage = (e) => {
        const ev = JSON.parse(e.data);
        handleEvent(ev);
    };

    source.onerror = () => {
        statusEl.textContent = 'Disconnected - reconnecting...';
        statusEl.className = 'disconnected';
    };
}

connect();
