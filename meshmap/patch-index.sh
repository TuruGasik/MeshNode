#!/bin/sh
# =============================================================
# Patch index.html for MeshNode Indonesia
# - Center map on Indonesia (-2.5, 118) at zoom 5
# - Restrict map bounds to Indonesian region
# - Update branding to MeshNode Indonesia
# =============================================================

INDEX="/usr/share/nginx/html/index.html"

# 1. Change default center and zoom to Indonesia
sed -i "s|center: window.localStorage.getItem('center')?.split(',') ?? \[25, 0\]|center: window.localStorage.getItem('center')?.split(',') ?? [-2.5, 118]|" "$INDEX"
sed -i "s|zoom: window.localStorage.getItem('zoom') ?? 2|zoom: window.localStorage.getItem('zoom') ?? 5|" "$INDEX"

# 2. Add maxBounds to restrict panning to Indonesia region
#    Bounds cover: (-11, 94) to (8, 142) — all of Indonesia with some padding
sed -i "s|worldCopyJump: true,|worldCopyJump: true,\n    maxBounds: [[-15, 90], [12, 145]],\n    maxBoundsViscosity: 0.8,\n    minZoom: 4,|" "$INDEX"

# 3. Update page title
sed -i 's|<title>MeshMap - Meshtastic Node Map</title>|<title>MeshNode Indonesia - Peta Node Meshtastic</title>|' "$INDEX"

# 4. Update meta description
sed -i 's|content="A nearly live map of Meshtastic nodes seen by the official Meshtastic MQTT server"|content="Peta realtime node Meshtastic Indonesia - MeshNode Indonesia"|' "$INDEX"

# 5. Update header branding
sed -i 's|<a href="https://meshmap.net/" title="A nearly live map of Meshtastic nodes seen by the official Meshtastic MQTT server">MeshMap</a>|<a href="/" title="Peta realtime node Meshtastic Indonesia">MeshNode Indonesia</a>|' "$INDEX"

echo "[patch] index.html patched for MeshNode Indonesia"
