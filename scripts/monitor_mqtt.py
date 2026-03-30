#!/usr/bin/env python3
"""
MQTT Monitor - Monitor 2 servers simultaneously
Shows messages from both servers with timestamps
"""

import subprocess
import threading
import sys
from datetime import datetime

# Server configs
SERVERS = {
    "LOCAL": {
        "host": "localhost",
        "port": 1883,
        "user": "idmeshnode",
        "pass": "M3shN0d3",  # Same as community
    },
    "COMMUNITY": {
        "host": "mqtt.s-project.web.id",
        "port": 1883,
        "user": "idmeshnode",
        "pass": "M3shN0d3",
    },
    "MESHNODEID": {
        "host": "103.141.75.100",
        "port": 1883,
        "user": "meshnodeid",
        "pass": "p4d4n6",
    }
}

def monitor_server(name, config):
    """Monitor a single server and print messages"""
    cmd = [
        "mosquitto_sub",
        "-h", config['host'],
        "-p", str(config['port']),
        "-t", "msh/ID_923/#",
        "-v"
    ]
    
    # Add auth if provided
    if config['user'] and config['pass']:
        cmd.extend(["-u", config['user'], "-P", config['pass']])
    
    print(f"[{datetime.now().strftime('%H:%M:%S')}] 🟢 {name} connected to {config['host']}")
    
    try:
        proc = subprocess.Popen(
            cmd,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            text=True,
            bufsize=1
        )
        
        for line in proc.stdout:
            timestamp = datetime.now().strftime('%H:%M:%S.%f')[:-3]
            # Format: topic message
            parts = line.strip().split(' ', 1)
            if len(parts) >= 2:
                topic, msg = parts
            else:
                topic = parts[0] if parts else ""
                msg = ""
            
            # Color codes
            colors = {
                "LOCAL": "\033[93m",      # Yellow
                "COMMUNITY": "\033[92m",  # Green
                "MESHNODEID": "\033[94m", # Blue
            }
            color = colors.get(name, "\033[0m")
            reset = "\033[0m"
            
            print(f"{color}[{timestamp}] [{name}]{reset} {topic} | {msg}")
            
    except Exception as e:
        print(f"[{datetime.now().strftime('%H:%M:%S')}] ❌ {name} error: {e}")

def main():
    print("="*70)
    print("MQTT MONITOR - Watching both servers")
    print("="*70)
    print(f"📡 Topic: msh/ID_923/#")
    print(f"🕐 Started: {datetime.now().strftime('%Y-%m-%d %H:%M:%S')}")
    print("-"*70)
    print("LOCAL = localhost (Yellow)")
    print("COMMUNITY = mqtt.s-project.web.id (Green)")
    print("MESHNODEID = 103.141.75.100 (Blue)")
    print("-"*70)
    print("Press Ctrl+C to stop")
    print("="*70)
    print()
    
    # Start threads for each server
    threads = []
    for name, config in SERVERS.items():
        t = threading.Thread(target=monitor_server, args=(name, config))
        t.daemon = True
        t.start()
        threads.append(t)
    
    # Keep running
    try:
        for t in threads:
            t.join()
    except KeyboardInterrupt:
        print("\n\n🛑 Stopped by user")
        sys.exit(0)

if __name__ == "__main__":
    main()
