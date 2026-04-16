// ── Constants ────────────────────────────────────────────────────────

const WALK_SPEED = 64;           // pixels per second (constant, axis-aligned)
const MIN_DWELL_TIME = 0.35;     // seconds working at station
const WALK_FRAME_DISTANCE = 8;   // pixels between walk frame flips

const DIR_DOWN = 0;
const DIR_UP = 1;
const DIR_LEFT = 2;
const DIR_RIGHT = 3;

// ── Globals populated by loadAssets ──────────────────────────────────

let MAP = null;               // parsed map.json
let TILES_IMG = null;         // background tileset
let CHARS_IMG = null;         // character spritesheet
let ANIM_IMG = null;          // animated-tile sheet
let TILE = 16;                // tile size (pixels)
let CHAR_W = 16;
let CHAR_H = 24;
let CANVAS_W = 0;
let CANVAS_H = 0;
let TILESET_COLS = 0;         // tiles per row in tiles.png

// ── State ────────────────────────────────────────────────────────────

const agents = new Map();
let lastTime = 0;
let nextCharRow = 0;

// ── Agent class ──────────────────────────────────────────────────────

class Agent {
    constructor(id, name) {
        this.id = id;
        this.name = name;
        this.charRow = pickCharRow(id);

        const spawn = spawnPixel(id);
        this.x = Math.round(spawn.x);
        this.y = Math.round(spawn.y);
        this.facing = DIR_DOWN;
        this.state = 'idle';       // idle | moving | working
        this.currentTool = null;
        this.dwellTimer = 0;

        // Axis-aligned waypoint path. Each waypoint is {x, y}; the agent
        // always moves along one axis at a time toward the next waypoint.
        this.path = [];

        this.walkDistance = 0;      // accumulated pixels travelled (for frame timing)
        this.walkFrame = 0;         // 0 = idle, 1 | 2 = walk frames

        this.spawnAnim = 1.0;
        this.removing = false;
        this.removeAnim = 0;

        this.queue = [];            // [{type:'visit'|'idle', tool?}]

        // Usage/context.
        this.promptTokens = 0;
        this.contextSize = 0;
    }

    enqueueVisit(toolName) {
        this.queue.push({ type: 'visit', tool: toolName });
        this._advance();
    }

    enqueueIdle() {
        const last = this.queue[this.queue.length - 1];
        if (last && last.type === 'idle') return;
        this.queue.push({ type: 'idle' });
        this._advance();
    }

    _setPathTo(tx, ty) {
        // Decompose (current → target) into axis-aligned segments: first
        // horizontal, then vertical. This gives grid-style movement.
        tx = Math.round(tx);
        ty = Math.round(ty);
        this.path = [];
        if (tx !== this.x) {
            this.path.push({ x: tx, y: this.y });
        }
        if (ty !== this.y) {
            this.path.push({ x: tx, y: ty });
        }
        if (this.path.length === 0) {
            this.x = tx;
            this.y = ty;
        }
    }

    _advance() {
        if (this.state === 'moving' ||
            (this.state === 'working' && this.dwellTimer < MIN_DWELL_TIME)) {
            return;
        }
        if (this.queue.length === 0) return;

        const action = this.queue.shift();
        this.dwellTimer = 0;

        if (action.type === 'visit') {
            const station = MAP && MAP.stations && MAP.stations[action.tool];
            if (!station) { this._advance(); return; }
            const tx = (station[0] + 0.5) * TILE;
            const ty = (station[1] + 1.5) * TILE; // stand south of station tile
            this._setPathTo(tx, ty);
            this.currentTool = action.tool;
        } else {
            const idle = idlePixel();
            const hash = this.id.charCodeAt(this.id.length - 1) || 0;
            const tx = idle.x + ((hash % 10) - 5) * (TILE * 0.6);
            const ty = idle.y + (Math.random() - 0.5) * TILE;
            this._setPathTo(tx, ty);
            this.currentTool = null;
        }

        if (this.path.length > 0) {
            this.state = 'moving';
        } else if (this.currentTool) {
            this.state = 'working';
        } else {
            this.state = 'idle';
            this._advance();
        }
    }

    update(dt) {
        if (this.state === 'moving' && this.path.length > 0) {
            const wp = this.path[0];
            // Each waypoint is axis-aligned from the previous position, so
            // exactly one of dx/dy is non-zero. Pick the non-zero axis —
            // never blend the two.
            const dx = wp.x - this.x;
            const dy = wp.y - this.y;
            const budget = WALK_SPEED * dt;
            let stepped = 0;

            if (dx !== 0) {
                const step = Math.sign(dx) * Math.min(Math.abs(dx), budget);
                this.x += step;
                stepped = Math.abs(step);
                this.facing = dx > 0 ? DIR_RIGHT : DIR_LEFT;
            } else if (dy !== 0) {
                const step = Math.sign(dy) * Math.min(Math.abs(dy), budget);
                this.y += step;
                stepped = Math.abs(step);
                this.facing = dy > 0 ? DIR_DOWN : DIR_UP;
            }

            this.walkDistance += stepped;
            if (this.walkDistance > WALK_FRAME_DISTANCE) {
                this.walkDistance = 0;
                this.walkFrame = this.walkFrame === 1 ? 2 : 1;
            }

            // Waypoint reached? Snap and advance to the next leg.
            if (Math.abs(wp.x - this.x) < 0.5 && Math.abs(wp.y - this.y) < 0.5) {
                this.x = wp.x;
                this.y = wp.y;
                this.path.shift();
                if (this.path.length === 0) {
                    if (this.currentTool) {
                        this.state = 'working';
                    } else {
                        this.state = 'idle';
                        this._advance();
                    }
                }
            }
        } else {
            this.walkFrame = 0;
        }

        if (this.state === 'working') {
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

function pickCharRow(id) {
    if (!MAP || !MAP.characters) return 0;
    if (id === 'main' && MAP.characters.main) return MAP.characters.main.row;
    const def = MAP.characters.default || { row: 1 };
    nextCharRow++;
    return def.row;
}

function idlePixel() {
    if (!MAP || !MAP.idle) return { x: 0, y: 0 };
    return {
        x: (MAP.idle[0] + 0.5) * TILE,
        y: (MAP.idle[1] + 0.5) * TILE,
    };
}

// Subagents emerge from the sessions_spawn portal; main starts at idle.
function spawnPixel(id) {
    if (id !== 'main' && MAP && MAP.stations && MAP.stations.sessions_spawn) {
        const s = MAP.stations.sessions_spawn;
        return {
            x: (s[0] + 0.5) * TILE,
            y: (s[1] + 1.5) * TILE, // one tile south of the portal, like station targets
        };
    }
    const idle = idlePixel();
    return {
        x: idle.x + (Math.random() - 0.5) * TILE * 1.5,
        y: idle.y + (Math.random() - 0.5) * TILE,
    };
}

// ── Asset loading ────────────────────────────────────────────────────

async function loadImage(src) {
    const img = new Image();
    img.src = src;
    await img.decode();
    return img;
}

async function loadAssets() {
    const res = await fetch('assets/map.json');
    MAP = await res.json();
    TILE = MAP.tileSize || 16;
    CHAR_W = MAP.charWidth || 16;
    CHAR_H = MAP.charHeight || 24;
    CANVAS_W = MAP.cols * TILE;
    CANVAS_H = MAP.rows * TILE;

    TILES_IMG = await loadImage(MAP.tileset);
    CHARS_IMG = await loadImage(MAP.chars);
    if (MAP.anim) {
        ANIM_IMG = await loadImage(MAP.anim);
    }
    TILESET_COLS = Math.max(1, Math.floor(TILES_IMG.width / TILE));
}

// ── Drawing ──────────────────────────────────────────────────────────

function drawTile(ctx, id, px, py, nowMs) {
    if (id === 0) return;

    const animDef = MAP.animated && MAP.animated[String(id)];
    if (animDef && ANIM_IMG) {
        const fps = animDef.fps || 4;
        const frames = animDef.frames || [0];
        const idx = Math.floor((nowMs / 1000) * fps) % frames.length;
        const fi = frames[idx];
        ctx.drawImage(ANIM_IMG, fi * TILE, 0, TILE, TILE, px, py, TILE, TILE);
        return;
    }

    const srcIdx = id - 1; // 1-indexed tile ids
    const sx = (srcIdx % TILESET_COLS) * TILE;
    const sy = Math.floor(srcIdx / TILESET_COLS) * TILE;
    ctx.drawImage(TILES_IMG, sx, sy, TILE, TILE, px, py, TILE, TILE);
}

function drawLayer(ctx, layer, nowMs) {
    if (!layer) return;
    for (let ty = 0; ty < layer.length; ty++) {
        const row = layer[ty];
        for (let tx = 0; tx < row.length; tx++) {
            drawTile(ctx, row[tx], tx * TILE, ty * TILE, nowMs);
        }
    }
}

function drawMap(ctx, nowMs) {
    ctx.fillStyle = '#16213e';
    ctx.fillRect(0, 0, CANVAS_W, CANVAS_H);
    drawLayer(ctx, MAP.layers.ground, nowMs);
    drawLayer(ctx, MAP.layers.overlay, nowMs);
}

function drawStationLabels(ctx) {
    ctx.fillStyle = 'rgba(255,255,255,0.75)';
    ctx.strokeStyle = 'rgba(0,0,0,0.85)';
    ctx.lineWidth = 3;
    ctx.font = 'bold 9px "Courier New", monospace';
    ctx.textAlign = 'center';
    ctx.textBaseline = 'middle';
    for (const [name, pos] of Object.entries(MAP.stations || {})) {
        const x = (pos[0] + 0.5) * TILE;
        const y = (pos[1] - 0.3) * TILE;
        ctx.strokeText(name, x, y);
        ctx.fillText(name, x, y);
    }
}

function drawAgent(ctx, agent) {
    let alpha = 1;
    let scale = 1 - agent.spawnAnim;
    if (agent.removing) {
        alpha = Math.max(0, 1 - agent.removeAnim);
        scale = alpha;
    }
    if (scale <= 0) return;

    const sx = agent.walkFrame * CHAR_W;
    const sy = agent.charRow * CHAR_H * 4 + agent.facing * CHAR_H;

    const drawW = CHAR_W * scale;
    const drawH = CHAR_H * scale;
    const px = Math.round(agent.x - drawW / 2);
    const py = Math.round(agent.y - drawH + TILE / 2); // feet near (x,y)

    ctx.save();
    ctx.globalAlpha = alpha;
    ctx.imageSmoothingEnabled = false;

    // Working indicator: subtle shadow oval pulsing.
    if (agent.state === 'working') {
        const t = performance.now() / 200;
        const pulse = 0.25 + Math.sin(t) * 0.1;
        ctx.globalAlpha = alpha * pulse;
        ctx.fillStyle = '#ffd166';
        ctx.beginPath();
        ctx.ellipse(agent.x, agent.y + TILE / 2 - 1, CHAR_W * 0.6, 3, 0, 0, Math.PI * 2);
        ctx.fill();
        ctx.globalAlpha = alpha;
    }

    ctx.drawImage(CHARS_IMG, sx, sy, CHAR_W, CHAR_H, px, py, drawW, drawH);

    // Context/health bar floating above the head.
    drawHealthBar(ctx, agent, px, py);

    // Name label below feet.
    ctx.fillStyle = '#a5b4fc';
    ctx.strokeStyle = 'rgba(0,0,0,0.85)';
    ctx.lineWidth = 3;
    ctx.font = 'bold 9px "Courier New", monospace';
    ctx.textAlign = 'center';
    ctx.textBaseline = 'top';
    const labelY = py + drawH + 1;
    ctx.strokeText(agent.name, agent.x, labelY);
    ctx.fillText(agent.name, agent.x, labelY);

    ctx.restore();
}

function drawHealthBar(ctx, agent, px, py) {
    const barW = CHAR_W + 2;
    const barH = 3;
    const bx = Math.round(agent.x - barW / 2);
    const by = py - 5;

    // Background frame.
    ctx.fillStyle = 'rgba(0,0,0,0.75)';
    ctx.fillRect(bx - 1, by - 1, barW + 2, barH + 2);
    ctx.fillStyle = '#222';
    ctx.fillRect(bx, by, barW, barH);

    if (agent.contextSize <= 0) return;

    const ratio = Math.max(0, Math.min(1, agent.promptTokens / agent.contextSize));
    if (ratio <= 0) return;

    let color = '#06d6a0';           // green
    if (ratio > 0.85) color = '#ef476f'; // red
    else if (ratio > 0.6) color = '#ffd166'; // yellow

    ctx.fillStyle = color;
    ctx.fillRect(bx, by, Math.max(1, Math.floor(barW * ratio)), barH);
}

// ── Game loop ────────────────────────────────────────────────────────

const canvas = document.getElementById('room');
const ctx = canvas.getContext('2d');
ctx.imageSmoothingEnabled = false;

function resizeCanvas() {
    if (!CANVAS_W || !CANVAS_H) return;
    canvas.width = CANVAS_W;
    canvas.height = CANVAS_H;

    const container = canvas.parentElement;
    const rect = container.getBoundingClientRect();
    const sidebarWidth = 280;
    const availW = rect.width - sidebarWidth;
    const availH = rect.height;

    const scaleX = availW / CANVAS_W;
    const scaleY = availH / CANVAS_H;
    const scale = Math.max(0.1, Math.min(scaleX, scaleY));

    canvas.style.width = (CANVAS_W * scale) + 'px';
    canvas.style.height = (CANVAS_H * scale) + 'px';
    canvas.style.imageRendering = 'pixelated';
}

function gameLoop(timestamp) {
    const dt = Math.min((timestamp - lastTime) / 1000, 0.1);
    lastTime = timestamp;
    const nowMs = timestamp;

    agents.forEach(a => a.update(dt));
    agents.forEach((a, id) => {
        if (a.removing && a.removeAnim >= 1) agents.delete(id);
    });

    ctx.clearRect(0, 0, CANVAS_W, CANVAS_H);
    drawMap(ctx, nowMs);
    drawStationLabels(ctx);

    // Depth sort so agents further down overlap those above.
    const sorted = Array.from(agents.values()).sort((a, b) => a.y - b.y);
    sorted.forEach(a => drawAgent(ctx, a));

    requestAnimationFrame(gameLoop);
}

// ── Agent management ─────────────────────────────────────────────────

function getOrCreateAgent(id, name) {
    if (!agents.has(id)) {
        agents.set(id, new Agent(id, name || id));
    }
    return agents.get(id);
}

// ── Event log ────────────────────────────────────────────────────────

const logEl = document.getElementById('log');
const statusEl = document.getElementById('status');
const sessionTotalTokensEl = document.getElementById('session-total-tokens');
const sessionAgentCountEl = document.getElementById('session-agent-count');
const agentStatsEl = document.getElementById('agent-stats');
const tokenStats = new Map();

function getTokenStat(id, name) {
    if (!tokenStats.has(id)) {
        tokenStats.set(id, {
            id,
            name: name || id,
            calls: 0,
            totalTokens: 0,
            promptTokens: 0,
            contextSize: 0,
        });
    }
    const stat = tokenStats.get(id);
    if (name) stat.name = name;
    return stat;
}

function formatNumber(n) {
    return new Intl.NumberFormat().format(n || 0);
}

function renderDashboard() {
    let sessionTotal = 0;
    const rows = Array.from(tokenStats.values())
        .sort((a, b) => b.totalTokens - a.totalTokens || a.name.localeCompare(b.name));

    for (const row of rows) {
        sessionTotal += row.totalTokens;
    }

    sessionTotalTokensEl.textContent = formatNumber(sessionTotal);
    sessionAgentCountEl.textContent = formatNumber(rows.length);

    agentStatsEl.innerHTML = '';
    for (const row of rows) {
        const pct = row.contextSize > 0
            ? Math.round((row.promptTokens / row.contextSize) * 100)
            : null;
        const el = document.createElement('div');
        el.className = 'agent-stat';
        el.innerHTML = `
            <div class="agent-stat-header">
                <span class="agent-stat-name">${row.name}</span>
                <span class="agent-stat-total">${formatNumber(row.totalTokens)} tok</span>
            </div>
            <div class="agent-stat-meta">
                <span>Calls <strong>${formatNumber(row.calls)}</strong></span>
                <span>Last prompt <strong>${formatNumber(row.promptTokens)}</strong></span>
                <span>Context <strong>${row.contextSize > 0 ? formatNumber(row.contextSize) : '—'}</strong></span>
                <span>Usage <strong>${pct === null ? '—' : pct + '%'}</strong></span>
            </div>
        `;
        agentStatsEl.appendChild(el);
    }
}

function addLogEntry(ev) {
    const entry = document.createElement('div');
    entry.className = 'log-entry';

    if (ev.type === 'agent_spawn' || ev.type === 'agent_done' ||
        ev.type === 'agent_start' || ev.type === 'agent_end') {
        entry.classList.add('spawn');
    }
    if (ev.status === 'error') entry.classList.add('error');

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
        case 'agent_usage': {
            const pct = ev.context_size > 0
                ? Math.round((ev.prompt_tokens / ev.context_size) * 100)
                : 0;
            text = `<span class="time">${time}</span> <span class="agent">${ev.agent_name}</span> ctx ${ev.prompt_tokens}/${ev.context_size} (${pct}%)`;
            break;
        }
    }
    if (!text) return;

    entry.innerHTML = text;
    logEl.appendChild(entry);
    logEl.scrollTop = logEl.scrollHeight;
    while (logEl.children.length > 200) {
        logEl.removeChild(logEl.firstChild);
    }
}

// ── Event dispatch ───────────────────────────────────────────────────

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
        case 'agent_spawn':
            break;
        case 'agent_done': {
            agents.forEach(a => {
                if (a.name === ev.agent_name && a.id !== 'main') a.removing = true;
            });
            break;
        }
        case 'agent_start': {
            getOrCreateAgent(ev.agent_id, ev.agent_name);
            getTokenStat(ev.agent_id, ev.agent_name);
            break;
        }
        case 'agent_end': {
            const agent = getOrCreateAgent(ev.agent_id, ev.agent_name);
            agent.enqueueIdle();
            if (ev.agent_id !== 'main') agent.removing = true;
            break;
        }
        case 'agent_usage': {
            const agent = getOrCreateAgent(ev.agent_id, ev.agent_name);
            agent.promptTokens = ev.prompt_tokens || 0;
            agent.contextSize = ev.context_size || 0;

            const stat = getTokenStat(ev.agent_id, ev.agent_name);
            stat.calls += 1;
            stat.totalTokens += ev.total_tokens || 0;
            stat.promptTokens = ev.prompt_tokens || 0;
            stat.contextSize = ev.context_size || 0;
            break;
        }
    }
    addLogEntry(ev);
    renderDashboard();
}

// ── SSE connection ───────────────────────────────────────────────────

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

// ── Bootstrap ────────────────────────────────────────────────────────

async function main() {
    try {
        await loadAssets();
    } catch (err) {
        console.error('asset load failed', err);
        statusEl.textContent = 'Asset load failed';
        statusEl.className = 'disconnected';
        return;
    }

    resizeCanvas();
    window.addEventListener('resize', resizeCanvas);

    getOrCreateAgent('main', 'orchestrator');
    getTokenStat('main', 'orchestrator');
    renderDashboard();

    requestAnimationFrame(gameLoop);
    connect();
}

main();
