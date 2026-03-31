#!/usr/bin/env python3
"""
MQTT Relay Monitor v2 - Monitor relay dedup service in real-time
Shows raw bridge inbound vs clean deduped vs outbound relay messages
with dedup stats.

Architecture (v2):
  📥 BRIDGE_IN : msh/bridge_in/ID_923/#  — raw from bridges (3x dups)
  📦 CLEAN     : msh/ID_923/#            — deduped by relay for clients
  📤 RELAYED   : msh/relay/ID_923/#      — outbound to bridges
"""

import subprocess
import threading
import sys
import hashlib
from datetime import datetime
from collections import defaultdict

# Server config
BROKER = {
    "host": "localhost",
    "port": 1883,
    "user": "idmeshnode",
    "pass": "M3shN0d3",
}

# Topics to monitor (v2 — 3 namespaces)
TOPICS = {
    "BRIDGE_IN": "msh/bridge_in/ID_923/#",  # Raw inbound from bridges (duplicated)
    "CLEAN":     "msh/ID_923/#",             # Deduped by relay → local clients
    "RELAYED":   "msh/relay/ID_923/#",       # Outbound to bridges
}

# Stats
stats_lock = threading.Lock()
stats = {
    "bridge_in": 0,
    "clean": 0,
    "relayed": 0,
}
per_subtopic = defaultdict(lambda: {"bridge_in": 0, "clean": 0, "relayed": 0})


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
    """Extract subtopic after the ID_923/ part"""
    # msh/bridge_in/ID_923/2/json/... → 2/json/...
    # msh/ID_923/2/json/...           → 2/json/...
    # msh/relay/ID_923/2/json/...     → 2/json/...
    if "ID_923/" in topic:
        return topic.split("ID_923/", 1)[1]
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


def monitor_topic(label, topic):
    """Monitor a single topic pattern"""
    cmd = [
        "mosquitto_sub",
        "-h", BROKER["host"],
        "-p", str(BROKER["port"]),
        "-t", topic,
        "-v",
    ]
    if BROKER["user"] and BROKER["pass"]:
        cmd.extend(["-u", BROKER["user"], "-P", BROKER["pass"]])

    colors = {
        "BRIDGE_IN": "\033[93m",  # Yellow — raw bridge inbound
        "CLEAN":     "\033[96m",  # Cyan   — deduped clean
        "RELAYED":   "\033[92m",  # Green  — outbound relay
    }
    icons = {
        "BRIDGE_IN": "📥",
        "CLEAN":     "📦",
        "RELAYED":   "📤",
    }
    stat_keys = {
        "BRIDGE_IN": "bridge_in",
        "CLEAN":     "clean",
        "RELAYED":   "relayed",
    }
    color = colors.get(label, "\033[0m")
    icon = icons.get(label, "📦")
    stat_key = stat_keys.get(label, "bridge_in")
    reset = "\033[0m"

    msg_count = 0

    try:
        proc = subprocess.Popen(
            cmd,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            bufsize=0,
        )

        for raw_line in proc.stdout:
            timestamp = datetime.now().strftime("%H:%M:%S.%f")[:-3]
            parts = raw_line.strip().split(b" ", 1)
            msg_topic = parts[0].decode("utf-8", errors="replace") if parts else ""
            msg_payload_bytes = parts[1] if len(parts) >= 2 else b""

            subtopic = get_subtopic(msg_topic)
            h = short_hash(msg_topic, msg_payload_bytes)

            with stats_lock:
                stats[stat_key] += 1
                per_subtopic[subtopic][stat_key] += 1

            msg_count += 1
            display_payload = format_payload(msg_payload_bytes)

            print(
                f"{color}{icon} [{timestamp}] [{label:>9s}]{reset} "
                f"\033[36m{subtopic}\033[0m "
                f"\033[90m#{h}\033[0m "
                f"│ {display_payload}"
            )

            # Print stats every 30 messages
            if msg_count % 30 == 0:
                print_stats()

    except FileNotFoundError:
        print(f"❌ mosquitto_sub not found. Install: apt install mosquitto-clients")
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
    print(f"  📡 Broker   : {BROKER['host']}:{BROKER['port']}")
    print(f"  \033[93m📥 BRIDGE_IN : {TOPICS['BRIDGE_IN']}\033[0m (raw dari bridge, 3x dups)")
    print(f"  \033[96m📦 CLEAN     : {TOPICS['CLEAN']}\033[0m (deduped untuk client)")
    print(f"  \033[92m📤 RELAYED   : {TOPICS['RELAYED']}\033[0m (outbound ke bridge)")
    print(f"  🕐 Started  : {datetime.now().strftime('%Y-%m-%d %H:%M:%S')}")
    print("\033[90m" + "-" * 70 + "\033[0m")
    print("  Relay bekerja jika: RAW >> CLEAN (duplikat dibuang)")
    print("  Ratio ideal : CLEAN ≈ RAW/3 (3 bridge, 1 unik)")
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
        print_stats()
        print("🛑 Stopped by user")
        sys.exit(0)


if __name__ == "__main__":
    main()
