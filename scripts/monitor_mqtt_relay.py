#!/usr/bin/env python3
"""
MQTT Relay Monitor v2 - Monitor relay dedup service in real-time
Shows raw bridge inbound vs clean deduped vs outbound relay messages
with dedup stats.

Architecture (v2):
  📥 BRIDGE_IN : msh/bridge_in/ID/#  — raw from bridges (2x dups)
  📦 CLEAN     : msh/ID/#            — deduped by relay for clients
  📤 RELAYED   : msh/relay/ID/#      — outbound to bridges
"""

import threading
import sys
import os
import hashlib
from datetime import datetime
from collections import defaultdict
import base64
import struct

# Optional: pip install meshtastic cryptography
try:
    from meshtastic.protobuf import mesh_pb2, mqtt_pb2, telemetry_pb2
    _PROTO_AVAILABLE = True
except ImportError:
    try:
        from meshtastic import mesh_pb2, mqtt_pb2, telemetry_pb2
        _PROTO_AVAILABLE = True
    except ImportError:
        _PROTO_AVAILABLE = False

try:
    from cryptography.hazmat.primitives.ciphers import Cipher, algorithms, modes
    _CRYPTO_AVAILABLE = True
except ImportError:
    _CRYPTO_AVAILABLE = False

# Server config — credentials from environment variables.
# Load .env if exists
env_path = os.path.join(os.path.dirname(os.path.abspath(__file__)), "..", ".env")
if os.path.exists(env_path):
    with open(env_path) as f:
        for line in f:
            if "=" in line and not line.strip().startswith("#"):
                k, v = line.strip().split("=", 1)
                os.environ.setdefault(k, v)

BROKER = {
    "host": os.environ.get("MONITOR_LOCAL_HOST", "localhost"),
    "port": int(os.environ.get("MONITOR_LOCAL_PORT", "1883")),
    "user": os.environ.get("MONITOR_LOCAL_USER", os.environ.get("MQTT_USERNAME", "")),
    "pass": os.environ.get("MONITOR_LOCAL_PASS", os.environ.get("MQTT_PASSWORD", "")),
}

# Topics to monitor (v2 — 3 namespaces)
TOPICS = {
    "BRIDGE_IN": "msh/bridge_in/ID/#",  # Raw inbound from bridges (duplicated)
    "CLEAN":     "msh/ID/#",             # Deduped by relay → local clients
    "RELAYED":   "msh/relay/ID/#",       # Outbound to bridges
}

# Stats
stats_lock = threading.Lock()
stats = {
    "bridge_in": 0,
    "clean": 0,
    "relayed": 0,
}
per_subtopic = defaultdict(lambda: {"bridge_in": 0, "clean": 0, "relayed": 0})

# Channel PSK config — base64 PSK per channel name.
# AQ== = Meshtastic default key (0x01), expands to 1PG7OiApB1nwvP+rz05pAQ== padded to 32B.
CHANNEL_KEYS = {
    "LongFast":    "AQ==",
    "MeshNode_ID": "AQ==",
}
_DEFAULT_KEY_BYTES = base64.b64decode("1PG7OiApB1nwvP+rz05pAQ==")  # 16 bytes → AES-128

import paho.mqtt.client as paho_mqtt

# Portnum constants (meshtastic/protobuf/portnums_pb2.py)
_PORT_TEXT      = 1   # TEXT_MESSAGE_APP
_PORT_POSITION  = 3   # POSITION_APP
_PORT_NODEINFO  = 4   # NODEINFO_APP
_PORT_TELEMETRY = 67  # TELEMETRY_APP


def _expand_psk(psk_b64: str) -> bytes:
    """Expand PSK to actual key bytes. AQ== (0x01) → 16-byte default key."""
    raw = base64.b64decode(psk_b64)
    if len(raw) == 1 and raw[0] == 0x01:
        raw = _DEFAULT_KEY_BYTES  # 16 bytes, use as AES-128
    return raw  # 16 bytes = AES-128, 32 bytes = AES-256


def _aes_ctr_decrypt(data: bytes, packet_id: int, from_node: int, key: bytes):
    """AES-CTR decrypt. nonce = packet_id(8B LE) + from_node(4B LE) + 4 zero bytes."""
    if not _CRYPTO_AVAILABLE:
        return None
    try:
        nonce = struct.pack('<Q', packet_id) + struct.pack('<I', from_node) + b'\x00\x00\x00\x00'
        cipher = Cipher(algorithms.AES(key), modes.CTR(nonce))
        dec = cipher.decryptor()
        return dec.update(data) + dec.finalize()
    except Exception as e:
        return None


def _decode_data(data) -> str:
    try:
        port = data.portnum
        if port == _PORT_TEXT:
            return f"\U0001f4ac \"{data.payload.decode('utf-8', errors='replace')}\""
        elif port == _PORT_POSITION:
            pos = mesh_pb2.Position()
            pos.ParseFromString(data.payload)
            return f"\U0001f4cd {pos.latitude_i/1e7:.5f},{pos.longitude_i/1e7:.5f} alt={pos.altitude}m"
        elif port == _PORT_NODEINFO:
            user = mesh_pb2.User()
            user.ParseFromString(data.payload)
            return f"\U0001f464 {user.long_name} ({user.short_name})"
        elif port == _PORT_TELEMETRY:
            tel = telemetry_pb2.Telemetry()
            tel.ParseFromString(data.payload)
            if tel.HasField('device_metrics'):
                m = tel.device_metrics
                return f"\U0001f50b {m.battery_level}% {m.voltage:.2f}V ch={m.channel_utilization:.1f}% air={m.air_util_tx:.1f}%"
            if tel.HasField('environment_metrics'):
                m = tel.environment_metrics
                return f"\U0001f321\ufe0f {m.temperature:.1f}\u00b0C {m.relative_humidity:.1f}% {m.barometric_pressure:.1f}hPa"
            return "\U0001f4ca TELEMETRY"
        else:
            return f"[port={port} {len(data.payload)}B]"
    except Exception as e:
        return f"[decode err: {e}]"


def decode_payload(msg_topic: str, payload_bytes: bytes) -> str:
    """Decode a Meshtastic ServiceEnvelope payload into human-readable string."""
    if not _PROTO_AVAILABLE:
        return format_payload(payload_bytes)
    # /json/ topics are plain JSON, not protobuf ServiceEnvelope
    if "/json/" in msg_topic:
        try:
            return payload_bytes.decode("utf-8", errors="replace")[:120]
        except Exception:
            return format_payload(payload_bytes)
    try:
        envelope = mqtt_pb2.ServiceEnvelope()
        envelope.ParseFromString(payload_bytes)
        pkt = envelope.packet
    except Exception as e:
        return f"[proto parse err: {e}]"

    from_node = getattr(pkt, 'from')
    node_str = f"!{from_node:08x}"

    if pkt.HasField('encrypted'):
        subtopic = get_subtopic(msg_topic)
        parts = subtopic.split('/')
        channel = parts[2] if len(parts) > 2 else "LongFast"
        key = _expand_psk(CHANNEL_KEYS.get(channel, "AQ=="))
        plain = _aes_ctr_decrypt(bytes(pkt.encrypted), pkt.id, from_node, key)
        if plain is None:
            return f"{node_str} [decrypt failed]"
        try:
            data = mesh_pb2.Data()
            data.ParseFromString(plain)
            return f"{node_str} {_decode_data(data)}"
        except Exception as e:
            return f"{node_str} [decrypt ok, proto fail: {e}]"

    if pkt.HasField('decoded'):
        return f"{node_str} {_decode_data(pkt.decoded)}"

    return f"[no payload field]"



def short_hash(topic, payload_bytes):
    """Generate short hash for display (canonical topic, same algo as relay.py)"""
    # Normalize: strip bridge_in/ prefix for canonical hash
    canonical = topic.replace("bridge_in/", "").replace("relay/", "")
    h = hashlib.sha256()
    h.update(canonical.encode("utf-8"))
    h.update(payload_bytes)
    return h.hexdigest()[:12]


def format_payload(payload_bytes):
    """Format payload for display, handling binary data"""
    try:
        text = payload_bytes.decode("utf-8")
    except UnicodeDecodeError:
        text = f"<binary {len(payload_bytes)}B: {payload_bytes[:20].hex()}…>"
    if len(text) > 60:
        return f"{text[:60]}… ({len(payload_bytes)}B)"
    return text


def get_subtopic(topic):
    """Extract subtopic after the ID/ part"""
    # msh/bridge_in/ID/2/json/... → 2/json/...
    # msh/ID/2/json/...           → 2/json/...
    # msh/relay/ID/2/json/...     → 2/json/...
    if "ID/" in topic:
        return topic.split("ID/", 1)[1]
    return topic


def print_stats():
    """Print current stats summary"""
    with stats_lock:
        total_raw = stats["bridge_in"]
        total_clean = stats["clean"]
        total_relay = stats["relayed"]
        dedup_count = total_raw - total_clean
        if dedup_count < 0:
            dedup_count = 0
        dedup_pct = (dedup_count / total_raw * 100) if total_raw > 0 else 0

        print(f"\033[90m{'─'*70}\033[0m")
        print(
            f"\033[1m  📊 STATS │ "
            f"\033[93mRAW: {total_raw}\033[0m\033[1m │ "
            f"\033[96mCLEAN: {total_clean}\033[0m\033[1m │ "
            f"\033[92mOUT: {total_relay}\033[0m\033[1m │ "
            f"\033[91mDEDUP: {dedup_count} ({dedup_pct:.1f}%)\033[0m"
        )

        # Top subtopics
        if per_subtopic:
            sorted_topics = sorted(
                per_subtopic.items(),
                key=lambda x: x[1]["bridge_in"],
                reverse=True
            )[:5]
            print(f"\033[90m  ───────────────────────────────────────────────\033[0m")
            print(f"\033[1m  📋 TOP TOPICS:\033[0m")
            for sub, counts in sorted_topics:
                dedup = counts["bridge_in"] - counts["clean"]
                if dedup < 0:
                    dedup = 0
                short = sub[:35] + "…" if len(sub) > 35 else sub
                print(
                    f"     {short:37s} "
                    f"\033[93mraw:{counts['bridge_in']:>4}\033[0m "
                    f"\033[96mclean:{counts['clean']:>4}\033[0m "
                    f"\033[92mout:{counts['relayed']:>4}\033[0m "
                    f"\033[91mdedup:{dedup:>4}\033[0m"
                )
        print(f"\033[90m{'─'*70}\033[0m")
        print()


# Registry of active paho clients for clean shutdown
_clients = []
_clients_lock = threading.Lock()


def monitor_topic(label, topic):
    """Monitor a single topic pattern using paho-mqtt (handles binary payloads correctly)."""
    colors = {
        "BRIDGE_IN": "\033[93m",  # Yellow
        "CLEAN":     "\033[96m",  # Cyan
        "RELAYED":   "\033[92m",  # Green
    }
    icons    = {"BRIDGE_IN": "📥", "CLEAN": "📦", "RELAYED": "📤"}
    stat_keys = {"BRIDGE_IN": "bridge_in", "CLEAN": "clean", "RELAYED": "relayed"}
    color    = colors.get(label, "\033[0m")
    icon     = icons.get(label, "📦")
    stat_key = stat_keys.get(label, "bridge_in")
    reset    = "\033[0m"
    msg_count = [0]

    def on_connect(client, userdata, flags, reason_code, properties):
        if reason_code.is_failure:
            print(f"[{datetime.now().strftime('%H:%M:%S')}] ❌ {label} connect failed: {reason_code}")
        else:
            client.subscribe(topic, qos=0)

    def on_disconnect(client, userdata, flags, reason_code, properties):
        if reason_code.value != 0:
            print(f"[{datetime.now().strftime('%H:%M:%S')}] ⚠️  {label} disconnected ({reason_code}), reconnecting…")

    def on_message(client, userdata, message):
        timestamp = datetime.now().strftime("%H:%M:%S.%f")[:-3]
        msg_topic       = message.topic
        msg_payload_bytes = message.payload  # bytes — no newline splitting!

        subtopic = get_subtopic(msg_topic)
        h = short_hash(msg_topic, msg_payload_bytes)

        with stats_lock:
            stats[stat_key] += 1
            per_subtopic[subtopic][stat_key] += 1

        msg_count[0] += 1
        display_payload = decode_payload(msg_topic, msg_payload_bytes)

        print(
            f"{color}{icon} [{timestamp}] [{label:<9s}]{reset} "
            f"\033[36m{subtopic}\033[0m "
            f"\033[90m#{h}\033[0m "
            f"│ {display_payload}"
        )

        if msg_count[0] % 30 == 0:
            print_stats()

    try:
        try:
            client = paho_mqtt.Client(
                callback_api_version=paho_mqtt.CallbackAPIVersion.VERSION2,
                client_id=f"monitor-{label.lower()}-{os.getpid()}"
            )
        except (AttributeError, TypeError):
            client = paho_mqtt.Client(client_id=f"monitor-{label.lower()}-{os.getpid()}")

        if BROKER["user"] and BROKER["pass"]:
            client.username_pw_set(BROKER["user"], BROKER["pass"])
        client.on_connect    = on_connect
        client.on_disconnect = on_disconnect
        client.on_message    = on_message
        client.reconnect_delay_set(min_delay=1, max_delay=30)
        client.connect(BROKER["host"], BROKER["port"], keepalive=60)
        with _clients_lock:
            _clients.append(client)
        client.loop_forever()
        with _clients_lock:
            _clients.remove(client)
    except Exception as e:
        print(f"[{datetime.now().strftime('%H:%M:%S')}] ❌ {label} error: {e}")


def stats_printer():
    """Print stats periodically"""
    import time
    while True:
        time.sleep(30)
        with stats_lock:
            if stats["bridge_in"] > 0 or stats["clean"] > 0:
                print_stats()


def main():
    print("\033[1m" + "=" * 70 + "\033[0m")
    print("\033[1m  🔄 MQTT RELAY MONITOR v2 — Deduplication Tracker\033[0m")
    print("\033[1m" + "=" * 70 + "\033[0m")
    print(f"  📡 Broker  : {BROKER['host']}:{BROKER['port']}")
    print(f"  🕐 Started : {datetime.now().strftime('%Y-%m-%d %H:%M:%S')}")
    print("\033[90m" + "-" * 70 + "\033[0m")
    print("  Topics:")
    print(f"  \033[93m📥 BRIDGE_IN : {TOPICS['BRIDGE_IN']}\033[0m  — raw dari bridge, 2x dups")
    print(f"  \033[96m📦 CLEAN     : {TOPICS['CLEAN']}\033[0m  — deduped untuk client")
    print(f"  \033[92m📤 RELAYED   : {TOPICS['RELAYED']}\033[0m  — outbound ke bridge")
    print("\033[90m" + "-" * 70 + "\033[0m")
    print("  Libraries:")
    proto_st = "\033[92m✓ meshtastic\033[0m" if _PROTO_AVAILABLE else "\033[91m✗ pip install meshtastic\033[0m"
    crypto_st = "\033[92m✓ cryptography\033[0m" if _CRYPTO_AVAILABLE else "\033[91m✗ pip install cryptography\033[0m"
    print(f"  📦 Proto decode : {proto_st}")
    print(f"  🔑 AES decrypt  : {crypto_st}")
    print("\033[90m" + "-" * 70 + "\033[0m")
    print("  ℹ️  Relay bekerja jika RAW >> CLEAN (duplikat dibuang)")
    print("  ℹ️  Ratio ideal : CLEAN ≈ RAW/2 (2 bridge aktif)")
    print("\033[90m" + "-" * 70 + "\033[0m")
    print("  Press Ctrl+C to stop")
    print("\033[1m" + "=" * 70 + "\033[0m")
    print()

    threads = []

    # Monitor raw bridge inbound
    t1 = threading.Thread(
        target=monitor_topic, args=("BRIDGE_IN", TOPICS["BRIDGE_IN"])
    )
    t1.daemon = True
    t1.start()
    threads.append(t1)

    # Monitor clean deduped
    t2 = threading.Thread(
        target=monitor_topic, args=("CLEAN", TOPICS["CLEAN"])
    )
    t2.daemon = True
    t2.start()
    threads.append(t2)

    # Monitor outbound relay
    t3 = threading.Thread(
        target=monitor_topic, args=("RELAYED", TOPICS["RELAYED"])
    )
    t3.daemon = True
    t3.start()
    threads.append(t3)

    # Periodic stats
    t4 = threading.Thread(target=stats_printer)
    t4.daemon = True
    t4.start()

    try:
        for t in threads:
            t.join()
    except KeyboardInterrupt:
        print()
        with _clients_lock:
            for c in _clients:
                try:
                    c.disconnect()
                except Exception:
                    pass
        print_stats()
        print("🛑 Stopped by user")
        sys.exit(0)


if __name__ == "__main__":
    main()
