#!/usr/bin/env python3
"""
MQTT Relay Monitor v3 — Real dedup tracker

Subscribes to all three brokers the relay is connected to (local + upstream A
+ upstream B), all on the SAME topic (TOPIC_ROOT). For each message it computes
the canonical Meshtastic hash and marks it as first-seen or duplicate, mirroring
the dedup logic of the Go relay.

Optionally polls the relay's /metrics endpoint to cross-check counters.

Env (loaded from /root/MeshNode/.env if present):
  LOCAL_MQTT_HOST / LOCAL_MQTT_PORT / LOCAL_MQTT_TLS / LOCAL_MQTT_USERNAME / LOCAL_MQTT_PASSWORD
  UPSTREAM_A_HOST / UPSTREAM_A_PORT / UPSTREAM_A_TLS / UPSTREAM_A_USERNAME / UPSTREAM_A_PASSWORD
  UPSTREAM_B_HOST / UPSTREAM_B_PORT / UPSTREAM_B_TLS / UPSTREAM_B_USERNAME / UPSTREAM_B_PASSWORD
  TOPIC_ROOT     (default: msh/ID/#)
  RELAY_METRICS_URL (default: http://localhost:8081/metrics)

Monitor-only overrides (use these so monitor talks to the host, not the docker network):
  MONITOR_LOCAL_HOST / MONITOR_LOCAL_PORT / MONITOR_LOCAL_TLS / MONITOR_LOCAL_USER / MONITOR_LOCAL_PASS
"""

from __future__ import annotations

import base64
import hashlib
import os
import ssl
import struct
import sys
import threading
import time
import urllib.request
from collections import defaultdict, deque
from dataclasses import dataclass, field
from datetime import datetime
from typing import Optional

import paho.mqtt.client as paho_mqtt

try:
    from meshtastic.protobuf import mesh_pb2, mqtt_pb2, telemetry_pb2
    _PROTO_AVAILABLE = True
except ImportError:
    try:
        from meshtastic import mesh_pb2, mqtt_pb2, telemetry_pb2  # type: ignore
        _PROTO_AVAILABLE = True
    except ImportError:
        _PROTO_AVAILABLE = False

try:
    from cryptography.hazmat.primitives.ciphers import Cipher, algorithms, modes
    _CRYPTO_AVAILABLE = True
except ImportError:
    _CRYPTO_AVAILABLE = False


# ---------------------------------------------------------------------------
# Env loader
# ---------------------------------------------------------------------------

ENV_PATH = os.path.join(os.path.dirname(os.path.abspath(__file__)), "..", ".env")
if os.path.exists(ENV_PATH):
    with open(ENV_PATH) as f:
        for line in f:
            if "=" in line and not line.strip().startswith("#"):
                k, v = line.strip().split("=", 1)
                os.environ.setdefault(k, v.strip().strip('"').strip("'"))


def _bool(name: str, default: bool = False) -> bool:
    v = os.environ.get(name)
    if v is None:
        return default
    return v.strip().lower() in {"1", "true", "yes", "y", "on"}


def _int(name: str, default: int) -> int:
    try:
        return int(os.environ.get(name, default))
    except (TypeError, ValueError):
        return default


# ---------------------------------------------------------------------------
# Broker config (uses MONITOR_LOCAL_* override for local since the monitor
# runs on the host, not inside the docker network)
# ---------------------------------------------------------------------------

@dataclass
class BrokerCfg:
    label: str
    host: str
    port: int
    tls: bool
    user: str
    password: str


BROKERS: list[BrokerCfg] = []

# local — prefer MONITOR_LOCAL_* override
local_host = os.environ.get("MONITOR_LOCAL_HOST", "localhost")
if local_host:
    BROKERS.append(BrokerCfg(
        label="LOCAL",
        host=local_host,
        port=_int("MONITOR_LOCAL_PORT", _int("RELAY_LOCAL_MQTT_PORT", 1883)),
        tls=_bool("MONITOR_LOCAL_TLS", _bool("RELAY_LOCAL_MQTT_TLS", False)),
        user=os.environ.get("MONITOR_LOCAL_USER", os.environ.get("RELAY_LOCAL_MQTT_USERNAME", os.environ.get("LOCAL_MQTT_USERNAME", ""))),
        password=os.environ.get("MONITOR_LOCAL_PASS", os.environ.get("EMQX_USER_RELAY_PASS", os.environ.get("LOCAL_MQTT_PASSWORD", ""))),
    ))

# upstream A
up_a_host = os.environ.get("RELAY_UPSTREAM_A_HOST", os.environ.get("UPSTREAM_A_HOST", ""))
if up_a_host:
    BROKERS.append(BrokerCfg(
        label="UP_A",
        host=up_a_host,
        port=_int("RELAY_UPSTREAM_A_PORT", _int("UPSTREAM_A_PORT", 1883)),
        tls=_bool("RELAY_UPSTREAM_A_TLS", _bool("UPSTREAM_A_TLS", False)),
        user=os.environ.get("RELAY_UPSTREAM_A_USERNAME", os.environ.get("UPSTREAM_A_USERNAME", "")),
        password=os.environ.get("RELAY_UPSTREAM_A_PASSWORD", os.environ.get("UPSTREAM_A_PASSWORD", "")),
    ))

# upstream B
up_b_host = os.environ.get("RELAY_UPSTREAM_B_HOST", os.environ.get("UPSTREAM_B_HOST", ""))
if up_b_host:
    BROKERS.append(BrokerCfg(
        label="UP_B",
        host=up_b_host,
        port=_int("RELAY_UPSTREAM_B_PORT", _int("UPSTREAM_B_PORT", 1883)),
        tls=_bool("RELAY_UPSTREAM_B_TLS", _bool("UPSTREAM_B_TLS", False)),
        user=os.environ.get("RELAY_UPSTREAM_B_USERNAME", os.environ.get("UPSTREAM_B_USERNAME", "")),
        password=os.environ.get("RELAY_UPSTREAM_B_PASSWORD", os.environ.get("UPSTREAM_B_PASSWORD", "")),
    ))

TOPIC_ROOT = os.environ.get("RELAY_TOPIC_ROOT", os.environ.get("TOPIC_ROOT", "msh/ID/#"))
METRICS_URL = os.environ.get("RELAY_METRICS_URL", "http://localhost:8081/metrics")

# ---------------------------------------------------------------------------
# Canonical hashing — mirrors dedup.go in the Go relay
# ---------------------------------------------------------------------------

# Meshtastic default channel PSK (AQ== expands to this 16-byte AES key)
_DEFAULT_KEY_BYTES = base64.b64decode("1PG7OiApB1nwvP+rz05pAQ==")
CHANNEL_KEYS = {"LongFast": "AQ==", "MeshNode_ID": "AQ=="}
# PSK per-channel dari .env — display-only; relay tidak pernah decrypt
if os.environ.get("PRIVATE_CHANNEL_PSK"):
    CHANNEL_KEYS["Private"] = os.environ["PRIVATE_CHANNEL_PSK"]
if os.environ.get("MESHNODE_ID_CHANNEL_PSK"):
    CHANNEL_KEYS["MeshNode_ID"] = os.environ["MESHNODE_ID_CHANNEL_PSK"]
_PORT_TEXT, _PORT_POS, _PORT_NODEINFO, _PORT_TELEMETRY = 1, 3, 4, 67


def canonical_hash(topic: str, payload: bytes) -> str:
    """Compute the same canonical SHA-256 the Go relay uses.

    For a valid Meshtastic ServiceEnvelope we extract the immutable packet
    fields (from, to, id, channel, encrypted/decoded) and hash topic + those.
    Otherwise fall back to hashing topic + raw payload.
    """
    canon = _canonical_meshpacket(payload)
    h = hashlib.sha256()
    h.update(topic.encode("utf-8"))
    h.update(canon if canon is not None else payload)
    return h.hexdigest()


def _canonical_meshpacket(envelope: bytes) -> Optional[bytes]:
    if not _PROTO_AVAILABLE:
        return None
    try:
        env = mqtt_pb2.ServiceEnvelope()
        env.ParseFromString(envelope)
    except Exception:
        return None
    pkt = env.packet
    from_node = getattr(pkt, "from")
    to_node = pkt.to
    pkt_id = pkt.id
    if from_node == 0 or pkt_id == 0:
        # missing required identity → fall back to raw hash
        return None
    encrypted = bytes(pkt.encrypted) if pkt.HasField("encrypted") else b""
    decoded = pkt.decoded.SerializeToString() if pkt.HasField("decoded") else b""
    if not encrypted and not decoded:
        return None
    out = (
        struct.pack("<Q", from_node) +
        struct.pack("<Q", to_node) +
        struct.pack("<Q", pkt_id) +
        struct.pack("<Q", pkt.channel) +
        encrypted +
        decoded
    )
    return out


# ---------------------------------------------------------------------------
# Stats
# ---------------------------------------------------------------------------

@dataclass
class Stats:
    received: dict[str, int] = field(default_factory=lambda: defaultdict(int))
    first_seen: dict[str, int] = field(default_factory=lambda: defaultdict(int))
    duplicates: dict[str, int] = field(default_factory=lambda: defaultdict(int))
    per_subtopic_first: dict[str, int] = field(default_factory=lambda: defaultdict(int))
    per_subtopic_dup: dict[str, int] = field(default_factory=lambda: defaultdict(int))


stats = Stats()
stats_lock = threading.Lock()

# Hash → (first_label, first_ts) within DEDUP_WINDOW seconds
DEDUP_WINDOW = 600  # match relay default
seen_lock = threading.Lock()
seen: dict[str, tuple[str, float]] = {}
seen_q: deque[tuple[str, float]] = deque()  # (hash, ts) for cheap eviction


def _evict_expired(now: float) -> None:
    while seen_q and now - seen_q[0][1] > DEDUP_WINDOW:
        h, ts = seen_q.popleft()
        cur = seen.get(h)
        if cur and cur[1] == ts:
            seen.pop(h, None)


def check_seen(h: str, label: str) -> Optional[str]:
    """Return None if first-seen; otherwise the previous source label."""
    now = time.time()
    with seen_lock:
        _evict_expired(now)
        prev = seen.get(h)
        if prev is None:
            seen[h] = (label, now)
            seen_q.append((h, now))
            return None
        return prev[0]


# ---------------------------------------------------------------------------
# Decoding helpers (best-effort, for display only)
# ---------------------------------------------------------------------------

def _expand_psk(psk_b64: str) -> bytes:
    raw = base64.b64decode(psk_b64)
    if len(raw) == 1 and raw[0] == 0x01:
        return _DEFAULT_KEY_BYTES
    return raw


def _aes_ctr_decrypt(data: bytes, packet_id: int, from_node: int, key: bytes) -> Optional[bytes]:
    if not _CRYPTO_AVAILABLE:
        return None
    try:
        nonce = struct.pack("<Q", packet_id) + struct.pack("<I", from_node) + b"\x00\x00\x00\x00"
        cipher = Cipher(algorithms.AES(key), modes.CTR(nonce))
        dec = cipher.decryptor()
        return dec.update(data) + dec.finalize()
    except Exception:
        return None


def _decode_data(data) -> str:
    try:
        port = data.portnum
        if port == _PORT_TEXT:
            return f"\U0001f4ac \"{data.payload.decode('utf-8', errors='replace')}\""
        if port == _PORT_POS:
            pos = mesh_pb2.Position()
            pos.ParseFromString(data.payload)
            return f"\U0001f4cd {pos.latitude_i/1e7:.5f},{pos.longitude_i/1e7:.5f}"
        if port == _PORT_NODEINFO:
            user = mesh_pb2.User()
            user.ParseFromString(data.payload)
            return f"\U0001f464 {user.long_name} ({user.short_name})"
        if port == _PORT_TELEMETRY:
            tel = telemetry_pb2.Telemetry()
            tel.ParseFromString(data.payload)
            if tel.HasField("device_metrics"):
                m = tel.device_metrics
                return f"\U0001f50b {m.battery_level}% {m.voltage:.2f}V"
            if tel.HasField("environment_metrics"):
                m = tel.environment_metrics
                return f"\U0001f321\ufe0f {m.temperature:.1f}\u00b0C"
            return "\U0001f4ca TELEMETRY"
        return f"[port={port} {len(data.payload)}B]"
    except Exception as e:
        return f"[decode err: {e}]"


def decode_payload(topic: str, payload: bytes) -> str:
    if not _PROTO_AVAILABLE:
        try:
            return payload[:60].decode("utf-8", errors="replace")
        except Exception:
            return f"<{len(payload)}B>"
    if "/json/" in topic:
        try:
            return payload.decode("utf-8", errors="replace")[:120]
        except Exception:
            return f"<{len(payload)}B>"
    parts = topic.split("/")
    ch = parts[4] if len(parts) > 4 else "LongFast"
    is_pki = ch == "PKI"
    try:
        env = mqtt_pb2.ServiceEnvelope()
        env.ParseFromString(payload)
        pkt = env.packet
    except Exception:
        if is_pki:
            try:
                pkt = mesh_pb2.MeshPacket()
                pkt.ParseFromString(payload)
                from_node = getattr(pkt, "from")
                to_node = pkt.to
                enc_len = len(pkt.encrypted) if pkt.HasField("encrypted") else 0
                return (f"!{from_node:08x} → !{to_node:08x} "
                        f"[PKI DM {enc_len}B encrypted]")
            except Exception:
                pass
            return f"[PKI DM {len(payload)}B]"
        return f"[parse err {len(payload)}B]"
    from_node = getattr(pkt, "from")
    node = f"!{from_node:08x}"
    if pkt.HasField("encrypted"):
        key = _expand_psk(CHANNEL_KEYS.get(ch, "AQ=="))
        plain = _aes_ctr_decrypt(bytes(pkt.encrypted), pkt.id, from_node, key)
        if plain is None:
            return f"{node} [encrypted {len(pkt.encrypted)}B]"
        try:
            data = mesh_pb2.Data()
            data.ParseFromString(plain)
            return f"{node} {_decode_data(data)}"
        except Exception:
            return f"{node} [decrypt ok, parse fail]"
    if pkt.HasField("decoded"):
        return f"{node} {_decode_data(pkt.decoded)}"
    return f"{node} [no payload]"


def get_subtopic(topic: str) -> str:
    if "/ID/" in topic:
        return topic.split("/ID/", 1)[1]
    return topic


# ---------------------------------------------------------------------------
# MQTT monitor
# ---------------------------------------------------------------------------

COLORS = {"LOCAL": "\033[96m", "UP_A": "\033[93m", "UP_B": "\033[95m"}
ICONS = {"LOCAL": "\U0001f4e6", "UP_A": "\U0001f4e5", "UP_B": "\U0001f4e5"}
RESET = "\033[0m"
DUP = "\033[91m"   # red
NEW = "\033[92m"   # green

_clients: list[paho_mqtt.Client] = []
_clients_lock = threading.Lock()


def monitor_broker(cfg: BrokerCfg) -> None:
    color = COLORS.get(cfg.label, "")
    icon = ICONS.get(cfg.label, "*")

    def on_connect(client, userdata, flags, reason_code, properties):
        if reason_code.is_failure if hasattr(reason_code, "is_failure") else reason_code != 0:
            print(f"[{datetime.now():%H:%M:%S}] {color}{cfg.label}{RESET} connect failed: {reason_code}")
            return
        client.subscribe(TOPIC_ROOT, qos=0)
        print(f"[{datetime.now():%H:%M:%S}] {color}{cfg.label}{RESET} connected to {cfg.host}:{cfg.port}, subscribed {TOPIC_ROOT}")

    def on_disconnect(client, userdata, flags, reason_code, properties):
        rc = reason_code.value if hasattr(reason_code, "value") else reason_code
        if rc != 0:
            print(f"[{datetime.now():%H:%M:%S}] {color}{cfg.label}{RESET} disconnected ({reason_code}), reconnecting…")

    def on_message(client, userdata, message):
        ts = datetime.now().strftime("%H:%M:%S.%f")[:-3]
        topic = message.topic
        payload = message.payload
        sub = get_subtopic(topic)
        h = canonical_hash(topic, payload)
        prev = check_seen(h, cfg.label)

        with stats_lock:
            stats.received[cfg.label] += 1
            if prev is None:
                stats.first_seen[cfg.label] += 1
                stats.per_subtopic_first[sub] += 1
            else:
                stats.duplicates[cfg.label] += 1
                stats.per_subtopic_dup[sub] += 1

        if prev is None:
            tag = f"{NEW}NEW{RESET}"
            extra = ""
        else:
            tag = f"{DUP}DUP{RESET}"
            extra = f" \033[90m(prev: {prev}){RESET}"
        body = decode_payload(topic, payload)
        print(
            f"{color}{icon} [{ts}] [{cfg.label:<5}]{RESET} {tag} "
            f"\033[36m{sub}\033[0m \033[90m#{h[:12]}\033[0m{extra} | {body}"
        )

    try:
        try:
            client = paho_mqtt.Client(
                callback_api_version=paho_mqtt.CallbackAPIVersion.VERSION2,
                client_id=f"monitor-{cfg.label.lower()}-{os.getpid()}",
            )
        except (AttributeError, TypeError):
            client = paho_mqtt.Client(client_id=f"monitor-{cfg.label.lower()}-{os.getpid()}")

        if cfg.user and cfg.password:
            client.username_pw_set(cfg.user, cfg.password)
        if cfg.tls:
            client.tls_set(cert_reqs=ssl.CERT_NONE)
            client.tls_insecure_set(True)
        client.on_connect = on_connect
        client.on_disconnect = on_disconnect
        client.on_message = on_message
        client.reconnect_delay_set(min_delay=1, max_delay=30)
        client.connect(cfg.host, cfg.port, keepalive=60)
        with _clients_lock:
            _clients.append(client)
        client.loop_forever()
    except Exception as e:
        print(f"[{datetime.now():%H:%M:%S}] {color}{cfg.label}{RESET} error: {e}")


# ---------------------------------------------------------------------------
# /metrics poller
# ---------------------------------------------------------------------------

_METRIC_KEYS = (
    "mqtt_relay_messages_received_total",
    "mqtt_relay_messages_relayed_total",
    "mqtt_relay_messages_dropped_total",
    "mqtt_relay_dedup_cache_size",
    "mqtt_relay_up",
    "mqtt_relay_upstream_connected",
)


def parse_metrics(text: str) -> dict[str, str]:
    out: dict[str, str] = {}
    for line in text.splitlines():
        if not line or line.startswith("#"):
            continue
        if not any(line.startswith(k) for k in _METRIC_KEYS):
            continue
        try:
            name_lbl, val = line.rsplit(" ", 1)
        except ValueError:
            continue
        out[name_lbl] = val
    return out


def metrics_poller() -> None:
    if not METRICS_URL:
        return
    while True:
        time.sleep(30)
        try:
            with urllib.request.urlopen(METRICS_URL, timeout=5) as resp:
                txt = resp.read().decode("utf-8", errors="replace")
        except Exception as e:
            print(f"\n\033[90m[metrics] poll failed: {e}\033[0m\n")
            continue
        m = parse_metrics(txt)
        if not m:
            continue
        print_summary(extra_metrics=m)


# ---------------------------------------------------------------------------
# Pretty summary
# ---------------------------------------------------------------------------

def print_summary(extra_metrics: dict[str, str] | None = None) -> None:
    with stats_lock:
        rec = dict(stats.received)
        first = dict(stats.first_seen)
        dup = dict(stats.duplicates)
        top = sorted(
            ((s, stats.per_subtopic_first[s] + stats.per_subtopic_dup[s]) for s in
             set(stats.per_subtopic_first) | set(stats.per_subtopic_dup)),
            key=lambda x: x[1], reverse=True,
        )[:5]
        first_per_sub = dict(stats.per_subtopic_first)
        dup_per_sub = dict(stats.per_subtopic_dup)

    total_rx = sum(rec.values())
    total_first = sum(first.values())
    total_dup = sum(dup.values())
    pct = (total_dup / total_rx * 100) if total_rx else 0.0

    print(f"\033[90m{'─'*78}\033[0m")
    print(
        f"\033[1m  STATS  RX:{total_rx}  NEW:{NEW}{total_first}{RESET}\033[1m  "
        f"DUP:{DUP}{total_dup}{RESET}\033[1m  ({pct:.1f}%)\033[0m"
    )
    for label in ("LOCAL", "UP_A", "UP_B"):
        if label in rec:
            color = COLORS.get(label, "")
            print(
                f"    {color}{label:<5}{RESET}  rx:{rec[label]:>5}  "
                f"new:{NEW}{first.get(label,0):>5}{RESET}  "
                f"dup:{DUP}{dup.get(label,0):>5}{RESET}"
            )
    if top:
        print("  TOP TOPICS:")
        for sub, _ in top:
            print(
                f"    {sub[:50]:<50}  "
                f"new:{NEW}{first_per_sub.get(sub,0):>4}{RESET}  "
                f"dup:{DUP}{dup_per_sub.get(sub,0):>4}{RESET}"
            )
    if extra_metrics:
        print("  RELAY /metrics:")
        for k, v in sorted(extra_metrics.items()):
            print(f"    \033[90m{k} = {v}\033[0m")
    print(f"\033[90m{'─'*78}\033[0m\n")


def periodic_summary() -> None:
    while True:
        time.sleep(30)
        with stats_lock:
            if sum(stats.received.values()) == 0:
                continue
        print_summary()


# ---------------------------------------------------------------------------
# Main
# ---------------------------------------------------------------------------

def main() -> None:
    print("\033[1m" + "=" * 78 + "\033[0m")
    print("\033[1m  MQTT RELAY MONITOR v3 — multi-broker dedup tracker\033[0m")
    print("\033[1m" + "=" * 78 + "\033[0m")
    print(f"  Topic        : {TOPIC_ROOT}")
    print(f"  Dedup window : {DEDUP_WINDOW}s")
    print(f"  Metrics      : {METRICS_URL or '(disabled)'}")
    print(f"  Proto decode : {'ok' if _PROTO_AVAILABLE else 'install meshtastic'}")
    print(f"  AES decrypt  : {'ok' if _CRYPTO_AVAILABLE else 'install cryptography'}")
    print("\033[90m" + "-" * 78 + "\033[0m")
    if not BROKERS:
        print("  no brokers configured — set LOCAL/UPSTREAM_A/UPSTREAM_B env vars")
        sys.exit(2)
    for b in BROKERS:
        c = COLORS.get(b.label, "")
        print(f"  {c}{b.label:<5}{RESET} {b.host}:{b.port}  tls={b.tls}  user={b.user or '-'}")
    print("\033[90m" + "-" * 78 + "\033[0m")
    print("  NEW = first time this canonical hash is seen across all brokers")
    print("  DUP = same hash already seen on another broker (or echo) within window")
    print("\033[1m" + "=" * 78 + "\033[0m\n")

    threads = []
    for cfg in BROKERS:
        t = threading.Thread(target=monitor_broker, args=(cfg,), daemon=True)
        t.start()
        threads.append(t)

    threading.Thread(target=periodic_summary, daemon=True).start()
    threading.Thread(target=metrics_poller, daemon=True).start()

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
        print_summary()
        print("stopped")
        sys.exit(0)


if __name__ == "__main__":
    main()
