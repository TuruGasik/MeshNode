// ── State ──
const mapOnly =
  new URLSearchParams(window.location.search).get("mapOnly") === "true";
let lastActiveNode;
let filterOnline = window.localStorage.getItem("filterOnline") !== "false";
let filterOffline = window.localStorage.getItem("filterOffline") === "true";
let markersByNode = {};
let markerCacheByNode = {};
let markerSignatureByNode = {};
let lastSeenByNode = {};
let detailDrawersByNode = {};
let nodesBySearchString = {};
let filterCounts = { online: 0, offline: 0 };
const ipinfoToken = "aeb066758afd49";
const updateInterval = 65000;
let drawPendingOnVisible = false;

// ── Utilities ──
const html = (str) =>
  String(str ?? "")
    .replace(
      /[\x00-\x1F]/g,
      (c) =>
        `\\x${c.charCodeAt(0).toString(16).toUpperCase().padStart(2, "0")}`,
    )
    .replace(/["&<>]/g, (c) => `&#${c.charCodeAt(0)};`);

const duration = (d) => {
  let s = "";
  if (d >= 86400) {
    s += `${Math.floor(d / 86400)}d `;
    d %= 86400;
  }
  if (d >= 3600) {
    s += `${Math.floor(d / 3600)}h `;
    d %= 3600;
  }
  if (d >= 60) s += `${Math.floor(d / 60)}m`;
  else s += `${Math.max(0, Math.floor(d))}s`;
  return s;
};

const since = (t) => duration(Date.now() / 1000 - t);

function setDarkMode(enabled) {
  document.body.classList.toggle("dark", enabled);
  document.querySelector('meta[name="theme-color"]').content = enabled
    ? "#0f172a"
    : "#fff";
  if (enabled) window.localStorage.setItem("theme", "dark");
  else window.localStorage.removeItem("theme");
  return enabled;
}

function toggleDark() {
  return setDarkMode(!document.body.classList.contains("dark"));
}

// ── Online / Offline filter ──
function toggleFilter() {
  filterOnline = document.getElementById("filterOnline").checked;
  filterOffline = document.getElementById("filterOffline").checked;
  window.localStorage.setItem("filterOnline", filterOnline);
  window.localStorage.setItem("filterOffline", filterOffline);
  if (window.meshmapData) updateNodes(window.meshmapData);
}

function updateFilterLabels(
  cOn = filterCounts.online,
  cOff = filterCounts.offline,
) {
  filterCounts = { online: cOn, offline: cOff };
  const compact = window.matchMedia("(max-width: 560px)").matches;
  const elOn = document.getElementById("labelOn");
  if (elOn) elOn.innerText = compact ? `🔵 ${cOn}` : `🔵 Online (${cOn})`;
  const elOff = document.getElementById("labelOff");
  if (elOff) elOff.innerText = compact ? `🔴 ${cOff}` : `🔴 Offline (${cOff})`;
}

// ── About modal ──
function openAboutModal() {
  if (mapOnly) return;
  const m = document.getElementById("aboutModal");
  m.style.display = "block";
  requestAnimationFrame(() =>
    requestAnimationFrame(() => m.classList.add("am-open")),
  );
}

function closeAboutModal() {
  const m = document.getElementById("aboutModal");
  m.classList.remove("am-open");
  setTimeout(() => {
    m.style.display = "none";
  }, 300);
  window.localStorage.setItem("welcomeSeen", "true");
}

// ── Node Database modal ──
let ndFilter = "all",
  ndSearchTimer = null;

function openNodeDbModal() {
  if (mapOnly) return;
  const m = document.getElementById("nodeDbModal");
  m.style.display = "block";
  requestAnimationFrame(() =>
    requestAnimationFrame(() => m.classList.add("nd-open")),
  );
  document.getElementById("ndSearchInput").value = "";
  document.getElementById("ndList").scrollTop = 0;
  document.getElementById("nodeDbModal-content").scrollTop = 0;
  ndFetchNodes(true);
}

function closeNodeDbModal() {
  const m = document.getElementById("nodeDbModal");
  m.classList.remove("nd-open");
  setTimeout(() => {
    m.style.display = "none";
  }, 300);
}

function ndSetFilter(f) {
  ndFilter = f;
  document
    .querySelectorAll(".nd-filter-btn")
    .forEach((b) => b.classList.toggle("active", b.dataset.filter === f));
  document.getElementById("ndSearchInput").value = "";
  document.getElementById("ndList").scrollTop = 0;
  document.getElementById("nodeDbModal-content").scrollTop = 0;
  ndFetchNodes(true);
}

function ndSearch() {
  clearTimeout(ndSearchTimer);
  ndSearchTimer = setTimeout(() => {
    document.getElementById("ndList").scrollTop = 0;
    document.getElementById("nodeDbModal-content").scrollTop = 0;
    const q = document.getElementById("ndSearchInput").value.trim();
    if (q.length > 0) ndFetchSearch(q, true);
    else ndFetchNodes(true);
  }, 300);
}

function ndFetchNodes(clear) {
  const url = "/api/nodes/all?limit=500";
  fetch(url)
    .then((r) => r.json())
    .then((nodes) => {
      ndRender(nodes, clear);
    })
    .catch(() => {});
}

function ndFetchSearch(q, clear) {
  fetch(`/api/nodes/search?q=${encodeURIComponent(q)}&limit=200`)
    .then((r) => r.json())
    .then((nodes) => {
      ndRender(nodes, clear);
    })
    .catch(() => {});
}

function ndRender(nodes, clear) {
  const tbody = document.getElementById("ndBody");
  if (clear) tbody.innerHTML = "";
  const now = Date.now() / 1000;
  const counts = {
    all: 0,
    online: 0,
    offline: 0,
    dead: 0,
    with_position: 0,
    no_position: 0,
  };
  nodes.forEach((n) => {
    const age = now - n.lastSeen;
    const isDead = age > 86400;
    const isOff = age > 21600;
    counts.all++;
    if (n.hasPosition) counts.with_position++;
    else counts.no_position++;
    if (isDead) counts.dead++;
    else if (isOff) counts.offline++;
    else counts.online++;
  });
  const statLabels = {
    all: `Total: ${counts.all} | Online: ${counts.online} | Offline: ${counts.offline}`,
    online: `Online: ${counts.online}`,
    offline: `Offline: ${counts.offline}`,
    dead: `Dead: ${counts.dead}`,
    with_position: `Punya posisi: ${counts.with_position}`,
    no_position: `Tanpa posisi: ${counts.no_position}`,
  };
  document.getElementById("ndStats").textContent =
    statLabels[ndFilter] ?? statLabels.all;
  nodes.forEach((n) => {
    const age = now - n.lastSeen;
    const isDead = age > 86400; // 24 hours
    const isOff = age > 21600; // 6 hours
    // Client-side filter
    if (ndFilter === "online" && isOff) return;
    if (ndFilter === "offline" && (!isOff || isDead)) return;
    if (ndFilter === "dead" && !isDead) return;
    if (ndFilter === "with_position" && !n.hasPosition) return;
    if (ndFilter === "no_position" && n.hasPosition) return;
    const tr = document.createElement("tr");
    const ago = since(n.lastSeen);
    const statusBadge = !n.hasPosition
      ? '<span class="nd-badge nd-badge-nomap">No Map</span>'
      : isDead
        ? '<span class="nd-badge nd-badge-dead">Dead</span>'
        : isOff
          ? '<span class="nd-badge nd-badge-off">Offline</span>'
          : '<span class="nd-badge nd-badge-on">Online</span>';
    const safeLong = html(n.longName || "\u2014");
    const safeShort = html(n.shortName || "\u2014");
    tr.innerHTML = `
      <td style="font-family:monospace;font-size:12px;">${html(n.hexId)}</td>
      <td style="font-family:monospace;font-size:12px;">${safeShort}</td>
      <td><b>${safeLong}</b></td>
      <td class="nd-hide-sm">${html(n.hwModel || "\u2014")}</td>
      <td class="nd-hide-sm">${html(n.region || "\u2014")}</td>
      <td>${statusBadge}</td>
      <td style="white-space:nowrap;">${ago}</td>
    `;
    if (n.hasPosition) {
      tr.onclick = () => {
        closeNodeDbModal();
        if (markersByNode[n.nodeNum]) {
          showNode(n.nodeNum);
        } else {
          map.flyTo([n.latitude, n.longitude], 12);
        }
      };
    }
    tbody.appendChild(tr);
  });
}

// ── Map init (runs after Leaflet libs are loaded) ──
const map = L.map("map", {
  center: window.localStorage.getItem("center")?.split(",") ?? [-2.5, 118],
  zoom: window.localStorage.getItem("zoom") ?? 5,
  attributionControl: false,
  zoomControl: false,
  worldCopyJump: true,
  maxBounds: [
    [-15, 90],
    [12, 145],
  ],
  maxBoundsViscosity: 0.8,
  minZoom: 4,
});

L.tileLayer("https://tile.openstreetmap.org/{z}/{x}/{y}.png", {
  attribution:
    'Map tiles from <a href="https://www.openstreetmap.org/copyright">OpenStreetMap</a>',
  maxZoom: 17,
}).addTo(map);

setDarkMode(window.localStorage.getItem("theme") === "dark");

const markers = L.markerClusterGroup({
  maxClusterRadius: (zoom) => (zoom < 3 ? 40 : 30),
  chunkedLoading: true,
  chunkInterval: 50,
  chunkDelay: 25,
}).addTo(map);

const detailsLayer = L.layerGroup().addTo(map);
map.on("click", () => detailsLayer.clearLayers());

map.addControl(
  new L.Control.Search({
    layer: markers,
    propertyName: "searchString",
    initial: false,
    position: "topleft",
    marker: false,
    moveToLocation: (_, s) => showNode(nodesBySearchString[s]),
  }),
);

L.control.zoom({ position: "topright" }).addTo(map);

L.easyButton({
  position: "topright",
  states: [
    {
      stateName: "geolocation-button",
      title: "Center map to current IP geolocation",
      icon: "fa-crosshairs fa-lg",
      onClick: () => {
        fetch(`https://ipinfo.io/json?token=${ipinfoToken}`)
          .then((r) => r.json())
          .then(({ loc }) => loc && map.flyTo(loc.split(","), 10))
          .catch((e) => console.error("Failed to set location:", e));
      },
    },
  ],
}).addTo(map);

const attribution = L.control
  .attribution({ position: "bottomright", prefix: false })
  .addTo(map);

const updateNodeCount = (count) =>
  attribution.setPrefix(`
  ${count.toLocaleString()} nodes from
  <a href="https://www.meshnode.id/">MeshNode Indonesia / ID Network</a>
`);

map.on("moveend", () => {
  const center = map.getCenter();
  window.localStorage.setItem("center", [center.lat, center.lng].join(","));
});
map.on("zoomend", () => {
  window.localStorage.setItem("zoom", map.getZoom());
});

const nodeLink = (num, label) =>
  `<a href="#${num}" onclick="showNode(${num});return false;">${html(label)}</a>`;

const getLastSeen = (node) => {
  const seenValues = Object.values(node?.seenBy ?? {}).filter(Number.isFinite);
  return seenValues.length ? Math.max(...seenValues) : 0;
};

const pulseNodeUpdate = (position, markerColor) => {
  const pulse = L.circleMarker(position, {
    radius: 12,
    color: markerColor,
    weight: 2,
    opacity: 0.55,
    fillColor: markerColor,
    fillOpacity: 0.22,
    interactive: false,
    pane: "markerPane",
  }).addTo(map);
  let frame = 0;
  const maxFrames = 45;
  const animate = () => {
    frame += 1;
    const progress = frame / maxFrames;
    pulse.setStyle({
      opacity: 0.55 * (1 - progress),
      fillOpacity: 0.22 * (1 - progress),
    });
    pulse.setRadius(12 + 26 * progress);
    if (frame < maxFrames) requestAnimationFrame(animate);
    else map.removeLayer(pulse);
  };
  requestAnimationFrame(animate);
};

const getVisibleNodeIds = (data, hiddenNodes, now) => {
  const visibleNodeIds = new Set();
  Object.entries(data).forEach(([nodeNum, node]) => {
    if (hiddenNodes.has(nodeNum)) return;
    const age = now - getLastSeen(node);
    const isOffline = age > 21600; // 6 hours
    const isDead = age > 86400; // 24 hours
    if (isDead) return;
    if (isOffline && !filterOffline) return;
    if (!isOffline && !filterOnline) return;
    visibleNodeIds.add(nodeNum);
  });
  return visibleNodeIds;
};

const buildRelationsByNode = (data, hiddenNodes) => {
  const relationsByNode = {};
  Object.entries(data).forEach(([nodeNum, node]) => {
    if (hiddenNodes.has(nodeNum)) return;
    const { neighbors, seenBy } = node;
    const seenByNums = new Set(
      Object.keys(seenBy)
        .map((topic) => topic.match(/\/!([0-9a-f]+)$/))
        .filter((match) => match)
        .map((match) => parseInt(match[1], 16)),
    );
    relationsByNode[nodeNum] ??= {};
    seenByNums.forEach((seenByNum) => {
      relationsByNode[seenByNum] ??= {};
      const relationObj =
        relationsByNode[nodeNum][seenByNum] ??
        relationsByNode[seenByNum][nodeNum] ??
        {};
      relationsByNode[nodeNum][seenByNum] = relationObj;
      relationsByNode[seenByNum][nodeNum] = relationObj;
      relationObj.mqtt = true;
    });
    if (neighbors) {
      Object.keys(neighbors).forEach((neighborNum) => {
        relationsByNode[neighborNum] ??= {};
        const relationObj =
          relationsByNode[nodeNum][neighborNum] ??
          relationsByNode[neighborNum][nodeNum] ??
          {};
        relationsByNode[nodeNum][neighborNum] = relationObj;
        relationsByNode[neighborNum][nodeNum] = relationObj;
        relationObj.neighbor = true;
      });
    }
  });
  return relationsByNode;
};

const buildMarkerSignature = (node, markerColor, precisionCorners) =>
  JSON.stringify({
    markerColor,
    precisionCorners,
    longName: node.longName,
    shortName: node.shortName,
    hwModel: node.hwModel,
    role: node.role,
    fwVersion: node.fwVersion,
    region: node.region,
    modemPreset: node.modemPreset,
    onlineLocalNodes: node.onlineLocalNodes,
    latitude: node.latitude,
    longitude: node.longitude,
    altitude: node.altitude,
    precision: node.precision,
    batteryLevel: node.batteryLevel,
    voltage: node.voltage,
    chUtil: node.chUtil,
    airUtilTx: node.airUtilTx,
    uptime: node.uptime,
    temperature: node.temperature,
    relativeHumidity: node.relativeHumidity,
    barometricPressure: node.barometricPressure,
    lux: node.lux,
    windDirection: node.windDirection,
    windSpeed: node.windSpeed,
    windGust: node.windGust,
    radiation: node.radiation,
    rainfall1: node.rainfall1,
    rainfall24: node.rainfall24,
    neighbors: node.neighbors,
    seenBy: node.seenBy,
  });

// ── Update node markers ──
const updateNodes = (data) => {
  window.meshmapData = data;
  const now = Date.now() / 1000;
  let cOn = 0,
    cOff = 0;
  Object.values(data).forEach((n) => {
    const ls = getLastSeen(n);
    const age = now - ls;
    if (age > 86400) return;
    if (age > 21600) cOff++;
    else cOn++;
  });
  updateFilterLabels(cOn, cOff);

  // Dedup: hide dead nodes if another node with the same name is online
  const hiddenNodes = new Set();
  const byName = {};
  Object.entries(data).forEach(([num, n]) => {
    if (!n.longName) return;
    (byName[n.longName] ??= []).push({
      num,
      ls: getLastSeen(n),
    });
  });
  const now2 = Date.now() / 1000;
  Object.values(byName).forEach((group) => {
    if (group.length < 2) return;
    const hasOnline = group.some((g) => now2 - g.ls <= 21600);
    if (hasOnline)
      group.forEach((g) => {
        if (now2 - g.ls > 86400) hiddenNodes.add(g.num);
      });
  });

  const popupWasOpen =
    lastActiveNode && markersByNode[lastActiveNode]?.isPopupOpen();
  const detailsLayerWasPopulated = detailsLayer.getLayers().length > 0;
  nodesBySearchString = {};
  const nextMarkersByNode = {};
  let reactivate = () => {};
  const relationsByNode = buildRelationsByNode(data, hiddenNodes);
  const visibleNodeIds = getVisibleNodeIds(data, hiddenNodes, now);
  const markersToAdd = [];

  Object.entries(data).forEach(([nodeNum, node]) => {
    if (!visibleNodeIds.has(nodeNum)) return;
    const {
      longName,
      shortName,
      hwModel,
      role,
      fwVersion,
      region,
      modemPreset,
      hasDefaultCh,
      onlineLocalNodes,
      latitude,
      longitude,
      altitude,
      precision,
      batteryLevel,
      voltage,
      chUtil,
      airUtilTx,
      uptime,
      temperature,
      relativeHumidity,
      barometricPressure,
      lux,
      windDirection,
      windSpeed,
      windGust,
      radiation,
      rainfall1,
      rainfall24,
      neighbors,
      seenBy,
    } = node;
    const id = `!${Number(nodeNum).toString(16)}`;
    const position = [latitude, longitude].map((x) => x / 10000000);
    let precisionCorners;
    let precisionCenter;
    let precisionRadius;
    if (precision && precision > 0 && precision < 32) {
      const [latLo, latHi] = [
        latitude & (0xffffffff << (32 - precision)),
        latitude | (0xffffffff >>> precision),
      ].sort((a, b) => a - b);
      const [longLo, longHi] = [
        longitude & (0xffffffff << (32 - precision)),
        longitude | (0xffffffff >>> precision),
      ].sort((a, b) => a - b);
      precisionCorners = [
        [latLo, longLo].map((x) => x / 10000000),
        [latHi, longHi].map((x) => x / 10000000),
      ];
      precisionCenter = [
        (precisionCorners[0][0] + precisionCorners[1][0]) / 2,
        (precisionCorners[0][1] + precisionCorners[1][1]) / 2,
      ];
      precisionRadius = Math.round(
        Math.max(
          map.distance(precisionCenter, precisionCorners[0]),
          map.distance(precisionCenter, precisionCorners[1]),
        ),
      );
    }
    const drawNodeDetails = () => {
      detailsLayer.clearLayers();
      if (precisionCenter && precisionRadius) {
        L.circle(precisionCenter, {
          radius: precisionRadius,
          color: "#ffa932",
          weight: 3,
          opacity: 1,
          fillColor: "#ffa932",
          fillOpacity: 0.2,
        }).addTo(detailsLayer);
      }
      Object.entries(relationsByNode[nodeNum] ?? {}).forEach(
        ([relatedNum, relationObj]) => {
          if (data[relatedNum] === undefined) return;
          const relatedPosition = [
            data[relatedNum].latitude,
            data[relatedNum].longitude,
          ].map((x) => x / 10000000);
          const relationContent = `
          <table><tbody>
          <tr><th>Related node</th><td>${nodeLink(relatedNum, `!${Number(relatedNum).toString(16)}`)}</td></tr>
          <tr><th>Relation type</th><td>${[relationObj.neighbor && "Neighbor", relationObj.mqtt && "MQTT uplink"].filter((v) => v).join(", ")}</td></tr>
          <tr><th>Distance</th><td>${Math.round(map.distance(position, relatedPosition)).toLocaleString()} m</td></tr>
          ${neighbors?.[relatedNum]?.snr ? `<tr><th>SNR</th><td>${neighbors[relatedNum].snr} dB</td></tr>` : ""}
          </tbody></table>
        `;
          L.polyline([position, relatedPosition])
            .bindTooltip(relationContent, { opacity: 0.95, sticky: true })
            .on("click", () => showNode(relatedNum))
            .addTo(detailsLayer);
        },
      );
    };
    detailDrawersByNode[nodeNum] = drawNodeDetails;
    const lastSeen = getLastSeen(node);
    const age = now - lastSeen;
    const isOffline = age > 21600; // 6 hours
    const markerColor = isOffline ? "#ef4444" : "#3b82f6";
    if (lastSeenByNode[nodeNum] && lastSeen > lastSeenByNode[nodeNum]) {
      pulseNodeUpdate(position, markerColor);
    }
    lastSeenByNode[nodeNum] = lastSeen;
    const searchString = `${longName} (${shortName}) ${id}`;
    nodesBySearchString[searchString] = nodeNum;
    const signature = buildMarkerSignature(node, markerColor, precisionCorners);
    let marker = markerCacheByNode[nodeNum];
    if (!marker) {
      marker = L.circleMarker(position, {
        radius: 7,
        fillColor: markerColor,
        color: "#fff",
        weight: 2,
        opacity: 1,
        fillOpacity: 0.85,
        searchString,
      });
      markerCacheByNode[nodeNum] = marker;
      markerSignatureByNode[nodeNum] = null;
    }
    if (markerSignatureByNode[nodeNum] !== signature) {
      const tooltipContent = `${html(longName)} (${html(shortName)}) ${since(lastSeen)}`;
      const popupContent = `
        <div class="title">${html(longName)} (${html(shortName)})</div>
        <div>${nodeLink(nodeNum, id)} | ${html(role)} | ${html(hwModel)}</div>
        <table><tbody>
        ${uptime ? `<tr><th>Uptime</th><td>${duration(uptime)}</td></tr>` : ""}
        ${
          batteryLevel
            ? `<tr><th>Power</th><td>${batteryLevel > 100 ? "Plugged in" : `${batteryLevel}%`}` +
              `${voltage ? ` (${voltage.toFixed(2)}V)` : ""}</td></tr>`
            : ""
        }
        ${fwVersion ? `<tr><th>Firmware</th><td>${html(fwVersion)}</td></tr>` : ""}
        ${region ? `<tr><th>LoRa config</th><td>${html(region)} / ${html(modemPreset)}</td></tr>` : ""}
        ${chUtil ? `<tr><th>ChUtil</th><td>${chUtil.toFixed(2)}%</td></tr>` : ""}
        ${airUtilTx ? `<tr><th>AirUtilTX</th><td>${airUtilTx.toFixed(2)}%</td></tr>` : ""}
        ${onlineLocalNodes ? `<tr><th>Local nodes</th><td>${onlineLocalNodes}</td></tr>` : ""}
        ${
          precisionRadius
            ? `<tr><th>Map precision</th><td>` +
              `&#177;${precisionRadius.toLocaleString()} m` +
              ` (orange circle)</td></tr>`
            : ""
        }
        ${altitude ? `<tr><th>Altitude</th><td>${altitude.toLocaleString()} m above MSL</td></tr>` : ""}
        ${
          temperature
            ? `<tr><th>Temperature</th><td>${temperature.toFixed(1)}&#8451; / ` +
              `${(temperature * 1.8 + 32).toFixed(1)}&#8457;</td></tr>`
            : ""
        }
        ${relativeHumidity ? `<tr><th>Relative humidity</th><td>${Math.round(relativeHumidity)}%</td></tr>` : ""}
        ${barometricPressure ? `<tr><th>Barometric pressure</th><td>${Math.round(barometricPressure)} hPa</td></tr>` : ""}
        ${
          windDirection || windSpeed
            ? `<tr><th>Wind</th><td>` +
              (windDirection ? `${windDirection}&#176;` : "") +
              (windDirection && windSpeed ? " @ " : "") +
              (windSpeed ? `${(windSpeed * 3.6).toFixed(1)}` : "") +
              (windSpeed && windGust
                ? ` G ${(windGust * 3.6).toFixed(1)}`
                : "") +
              (windSpeed ? " km/h" : "") +
              `</td></tr>`
            : ""
        }
        ${lux ? `<tr><th>Lux</th><td>${Math.round(lux)} lx</td></tr>` : ""}
        ${radiation ? `<tr><th>Radiation</th><td>${radiation.toFixed(2)} µR/h</td></tr>` : ""}
        ${
          rainfall1 || rainfall24
            ? `<tr><th>Rainfall</th><td>` +
              (rainfall1 ? `${rainfall1.toFixed(2)} mm/h` : "") +
              (rainfall1 && rainfall24 ? ", " : "") +
              (rainfall24 ? `${rainfall24.toFixed(2)} mm/24h` : "") +
              `</td></tr>`
            : ""
        }
        </tbody></table>
        <table><thead>
        <tr><th>Last seen</th><th>via</th><th>MQTT root</th><th>channel</th></tr>
        </thead><tbody>
        ${Array.from(
          new Map(
            Object.entries(seenBy)
              .map(([topic, seen]) =>
                ((match) => ({
                  seen,
                  via: match[3] ?? id,
                  root: match[1],
                  chan: match[2],
                }))(
                  topic.match(
                    /^(.*)(?:\/2\/e\/(.*)\/(![0-9a-f]+)|\/2\/map\/)$/s,
                  ),
                ),
              )
              .sort((a, b) => a.seen - b.seen)
              .map((v) => [v.via, v]),
          ).values(),
          ({ seen, via, root, chan }) => `
            <tr>
            <td>${since(seen)}</td>
            <td>${chan ? ((n, l) => (data[n] ? nodeLink(n, l) : l))(parseInt(via.slice(1), 16), via === id ? "self" : via) : "MapReport"}</td>
            <td class="break">${html(root)}</td>
            <td class="break">${html(chan ?? "n/a")}</td>
            </tr>
          `,
        )
          .reverse()
          .join("")}
        </tbody></table>
      `;
      marker.setLatLng(position);
      marker.setStyle({ fillColor: markerColor });
      marker.options.searchString = searchString;
      marker.unbindTooltip().bindTooltip(tooltipContent, { opacity: 0.95 });
      marker.unbindPopup().bindPopup(popupContent, { maxWidth: 600 });
      marker.off("popupopen");
      marker.on("popupopen", () => {
        lastActiveNode = nodeNum;
        history.replaceState(null, "", `#${nodeNum}`);
        detailDrawersByNode[nodeNum]?.();
      });
      markerSignatureByNode[nodeNum] = signature;
    }
    nextMarkersByNode[nodeNum] = marker;
    if (!markers.hasLayer(marker)) markersToAdd.push(marker);
    if (nodeNum === lastActiveNode) {
      if (popupWasOpen) {
        reactivate = () => {
          const cluster = markers.getVisibleParent(marker);
          if (typeof cluster?.spiderfy === "function") cluster.spiderfy();
          marker.openPopup();
        };
      } else if (detailsLayerWasPopulated) {
        reactivate = drawNodeDetails;
      }
    }
  });

  Object.entries(markerCacheByNode).forEach(([nodeNum, marker]) => {
    if (visibleNodeIds.has(nodeNum)) return;
    if (markers.hasLayer(marker)) markers.removeLayer(marker);
    if (
      !data[nodeNum] ||
      hiddenNodes.has(nodeNum) ||
      now - getLastSeen(data[nodeNum]) > 86400
    ) {
      delete markerCacheByNode[nodeNum];
      delete markerSignatureByNode[nodeNum];
      delete lastSeenByNode[nodeNum];
      delete detailDrawersByNode[nodeNum];
    }
  });
  markersByNode = nextMarkersByNode;
  if (markersToAdd.length) markers.addLayers(markersToAdd);
  if (lastActiveNode && !markersByNode[lastActiveNode])
    detailsLayer.clearLayers();
  reactivate();
};

// ── Fetch loop ──
const drawMap = async () => {
  drawPendingOnVisible = false;
  try {
    await fetch("/api/nodes/map")
      .then((r) => r.json())
      .then(updateNodes);
    updateNodeCount(Object.keys(markersByNode).length);
  } catch (e) {
    console.error("Failed to update nodes:", e);
  }
  setTimeout(() => {
    if (document.hidden) {
      if (drawPendingOnVisible) return;
      drawPendingOnVisible = true;
      document.addEventListener(
        "visibilitychange",
        () => {
          if (!document.hidden) drawMap();
          else drawPendingOnVisible = false;
        },
        { once: true },
      );
    } else {
      drawMap();
    }
  }, updateInterval);
};

const showNode = (nodeNum) => {
  if (markersByNode[nodeNum] === undefined) return false;
  map.panTo(markersByNode[nodeNum].getLatLng());
  setTimeout(() => {
    markers.zoomToShowLayer(markersByNode[nodeNum], () => {
      markersByNode[nodeNum].openPopup();
    });
  }, 300);
  return true;
};

window.addEventListener("hashchange", () => {
  if (window.location.hash && !showNode(window.location.hash.slice(1))) {
    history.replaceState(null, "", window.location.pathname);
  }
  if (!window.location.hash) {
    map.closePopup();
    detailsLayer.clearLayers();
  }
});
map.on("popupclose", () => {
  if (window.location.hash)
    history.replaceState(null, "", window.location.pathname);
});

// ── Bootstrap ──
attribution.setPrefix("Loading node data&hellip;");
drawMap().then(() => {
  if (window.location.hash && !showNode(window.location.hash.slice(1))) {
    history.replaceState(null, "", window.location.pathname);
  }
});

// First visit → show About modal; restore dark mode & filter state
document.addEventListener("DOMContentLoaded", () => {
  updateFilterLabels();
  const cbOn = document.getElementById("filterOnline");
  if (cbOn) cbOn.checked = filterOnline;
  const cbOff = document.getElementById("filterOffline");
  if (cbOff) cbOff.checked = filterOffline;
  if (mapOnly) {
    document.getElementById("header").style.display = "none";
    document.getElementById("map").style.top = "0";
  }
  if (!mapOnly && !window.localStorage.getItem("welcomeSeen")) openAboutModal();
});

window.addEventListener("resize", () => updateFilterLabels());
