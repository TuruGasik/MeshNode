// MeshNode Tracker - Frontend JavaScript
let map;
let trackerPassword = '';
let autoRefreshInterval = null;
let markers = {};
let pathLines = {};
let nodePositions = {};
let nodeList = [];
let filteredNodeList = [];
let selectedNodeHexId = null;

const TRACKER_API = '/api/tracker';
const MIN_VISIBLE_POSITIONS = 2;
const DEFAULT_MAP_CENTER = [-2.5, 118];
const DEFAULT_MAP_ZOOM = 5;

// Session storage keys
const SESSION_KEY = 'tracker_session';
const SESSION_EXPIRY = 60 * 60 * 1000; // 1 hour

// Initialize on page load
document.addEventListener('DOMContentLoaded', () => {
  checkSession();
  initMap();
  setupEventListeners();
});

function checkSession() {
  const session = sessionStorage.getItem(SESSION_KEY);
  if (session) {
    const { expiry } = JSON.parse(session);
    if (Date.now() < expiry) {
      document.getElementById('login-overlay').classList.add('hidden');
      document.getElementById('main-content').classList.remove('hidden');
      fetchTrackerNodes();
      setTimeout(() => map && map.invalidateSize(), 100);
      return;
    }
  }
  sessionStorage.removeItem(SESSION_KEY);
}

function initMap() {
  map = L.map('map').setView(DEFAULT_MAP_CENTER, DEFAULT_MAP_ZOOM);
  L.tileLayer('https://{s}.tile.openstreetmap.org/{z}/{x}/{y}.png', {
    attribution: '© OpenStreetMap contributors',
    maxZoom: 19
  }).addTo(map);

  map.on('click', clearSelectedNode);
}

function setupEventListeners() {
  // Login form
  document.getElementById('login-form').addEventListener('submit', async (e) => {
    e.preventDefault();
    const password = document.getElementById('password-input').value;
    await login(password);
  });

  // Logout button
  document.getElementById('logout-btn').addEventListener('click', () => {
    sessionStorage.removeItem(SESSION_KEY);
    trackerPassword = '';
    document.getElementById('login-overlay').classList.remove('hidden');
    document.getElementById('main-content').classList.add('hidden');
    clearMap();
    if (autoRefreshInterval) {
      clearInterval(autoRefreshInterval);
      autoRefreshInterval = null;
    }
  });

  // Show paths toggle
  document.getElementById('show-paths').addEventListener('change', (e) => {
    togglePaths(e.target.checked);
  });

  // Auto-refresh toggle
  document.getElementById('auto-refresh').addEventListener('change', (e) => {
    if (e.target.checked) {
      startAutoRefresh();
    } else {
      stopAutoRefresh();
    }
  });

  document.getElementById('node-search').addEventListener('input', applySearchFilter);
}

async function login(password) {
  const errorEl = document.getElementById('login-error');
  errorEl.textContent = '';

  try {
    const response = await fetch(`${TRACKER_API}/nodes`, {
      headers: { 'X-Tracker-Password': password }
    });

    if (!response.ok) {
      errorEl.textContent = 'Invalid password';
      return;
    }

    // Store session
    trackerPassword = password;
    sessionStorage.setItem(SESSION_KEY, JSON.stringify({
      expiry: Date.now() + SESSION_EXPIRY
    }));

    document.getElementById('login-overlay').classList.add('hidden');
    document.getElementById('main-content').classList.remove('hidden');
    fetchTrackerNodes();
    startAutoRefresh();
    setTimeout(() => map && map.invalidateSize(), 100);

  } catch (err) {
    errorEl.textContent = 'Connection error';
  }
}

function startAutoRefresh() {
  stopAutoRefresh();
  autoRefreshInterval = setInterval(fetchTrackerNodes, 30000);
}

function stopAutoRefresh() {
  if (autoRefreshInterval) {
    clearInterval(autoRefreshInterval);
    autoRefreshInterval = null;
  }
}

async function fetchTrackerNodes() {
  try {
    const response = await fetch(`${TRACKER_API}/nodes`, {
      headers: { 'X-Tracker-Password': trackerPassword }
    });

    if (response.status === 401) {
      sessionStorage.removeItem(SESSION_KEY);
      window.location.reload();
      return;
    }

    if (!response.ok) return;

    const data = await response.json();
    // API returns array directly, not {nodes: [...]}
    const fetchedNodes = Array.isArray(data) ? data : (data.nodes || []);
    nodeList = fetchedNodes.filter(node => getPositionCount(node) >= MIN_VISIBLE_POSITIONS);
    nodePositions = {};

    // Index positions by node from the positions array
    nodeList.forEach(node => {
      nodePositions[node.hexId] = node.positions || [];
    });

    applySearchFilter();
    updateNodeList();
    updateMapMarkers();
    updateStats(nodeList.length);

  } catch (err) {
    console.error('Failed to fetch tracker nodes:', err);
  }
}

function updateNodeList() {
  const container = document.getElementById('node-list');
  container.innerHTML = '';

  filteredNodeList.forEach(node => {
    const status = getNodeStatus(node.lastSeen);
    const positions = nodePositions[node.hexId] || [];

    const div = document.createElement('div');
    div.className = 'node-item';
    div.dataset.hexId = node.hexId;

    div.innerHTML = `
      <div class="node-name">${node.longName || node.shortName || node.hexId}</div>
      <div class="node-info">${node.shortName || '-'} · ${node.hwModel || '-'}</div>
      <div class="node-status ${status.class}">${status.text} · ${node.positionCount || 0} positions</div>
    `;

    div.addEventListener('click', () => {
      selectNode(node.hexId);
      if (node.latitude && node.longitude) {
        map.setView([node.latitude, node.longitude], 15);
      }
    });

    container.appendChild(div);
  });
}

function applySearchFilter() {
  const query = document.getElementById('node-search').value.trim().toLowerCase();
  filteredNodeList = nodeList.filter(node => {
    if (!query) return true;

    const searchableText = [
      node.hexId,
      node.longName,
      node.shortName,
      node.hwModel
    ].filter(Boolean).join(' ').toLowerCase();

    return searchableText.includes(query);
  });

  if (selectedNodeHexId && !filteredNodeList.some(node => node.hexId === selectedNodeHexId)) {
    selectedNodeHexId = null;
  }

  updateNodeList();
  updateMapMarkers();
}

function getPositionCount(node) {
  if (typeof node.positionCount === 'number') return node.positionCount;
  return Array.isArray(node.positions) ? node.positions.length : 0;
}

function updateMapMarkers() {
  // Clear old markers and paths
  Object.values(markers).forEach(m => map.removeLayer(m));
  Object.values(pathLines).forEach(l => map.removeLayer(l));
  markers = {};
  pathLines = {};

  if (selectedNodeHexId && !filteredNodeList.some(node => node.hexId === selectedNodeHexId)) {
    selectedNodeHexId = null;
  }

  // Add new markers
  filteredNodeList.forEach(node => {
    const positions = nodePositions[node.hexId] || [];
    if (!node.latitude || !node.longitude) return;

    const lat = node.latitude;
    const lon = node.longitude;
    const status = getNodeStatus(node.lastSeen);

    // Create custom icon
    const icon = L.divIcon({
      className: 'marker-icon',
      html: `<div style="
        width: 24px; height: 24px;
        background: ${status.color};
        border: 2px solid #fff;
        border-radius: 50%;
        box-shadow: 0 2px 8px rgba(0,0,0,0.3);
      "></div>`,
      iconSize: [24, 24],
      iconAnchor: [12, 12]
    });

    const marker = L.marker([lat, lon], { icon }).addTo(map);
    marker.bindPopup(`
      <strong>${node.longName || node.shortName || node.hexId}</strong><br>
      ${node.shortName || '-'} · ${node.hwModel || '-'}<br>
      Last seen: ${formatTime(node.lastSeen)}<br>
      Positions: ${node.positionCount || 0}
    `);

    markers[node.hexId] = marker;

    marker.on('click', (e) => {
      L.DomEvent.stopPropagation(e);
      selectNode(node.hexId);
    });
  });

  drawSelectedPath();
}

function togglePaths(show) {
  if (show) {
    drawSelectedPath();
  } else {
    Object.values(pathLines).forEach(l => map.removeLayer(l));
    pathLines = {};
  }
}

function selectNode(hexId) {
  if (selectedNodeHexId === hexId) {
    clearSelectedNode();
    return;
  }

  selectedNodeHexId = hexId;

  document.querySelectorAll('.node-item').forEach(el => {
    el.classList.toggle('active', el.dataset.hexId === hexId);
  });

  drawSelectedPath();
}

function clearSelectedNode() {
  selectedNodeHexId = null;

  document.querySelectorAll('.node-item').forEach(el => {
    el.classList.remove('active');
  });

  Object.values(pathLines).forEach(l => map.removeLayer(l));
  pathLines = {};
}

function drawSelectedPath() {
  Object.values(pathLines).forEach(l => map.removeLayer(l));
  pathLines = {};

  if (!selectedNodeHexId || !document.getElementById('show-paths').checked) return;

  const node = filteredNodeList.find(item => item.hexId === selectedNodeHexId);
  const positions = nodePositions[selectedNodeHexId] || [];
  if (!node || positions.length <= 1) return;

  const status = getNodeStatus(node.lastSeen);
  // Use last 50 positions for the selected node path only.
  const pathPositions = positions.slice(0, 50);
  const coords = pathPositions.map(p => [p.latitude, p.longitude]);
  const polyline = L.polyline(coords, {
    color: status.color,
    weight: 3,
    opacity: 0.7
  }).addTo(map);
  pathLines[selectedNodeHexId] = polyline;
}

function updateStats(count) {
  document.getElementById('node-count').textContent = `${count} nodes`;
  document.getElementById('last-update').textContent = `Updated: ${new Date().toLocaleTimeString()}`;
}

function clearMap() {
  Object.values(markers).forEach(m => map.removeLayer(m));
  Object.values(pathLines).forEach(l => map.removeLayer(l));
  markers = {};
  pathLines = {};
  selectedNodeHexId = null;
}

function getNodeStatus(lastSeen) {
  const now = Date.now() / 1000;
  const diff = now - lastSeen;
  const minutes = Math.floor(diff / 60);
  const hours = Math.floor(diff / 3600);

  if (minutes < 60) {
    return { class: 'online', text: `Online (${minutes}m ago)`, color: '#22c55e' };
  } else if (hours < 6) {
    return { class: 'online', text: `Active (${hours}h ago)`, color: '#eab308' };
  } else if (hours < 24) {
    return { class: 'offline', text: `Offline (${hours}h ago)`, color: '#ef4444' };
  } else {
    return { class: 'offline', text: `Dead (>${hours}h)`, color: '#6b7280' };
  }
}

function formatTime(timestamp) {
  const date = new Date(timestamp * 1000);
  return date.toLocaleString();
}
