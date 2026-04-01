#!/bin/sh
# =============================================================
# Patch index.html for MeshNode Indonesia
# - Center map on Indonesia (-2.5, 118) at zoom 5
# - Restrict map bounds to Indonesian region
# - Update branding to MeshNode Indonesia
# =============================================================

INDEX="/usr/share/nginx/html/index.html"

# Helper: verify a pattern exists in index.html before patching
assert_pattern() {
  if ! grep -q "$1" "$INDEX"; then
    echo "[FATAL] Pattern not found in index.html — upstream may have changed: $1" >&2
    exit 1
  fi
}

# Validate all required patterns exist before making any changes
echo "[patch] Validating upstream index.html patterns..."
assert_pattern "center: window.localStorage.getItem('center')"
assert_pattern "zoom: window.localStorage.getItem('zoom')"
assert_pattern "worldCopyJump: true,"
assert_pattern '<title>MeshMap'
assert_pattern 'content="A nearly live map'
assert_pattern 'meshmap.net/'
assert_pattern '<div id="header">'
assert_pattern 'let lastActiveNode'
assert_pattern 'const toggleDark ='
assert_pattern 'const updateNodes = data =>'
assert_pattern 'const lastSeen = Math.max'
assert_pattern 'Meshtastic MQTT'
echo "[patch] All patterns validated OK"

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

# 6. Add UI Checkboxes for Node Filtering (Online/Offline)
sed -i '/<div id="header">/a \  <div style="font-size: 13px; display: flex; gap: 8px;">\n    <label style="cursor: pointer;"><input type="checkbox" id="filterOnline" onchange="toggleFilter()" checked> <span id="labelOn">🟢 Online</span></label>\n    <label style="cursor: pointer;"><input type="checkbox" id="filterOffline" onchange="toggleFilter()"> <span id="labelOff">🔴 Offline</span></label>\n  </div>' "$INDEX"

# Insert toggle function and state variable
sed -i '/let lastActiveNode/a \  let filterOnline = window.localStorage.getItem("filterOnline") !== "false";\n  let filterOffline = window.localStorage.getItem("filterOffline") === "true";' "$INDEX"
sed -i '/const toggleDark =/i \  const toggleFilter = () => {\n    filterOnline = document.getElementById("filterOnline").checked;\n    filterOffline = document.getElementById("filterOffline").checked;\n    window.localStorage.setItem("filterOnline", filterOnline);\n    window.localStorage.setItem("filterOffline", filterOffline);\n    if (window.meshmapData) updateNodes(window.meshmapData);\n  }' "$INDEX"

# Insert logic to skip nodes based on the checkboxes selection
sed -i 's/const updateNodes = data => {/const updateNodes = data => {\n    window.meshmapData = data;\n    let cOn=0, cOff=0;\n    Object.values(data).forEach(n=>{const ls=Math.max(...Object.values(n.seenBy)); if((Date.now()\/1000-ls)>43200)cOff++; else cOn++;});\n    const elOn=document.getElementById("labelOn"); if(elOn) elOn.innerText=`🟢 Online (${cOn})`;\n    const elOff=document.getElementById("labelOff"); if(elOff) elOff.innerText=`🔴 Offline (${cOff})`;/g' "$INDEX"
sed -i 's/const lastSeen = Math.max(...Object.values(seenBy))/const lastSeen = Math.max(...Object.values(seenBy))\n      const isOffline = (Date.now() \/ 1000 - lastSeen) > 43200;\n      if (isOffline \&\& !filterOffline) return;\n      if (!isOffline \&\& !filterOnline) return;/g' "$INDEX"

# Patch opacity: online nodes fade 100%→25% over 12h, offline nodes always 100%
sed -i 's|const opacity = 1.0 - (Date.now() / 1000 - lastSeen) / 43200|const opacity = isOffline ? 1.0 : Math.max(0.25, 1.0 - (Date.now() / 1000 - lastSeen) / 43200)|g' "$INDEX"

# Make checkbox reflect localStorage state when page loads
sed -i '/const toggleDark =/i \  document.addEventListener("DOMContentLoaded", () => {\n    const cbOn = document.getElementById("filterOnline");\n    if (cbOn) cbOn.checked = filterOnline;\n    const cbOff = document.getElementById("filterOffline");\n    if (cbOff) cbOff.checked = filterOffline;\n  });' "$INDEX"

echo "[patch] Dual checkbox filtering feature added."

# 7. Add Welcome Modal
cat << 'EOF' >> "$INDEX"
<div id="welcomeOverlay" style="display:none; position:fixed; top:0; left:0; width:100%; height:100%; background:#f4f6f8; z-index:9999; justify-content:center; align-items:center; overflow-y:auto; padding: 15px; font-family: Inter,sans-serif;">
  <div style="background:#fff; color:#333; max-width:600px; padding:20px; border-radius:10px; box-shadow:0 4px 20px rgba(0,0,0,0.15); line-height:1.4; text-align:left; position:relative; margin:auto; font-size: 13px;">
    <h2 style="margin-top:0; color:#2c3e50; font-size: 18px; border-bottom: 1px solid #eee; padding-bottom: 8px; margin-bottom: 10px;">📻 Selamat Datang di MeshNode Indonesia!</h2>
    <p style="margin-top: 0; margin-bottom: 8px;">Selamat datang bagi teman-teman yang baru bergabung. Grup ini dibentuk untuk menguatkan jaringan <i>mesh chat</i> di Indonesia. Di sinilah kita bersatu dalam komunitas MeshNode Indonesia—sebuah wadah bagi kita yang ingin bisa berkomunikasi tanpa kuota internet, saling terkoneksi, dan saling menguatkan.</p>
    <p style="margin-top: 0; margin-bottom: 8px;">Sebagai catatan, grup ini bukan berisi para <i>expert</i>; kita semua di sini sama-sama masih belajar. Silakan ajukan pertanyaan sesederhana apa pun, karena anggota grup wajib dan pasti akan sedia membantu. Ingat, <b style="color: #0a84ff;">"Malu bertanya, sesat di udara"</b>.</p>
 
    <h3 style="color:#2c3e50; font-size: 15px; margin-top: 15px; margin-bottom: 8px;">📡 Standar & Frekuensi Kita</h3>
    <p style="margin-top: 0; margin-bottom: 8px;">Frekuensi yang digunakan adalah <b>920-923 MHz</b>, sesuai alokasi pita frekuensi radio yang ditetapkan pada Keputusan Menteri Kominfo RI Nomor 260 Tahun 2024 tentang Standar Teknis <i>Short Range Devices</i> (SRD), Halaman 25 Nomor 34.</p>
    <p style="margin-top: 0; margin-bottom: 5px;">Bagi yang sudah <i>online</i>, kita adakan <b>Absen Jaringan setiap hari antara jam 18:00 - 19:00 WIB</b> via Meshtastic chat di <i style="color: #0a84ff;">Primary Channel</i> dengan settingan berikut:</p>
    <ul style="margin-top:0; margin-bottom: 15px; padding-left:20px; background: #f8f9fa; padding: 10px 10px 10px 25px; border-radius: 6px;">
      <li style="margin-bottom: 3px;"><b>LoRa Region:</b> <code>SG_923</code></li>
      <li style="margin-bottom: 3px;"><b>LoRa Preset:</b> <code>Long Range / Fast</code></li>
      <li style="margin-bottom: 3px;"><b>LoRa Frequency Override:</b> <code>923</code></li>
      <li style="margin-bottom: 0;"><b>MQTT Root Topic:</b> <code>msh/ID_923</code></li>
    </ul>

    <h3 style="color:#2c3e50; font-size: 15px; margin-top: 15px; margin-bottom: 8px;">🌐 Informasi & Komunitas</h3>
    <ul style="margin-top:0; padding-left:20px; list-style-type:none; padding: 0;">
      <li style="margin-bottom: 5px;">🌍 <b>Website:</b> <a href="https://www.meshnode.id/" target="_blank" style="color: #0a84ff; text-decoration: none;">www.meshnode.id</a></li>
      <li style="margin-bottom: 5px;">🐦 <b>X (Twitter):</b> <a href="https://x.com/meshnodeid" target="_blank" style="color: #0a84ff; text-decoration: none;">@meshnodeid</a></li>
      <li style="margin-bottom: 5px;">📸 <b>Instagram:</b> <a href="https://instagram.com/meshnodeid" target="_blank" style="color: #0a84ff; text-decoration: none;">@meshnodeid</a></li>
      <li style="margin-bottom: 5px;">📘 <b>Facebook:</b> <a href="https://facebook.com/meshnodeid" target="_blank" style="color: #0a84ff; text-decoration: none;">MeshNode Indonesia</a></li>
      <li style="margin-bottom: 0;">💬 <b>Discord:</b> <a href="https://discord.gg/aeAj2SCwQ" target="_blank" style="color: #0a84ff; text-decoration: none;">https://discord.gg/aeAj2SCwQ</a></li>
    </ul>
    
    <div style="text-align:center; margin-top:20px;">
      <button onclick="closeWelcome()" style="background:#0a84ff; color:white; border:none; padding:8px 20px; font-size:14px; border-radius:6px; cursor:pointer; font-weight:bold; box-shadow: 0 4px 6px rgba(10, 132, 255, 0.3); transition: 0.2s;">Saya Mengerti, Mulai!</button>
    </div>
  </div>
</div>
<script>
  function closeWelcome() {
    document.getElementById("welcomeOverlay").style.display = "none";
    document.getElementById("map").style.visibility = "visible";
    document.getElementById("header").style.visibility = "visible";
    window.localStorage.setItem("welcomeSeen", "true");
  }
  document.addEventListener("DOMContentLoaded", () => {
    if (!window.localStorage.getItem("welcomeSeen")) {
      document.getElementById("map").style.visibility = "hidden";
      document.getElementById("header").style.visibility = "hidden";
      document.getElementById("welcomeOverlay").style.display = "flex";
    }
  });
</script>
EOF

# 8. Add Header Social Links
# Replace original github and faq buttons with MeshNode social links
apk add --no-cache perl
perl -0pi -e 's|<div>\s*<a href="https://github.com/brianshea2/meshmap.net\?tab=readme-ov-file#faqs".*?</div>\s*<div>\s*<a href="https://github.com/brianshea2/meshmap.net".*?</div>|<div><a href="https://www.meshnode.id/" target="_blank" title="Website"><i class="fa fa-globe fa-lg"></i></a></div><div><a href="https://x.com/meshnodeid" target="_blank" title="X (Twitter)"><i class="fa fa-twitter fa-lg"></i></a></div><div><a href="https://instagram.com/meshnodeid" target="_blank" title="Instagram"><i class="fa fa-instagram fa-lg"></i></a></div><div><a href="https://facebook.com/meshnodeid" target="_blank" title="Facebook"><i class="fa fa-facebook-official fa-lg"></i></a></div><div><a href="https://discord.gg/aeAj2SCwQ" target="_blank" title="Discord (Chat Channel)"><i class="fa fa-comments fa-lg"></i></a></div>|s' "$INDEX"
apk del perl

# 9. Improve Geolocation (Crosshairs icon) to use real GPS instead of IP
# sed -i 's|title: .Center map to current IP geolocation.,|title: "Center map to your GPS location",|g' "$INDEX"
# sed -i 's|fetch(`https://ipinfo.io/json?token=${ipinfoToken}`)|map.locate({setView: true, maxZoom: 14}); //|g' "$INDEX"
# sed -i 's|\.then(r => r\.json())|//|g' "$INDEX"
# sed -i 's|\.then(({loc}) => loc && map\.flyTo(loc\.split('\'','\''), 10))|//|g' "$INDEX"
# sed -i "s|\.catch(e => console\.error('Failed to set location:', e))|//|g" "$INDEX"

echo "[patch] Welcome Modal & Social Headers added."

# 10. Update Leaflet Map Attribution
sed -i 's|"https://meshtastic.org/docs/software/integrations/mqtt/#public-mqtt-server">Meshtastic MQTT|"https://www.meshnode.id/">MeshNode Indonesia / ID_923 Network|g' "$INDEX"

echo "[patch] Leaflet map attribution updated"
