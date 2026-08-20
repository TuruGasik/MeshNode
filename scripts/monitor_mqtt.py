#!/usr/bin/env python3
"""
MQTT Monitor — pick a broker (or several) at runtime and stream decoded
Meshtastic packets in real time.

Usage:
    python3 monitor_mqtt.py                      # interactive picker
    python3 monitor_mqtt.py local                # single broker
    python3 monitor_mqtt.py local up_a           # multiple
    python3 monitor_mqtt.py all                  # all configured
    python3 monitor_mqtt.py --list               # show config and exit

Brokers are read from ../.env (the same file the relay uses).
Local broker host is overridden to "localhost" for monitoring from the host
unless MONITOR_LOCAL_HOST is explicitly set.
"""

from __future__ import annotations

import argparse
import base64
import os
import ssl
import struct
import sys
import threading
import time
from dataclasses import dataclass
from datetime import datetime
from typing import Optional

import paho.mqtt.client as paho_mqtt

try:
    from meshtastic.protobuf import mesh_pb2, mqtt_pb2, telemetry_pb2
    _PROTO_OK = True
except ImportError:
    try:
        from meshtastic import mesh_pb2, mqtt_pb2, telemetry_pb2  # type: ignore
        _PROTO_OK = True
    except ImportError:
        _PROTO_OK = False

try:
    from cryptography.hazmat.primitives.ciphers import Cipher, algorithms, modes
    _CRYPTO_OK = True
except ImportError:
    _CRYPTO_OK = False


# ---------------------------------------------------------------------------
# .env loader
# ---------------------------------------------------------------------------

ENV_PATH = os.path.join(os.path.dirname(os.path.abspath(__file__)), "..", ".env")
if os.path.exists(ENV_PATH):
    with open(ENV_PATH) as f:
        for line in f:
            if "=" in line and not line.lstrip().startswith("#"):
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
# Broker config
# ---------------------------------------------------------------------------

@dataclass
class Broker:
    key: str
    label: str
    host: str
    port: int
    tls: bool
    user: str
    password: str
    color: str

    @property
    def configured(self) -> bool:
        return bool(self.host)


BROKERS: dict[str, Broker] = {
    "local": Broker(
        key="local",
        label="LOCAL",
        host=os.environ.get("MONITOR_LOCAL_HOST", "localhost"),
        port=_int("MONITOR_LOCAL_PORT", _int("RELAY_LOCAL_MQTT_PORT", _int("LOCAL_MQTT_PORT", 1883))),
        tls=_bool("MONITOR_LOCAL_TLS", _bool("RELAY_LOCAL_MQTT_TLS", _bool("LOCAL_MQTT_TLS", False))),
        user=os.environ.get("MONITOR_LOCAL_USER", os.environ.get("RELAY_LOCAL_MQTT_USERNAME", os.environ.get("LOCAL_MQTT_USERNAME", ""))),
        password=os.environ.get("MONITOR_LOCAL_PASS", os.environ.get("EMQX_USER_RELAY_PASS", os.environ.get("LOCAL_MQTT_PASSWORD", ""))),
        color="\033[96m",  # cyan
    ),
    "up_a": Broker(
        key="up_a",
        label="UP_A",
        host=os.environ.get("RELAY_UPSTREAM_A_HOST", os.environ.get("UPSTREAM_A_HOST", "")),
        port=_int("RELAY_UPSTREAM_A_PORT", _int("UPSTREAM_A_PORT", 1883)),
        tls=_bool("RELAY_UPSTREAM_A_TLS", _bool("UPSTREAM_A_TLS", False)),
        user=os.environ.get("RELAY_UPSTREAM_A_USERNAME", os.environ.get("UPSTREAM_A_USERNAME", "")),
        password=os.environ.get("RELAY_UPSTREAM_A_PASSWORD", os.environ.get("UPSTREAM_A_PASSWORD", "")),
        color="\033[93m",  # yellow
    ),
    "up_b": Broker(
        key="up_b",
        label="UP_B",
        host=os.environ.get("RELAY_UPSTREAM_B_HOST", os.environ.get("UPSTREAM_B_HOST", "")),
        port=_int("RELAY_UPSTREAM_B_PORT", _int("UPSTREAM_B_PORT", 1883)),
        tls=_bool("RELAY_UPSTREAM_B_TLS", _bool("UPSTREAM_B_TLS", False)),
        user=os.environ.get("RELAY_UPSTREAM_B_USERNAME", os.environ.get("UPSTREAM_B_USERNAME", "")),
        password=os.environ.get("RELAY_UPSTREAM_B_PASSWORD", os.environ.get("UPSTREAM_B_PASSWORD", "")),
        color="\033[95m",  # magenta
    ),
}

TOPIC_ROOT = os.environ.get("RELAY_TOPIC_ROOT", os.environ.get("TOPIC_ROOT", "msh/ID/#"))
RESET = "\033[0m"
DIM = "\033[90m"


# ---------------------------------------------------------------------------
# Decoding (PSK + AES-CTR + protobuf)
# ---------------------------------------------------------------------------

_DEFAULT_KEY = base64.b64decode("1PG7OiApB1nwvP+rz05pAQ==")
CHANNEL_KEYS = {"LongFast": "AQ==", "MeshNode_ID": "AQ==", "GempaBumi": "AQ=="}
_PORT_TEXT, _PORT_POS, _PORT_NODEINFO, _PORT_TELEMETRY = 1, 3, 4, 67


def _expand_psk(b64: str) -> bytes:
    raw = base64.b64decode(b64)
    if len(raw) == 1 and raw[0] == 0x01:
        return _DEFAULT_KEY
    return raw


def _aes_ctr_decrypt(data: bytes, packet_id: int, from_node: int, key: bytes) -> Optional[bytes]:
    if not _CRYPTO_OK:
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
            return f"💬 \"{data.payload.decode('utf-8', errors='replace')}\""
        if port == _PORT_POS:
            pos = mesh_pb2.Position()
            pos.ParseFromString(data.payload)
            return f"📍 {pos.latitude_i/1e7:.5f},{pos.longitude_i/1e7:.5f}"
        if port == _PORT_NODEINFO:
            user = mesh_pb2.User()
            user.ParseFromString(data.payload)
            return f"👤 {user.long_name} ({user.short_name})"
        if port == _PORT_TELEMETRY:
            tel = telemetry_pb2.Telemetry()
            tel.ParseFromString(data.payload)
            if tel.HasField("device_metrics"):
                m = tel.device_metrics
                return f"🔋 {m.battery_level}% {m.voltage:.2f}V"
            if tel.HasField("environment_metrics"):
                m = tel.environment_metrics
                return f"🌡️ {m.temperature:.1f}°C"
            return "📊 TELEMETRY"
        return f"[port={port} {len(data.payload)}B]"
    except Exception as e:
        return f"[decode err: {e}]"


def decode_payload(topic: str, payload: bytes) -> str:
    if not _PROTO_OK:
        try:
            return payload[:80].decode("utf-8", errors="replace")
        except Exception:
            return f"<{len(payload)}B>"
    if "/json/" in topic:
        try:
            return payload.decode("utf-8", errors="replace")[:160]
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
    to_node = f"!{pkt.to:08x}"
    if pkt.HasField("encrypted"):
        key = _expand_psk(CHANNEL_KEYS.get(ch, "AQ=="))
        plain = _aes_ctr_decrypt(bytes(pkt.encrypted), pkt.id, from_node, key)
        if plain is None:
            return f"{node} → {to_node} [encrypted {len(pkt.encrypted)}B]"
        try:
            data = mesh_pb2.Data()
            data.ParseFromString(plain)
            return f"{node} → {to_node} {_decode_data(data)}"
        except Exception:
            return f"{node} [decrypt ok, parse fail]"
    if pkt.HasField("decoded"):
        return f"{node} → {to_node} {_decode_data(pkt.decoded)}"
    return f"{node} [no payload]"


def short_topic(topic: str) -> str:
    return topic.split("/ID/", 1)[1] if "/ID/" in topic else topic


# ---------------------------------------------------------------------------
# Monitor worker
# ---------------------------------------------------------------------------

_print_lock = threading.Lock()


def stamp() -> str:
    return datetime.now().strftime("%H:%M:%S.%f")[:-3]


def monitor(broker: Broker) -> None:
    color = broker.color

    def on_connect(client, userdata, flags, reason_code, properties=None):
        rc = reason_code.value if hasattr(reason_code, "value") else reason_code
        if rc != 0:
            with _print_lock:
                print(f"[{stamp()}] {color}{broker.label}{RESET} connect failed: {reason_code}")
            return
        client.subscribe(TOPIC_ROOT, qos=0)
        with _print_lock:
            print(f"[{stamp()}] {color}{broker.label}{RESET} connected → {broker.host}:{broker.port} "
                  f"(subscribed {TOPIC_ROOT})")

    def on_disconnect(client, userdata, *args, **kwargs):
        with _print_lock:
            print(f"[{stamp()}] {color}{broker.label}{RESET} disconnected, reconnecting…")

    def on_message(client, userdata, message):
        body = decode_payload(message.topic, message.payload)
        with _print_lock:
            print(f"{color}[{stamp()}] [{broker.label:<5}]{RESET} "
                  f"{DIM}{short_topic(message.topic)}{RESET} | {body}")

    try:
        try:
            client = paho_mqtt.Client(
                callback_api_version=paho_mqtt.CallbackAPIVersion.VERSION2,
                client_id=f"monitor-{broker.key}-{os.getpid()}",
            )
        except (AttributeError, TypeError):
            client = paho_mqtt.Client(client_id=f"monitor-{broker.key}-{os.getpid()}")

        if broker.user and broker.password:
            client.username_pw_set(broker.user, broker.password)
        if broker.tls:
            client.tls_set(cert_reqs=ssl.CERT_NONE)
            client.tls_insecure_set(True)
        client.on_connect = on_connect
        client.on_disconnect = on_disconnect
        client.on_message = on_message
        client.reconnect_delay_set(min_delay=1, max_delay=30)
        client.connect(broker.host, broker.port, keepalive=60)
        client.loop_forever()
    except Exception as e:
        with _print_lock:
            print(f"[{stamp()}] {color}{broker.label}{RESET} error: {e}")


# ---------------------------------------------------------------------------
# CLI / picker
# ---------------------------------------------------------------------------

def list_brokers() -> None:
    print(f"{'KEY':<6}{'LABEL':<8}{'HOST':<28}{'PORT':<6}{'TLS':<7}{'USER'}")
    for b in BROKERS.values():
        if not b.configured:
            print(f"{b.key:<6}{b.label:<8}{DIM}(not configured){RESET}")
            continue
        print(f"{b.color}{b.key:<6}{b.label:<8}{RESET}{b.host:<28}{b.port:<6}{str(b.tls):<7}{b.user or '-'}")


def interactive_picker() -> list[Broker]:
    avail = [b for b in BROKERS.values() if b.configured]
    if not avail:
        print("no brokers configured in .env", file=sys.stderr)
        sys.exit(2)

    print("Pick broker(s) to monitor:\n")
    for i, b in enumerate(avail, 1):
        print(f"  {i}. {b.color}{b.label:<6}{RESET} {b.host}:{b.port}")
    print(f"  {len(avail) + 1}. ALL\n")
    raw = input("choice (e.g. 1, or 1,3, or 4 for all): ").strip()
    if not raw:
        return avail
    if raw == str(len(avail) + 1) or raw.lower() == "all":
        return avail
    picked: list[Broker] = []
    for tok in raw.replace(" ", "").split(","):
        if not tok:
            continue
        try:
            idx = int(tok)
            if 1 <= idx <= len(avail):
                picked.append(avail[idx - 1])
        except ValueError:
            pass
    if not picked:
        print("no valid selection", file=sys.stderr)
        sys.exit(2)
    return picked


def parse_args() -> list[Broker]:
    parser = argparse.ArgumentParser(
        description="Monitor Meshtastic MQTT brokers with PSK decryption.",
    )
    parser.add_argument("brokers", nargs="*", help="local | up_a | up_b | all")
    parser.add_argument("--list", action="store_true", help="list configured brokers and exit")
    args = parser.parse_args()

    if args.list:
        list_brokers()
        sys.exit(0)

    if not args.brokers:
        return interactive_picker()

    if args.brokers == ["all"]:
        picks = [b for b in BROKERS.values() if b.configured]
    else:
        picks = []
        for key in args.brokers:
            key = key.lower()
            if key not in BROKERS:
                print(f"unknown broker: {key} (valid: local, up_a, up_b, all)", file=sys.stderr)
                sys.exit(2)
            if not BROKERS[key].configured:
                print(f"broker '{key}' not configured in .env", file=sys.stderr)
                sys.exit(2)
            picks.append(BROKERS[key])

    if not picks:
        print("no brokers selected", file=sys.stderr)
        sys.exit(2)
    return picks


# ---------------------------------------------------------------------------
# Main
# ---------------------------------------------------------------------------

def main() -> None:
    picks = parse_args()

    print("\033[1m" + "=" * 78 + "\033[0m")
    print("\033[1m  MQTT MONITOR — decoded Meshtastic stream\033[0m")
    print("\033[1m" + "=" * 78 + "\033[0m")
    print(f"  topic         : {TOPIC_ROOT}")
    print(f"  proto decode  : {'ok' if _PROTO_OK else 'install meshtastic'}")
    print(f"  AES decrypt   : {'ok' if _CRYPTO_OK else 'install cryptography'}")
    print(f"  brokers       : {', '.join(b.label for b in picks)}")
    print("\033[90m" + "-" * 78 + "\033[0m")
    for b in picks:
        print(f"  {b.color}{b.label:<6}{RESET} {b.host}:{b.port}  tls={b.tls}  user={b.user or '-'}")
    print("\033[1m" + "=" * 78 + "\033[0m\n")

    threads = []
    for b in picks:
        t = threading.Thread(target=monitor, args=(b,), daemon=True)
        t.start()
        threads.append(t)

    try:
        while any(t.is_alive() for t in threads):
            time.sleep(0.5)
    except KeyboardInterrupt:
        print("\nstopped")
        sys.exit(0)


if __name__ == "__main__":
    main()
