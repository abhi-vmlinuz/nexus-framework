#!/usr/bin/env python3
"""
scripts/event-report.py — Competition & Telemetry Report Generator for Nexus Framework.

Parses /var/lib/nexus/events.jsonl and generates:
1. report.csv: Structured summary and challenge performance rows
2. report.pdf: Executive presentation PDF suitable for university faculty and corporate sponsors

Usage:
    python3 scripts/event-report.py [--input /var/lib/nexus/events.jsonl] [--pdf report.pdf] [--csv report.csv]
    python3 scripts/event-report.py --generate-sample sample_events.jsonl
"""

import argparse
import csv
import json
import math
import os
import shutil
import subprocess
import sys
from collections import defaultdict
from datetime import datetime, timezone


def parse_events(file_path):
    events = []
    if not os.path.exists(file_path):
        print(f"Warning: Events file '{file_path}' not found.", file=sys.stderr)
        return events

    with open(file_path, "r", encoding="utf-8") as f:
        for line_num, line in enumerate(f, 1):
            line = line.strip()
            if not line:
                continue
            try:
                ev = json.loads(line)
                events.append(ev)
            except json.JSONDecodeError:
                continue
    return events


def percentile(values, p):
    if not values:
        return 0.0
    sorted_vals = sorted(values)
    k = (len(sorted_vals) - 1) * (p / 100.0)
    f = math.floor(k)
    c = math.ceil(k)
    if f == c:
        return float(sorted_vals[int(k)])
    d0 = sorted_vals[int(f)] * (c - k)
    d1 = sorted_vals[int(c)] * (k - f)
    return float(d0 + d1)


def analyze_events(events):
    users = set()
    sessions_created = 0
    sessions_ready = 0
    sessions_failed = 0
    sessions_expired = 0
    vpn_claimed = 0
    vpn_exhausted = 0
    reconcile_repairs = 0

    ready_durations = []  # in seconds
    challenge_stats = defaultdict(lambda: {"created": 0, "ready": 0, "failed": 0, "durations": []})

    # Track concurrent sessions
    timeline = []

    for ev in events:
        etype = ev.get("type", "")
        uid = ev.get("user_id")
        cid = ev.get("challenge_id", "unknown")
        dur_ms = ev.get("duration_ms", 0)
        ts_str = ev.get("ts", "")

        if uid:
            users.add(uid)

        if etype == "session_created":
            sessions_created += 1
            if cid:
                challenge_stats[cid]["created"] += 1
            if ts_str:
                timeline.append((ts_str, 1))

        elif etype == "session_ready":
            sessions_ready += 1
            dur_s = dur_ms / 1000.0 if dur_ms > 0 else 0.0
            ready_durations.append(dur_s)
            if cid:
                challenge_stats[cid]["ready"] += 1
                if dur_s > 0:
                    challenge_stats[cid]["durations"].append(dur_s)

        elif etype == "session_failed":
            sessions_failed += 1
            if cid:
                challenge_stats[cid]["failed"] += 1
            if ts_str:
                timeline.append((ts_str, -1))

        elif etype == "session_expired":
            sessions_expired += 1
            if ts_str:
                timeline.append((ts_str, -1))

        elif etype == "vpn_claimed":
            vpn_claimed += 1

        elif etype == "vpn_exhausted":
            vpn_exhausted += 1

        elif etype == "reconcile_repair":
            reconcile_repairs += 1

    # Estimate Peak Concurrent Sessions
    timeline.sort(key=lambda x: x[0])
    current_active = 0
    peak_active = 0
    for _, delta in timeline:
        current_active = max(0, current_active + delta)
        if current_active > peak_active:
            peak_active = current_active

    total_attempts = sessions_created if sessions_created > 0 else (sessions_ready + sessions_failed)
    success_rate = (sessions_ready / total_attempts * 100.0) if total_attempts > 0 else 0.0

    p50_lat = percentile(ready_durations, 50)
    p90_lat = percentile(ready_durations, 90)
    p95_lat = percentile(ready_durations, 95)
    p99_lat = percentile(ready_durations, 99)
    avg_lat = (sum(ready_durations) / len(ready_durations)) if ready_durations else 0.0

    chall_summary = []
    for cid, s in sorted(challenge_stats.items(), key=lambda x: x[1]["created"], reverse=True):
        c_dur = s["durations"]
        c_p95 = percentile(c_dur, 95) if c_dur else 0.0
        c_avg = (sum(c_dur) / len(c_dur)) if c_dur else 0.0
        c_attempts = s["created"] if s["created"] > 0 else (s["ready"] + s["failed"])
        c_succ = (s["ready"] / c_attempts * 100.0) if c_attempts > 0 else 0.0
        chall_summary.append({
            "challenge_id": cid,
            "created": s["created"],
            "ready": s["ready"],
            "failed": s["failed"],
            "success_rate": c_succ,
            "avg_latency_s": c_avg,
            "p95_latency_s": c_p95,
        })

    return {
        "total_events": len(events),
        "unique_students": len(users),
        "sessions_created": sessions_created,
        "sessions_ready": sessions_ready,
        "sessions_failed": sessions_failed,
        "sessions_expired": sessions_expired,
        "success_rate": success_rate,
        "peak_concurrent_sessions": peak_active,
        "vpn_claimed": vpn_claimed,
        "vpn_exhausted": vpn_exhausted,
        "reconcile_repairs": reconcile_repairs,
        "latency_p50": p50_lat,
        "latency_p90": p90_lat,
        "latency_p95": p95_lat,
        "latency_p99": p99_lat,
        "latency_avg": avg_lat,
        "challenges": chall_summary,
    }


def export_csv(metrics, output_path):
    with open(output_path, "w", newline="", encoding="utf-8") as f:
        writer = csv.writer(f)
        writer.writerow(["# Nexus Framework Competition Telemetry Report"])
        writer.writerow(["Generated At", datetime.now(timezone.utc).isoformat()])
        writer.writerow([])

        writer.writerow(["Metric", "Value"])
        writer.writerow(["Unique Students", metrics["unique_students"]])
        writer.writerow(["Total Sessions Created", metrics["sessions_created"]])
        writer.writerow(["Total Sessions Ready", metrics["sessions_ready"]])
        writer.writerow(["Total Sessions Failed", metrics["sessions_failed"]])
        writer.writerow(["Session Success Rate (%)", f"{metrics['success_rate']:.2f}"])
        writer.writerow(["Peak Concurrent Sessions", metrics["peak_concurrent_sessions"]])
        writer.writerow(["VPN IPs Claimed", metrics["vpn_claimed"]])
        writer.writerow(["VPN Exhaustion Incidents", metrics["vpn_exhausted"]])
        writer.writerow(["Reconciler Self-Healing Repairs", metrics["reconcile_repairs"]])
        writer.writerow(["p50 Spawn Latency (s)", f"{metrics['latency_p50']:.2f}"])
        writer.writerow(["p90 Spawn Latency (s)", f"{metrics['latency_p90']:.2f}"])
        writer.writerow(["p95 Spawn Latency (s)", f"{metrics['latency_p95']:.2f}"])
        writer.writerow(["p99 Spawn Latency (s)", f"{metrics['latency_p99']:.2f}"])
        writer.writerow(["Average Spawn Latency (s)", f"{metrics['latency_avg']:.2f}"])
        writer.writerow([])

        writer.writerow(["# Challenge Performance Breakdown"])
        writer.writerow(["Challenge ID", "Spawns", "Ready", "Failed", "Success %", "Avg Latency (s)", "p95 Latency (s)"])
        for c in metrics["challenges"]:
            writer.writerow([
                c["challenge_id"],
                c["created"],
                c["ready"],
                c["failed"],
                f"{c['success_rate']:.1f}",
                f"{c['avg_latency_s']:.2f}",
                f"{c['p95_latency_s']:.2f}",
            ])
    print(f"[+] Wrote CSV summary to {output_path}")


def export_pdf(metrics, output_path, title="Nexus CTF Competition Report"):
    typst_bin = shutil.which("typst") or "/home/elish4h/.cargo/bin/typst"
    if os.path.exists(typst_bin) and os.access(typst_bin, os.X_OK):
        render_typst_pdf(metrics, output_path, title, typst_bin)
    else:
        print("Typst not detected; generating pure PDF fallback...", file=sys.stderr)
        render_minimal_pdf(metrics, output_path, title)


def render_typst_pdf(metrics, output_path, title, typst_bin):
    typ_content = f"""
#set page(
  paper: "a4",
  margin: (x: 2cm, y: 2.2cm),
  header: align(right, text(size: 9pt, fill: rgb("#718096"))[{title}]),
  footer: align(center)[#context text(size: 9pt, fill: rgb("#A0AEC0"))[Page #counter(page).display()]]
)
#set text(font: ("Liberation Sans", "DejaVu Sans", "Helvetica", "Arial"), size: 10pt, fill: rgb("#1A202C"))
#set par(justify: false, leading: 0.65em)

// Header
#align(center)[
  #text(size: 20pt, weight: "bold", fill: rgb("#2B6CB0"))[{title}] \\
  #v(2pt)
  #text(size: 11pt, fill: rgb("#4A5568"))[Platform Performance, Telemetry & Reliability Brief for Organizers and Sponsors] \\
  #v(1pt)
  #text(size: 8.5pt, fill: rgb("#718096"))[Report Generated: {datetime.now(timezone.utc).strftime("%B %d, %Y - %H:%M UTC")} | Nexus Framework v0.1.2]
]

#v(12pt)
#line(length: 100%, stroke: 1.5pt + rgb("#3182CE"))
#v(8pt)

== 1. Executive Summary & Scale KPIs

The Nexus orchestration engine managed the complete lifecycle of isolated container challenges on bare-metal infrastructure. Below are the key scalability and competition participation metrics.

#v(8pt)

#grid(
  columns: (1fr, 1fr, 1fr, 1fr),
  gutter: 12pt,
  rect(width: 100%, fill: rgb("#EBF8FF"), stroke: 1pt + rgb("#BEE3F8"), radius: 4pt, inset: 10pt)[
    #align(center)[
      #text(size: 9pt, weight: "bold", fill: rgb("#2B6CB0"))[STUDENTS] \\
      #v(2pt)
      #text(size: 18pt, weight: "bold", fill: rgb("#2C5282"))[{metrics['unique_students']}]
    ]
  ],
  rect(width: 100%, fill: rgb("#F0FFF4"), stroke: 1pt + rgb("#C6F6D5"), radius: 4pt, inset: 10pt)[
    #align(center)[
      #text(size: 9pt, weight: "bold", fill: rgb("#276749"))[PEAK ACTIVE SESSIONS] \\
      #v(2pt)
      #text(size: 18pt, weight: "bold", fill: rgb("#22543D"))[{metrics['peak_concurrent_sessions']}]
    ]
  ],
  rect(width: 100%, fill: rgb("#FAF5FF"), stroke: 1pt + rgb("#E9D8FD"), radius: 4pt, inset: 10pt)[
    #align(center)[
      #text(size: 9pt, weight: "bold", fill: rgb("#6B46C1"))[SUCCESS RATE] \\
      #v(2pt)
      #text(size: 18pt, weight: "bold", fill: rgb("#553C9A"))[{metrics['success_rate']:.1f}%]
    ]
  ],
  rect(width: 100%, fill: rgb("#EDFDFD"), stroke: 1pt + rgb("#C4F1F9"), radius: 4pt, inset: 10pt)[
    #align(center)[
      #text(size: 9pt, weight: "bold", fill: rgb("#285E61"))[P95 PROVISION LATENCY] \\
      #v(2pt)
      #text(size: 18pt, weight: "bold", fill: rgb("#234E52"))[{metrics['latency_p95']:.1f}s]
    ]
  ]
)

#v(14pt)

== 2. Session Lifecycle & Infrastructure Health

#table(
  columns: (1fr, 1fr, 1fr, 1fr),
  fill: (col, row) => if row == 0 {{ rgb("#EDF2F7") }} else {{ none }},
  stroke: 0.5pt + rgb("#CBD5E0"),
  inset: 7pt,
  [ *Metric* ], [ *Value* ], [ *Metric* ], [ *Value* ],
  [ Total Sessions Created ], [ {metrics['sessions_created']} ], [ p50 Median Latency ], [ {metrics['latency_p50']:.2f} s ],
  [ Successful Spawns ], [ {metrics['sessions_ready']} ], [ p90 Latency ], [ {metrics['latency_p90']:.2f} s ],
  [ Failed Spawns ], [ {metrics['sessions_failed']} ], [ p95 Latency ], [ {metrics['latency_p95']:.2f} s ],
  [ Expired / Cleaned Sessions ], [ {metrics['sessions_expired']} ], [ p99 Latency ], [ {metrics['latency_p99']:.2f} s ],
  [ WireGuard VPN Claims ], [ {metrics['vpn_claimed']} ], [ Reconciler Self-Heals ], [ {metrics['reconcile_repairs']} ],
  [ VPN Pool Capacity (10.8.0.0/22) ], [ 1022 IPs ], [ VPN Pool Exhaustions ], [ {metrics['vpn_exhausted']} ]
)

#v(14pt)

== 3. Challenge Spawns & Performance Breakdown

#table(
  columns: (2.2fr, 1fr, 1fr, 1fr, 1.2fr, 1.2fr),
  fill: (col, row) => if row == 0 {{ rgb("#EDF2F7") }} else {{ none }},
  stroke: 0.5pt + rgb("#CBD5E0"),
  inset: 6pt,
  [ *Challenge ID* ], [ *Spawns* ], [ *Ready* ], [ *Fail* ], [ *Success %* ], [ *p95 Latency* ],
"""

    if metrics["challenges"]:
        for c in metrics["challenges"][:15]:
            typ_content += f"""  [ `{c['challenge_id']}` ], [ {c['created']} ], [ {c['ready']} ], [ {c['failed']} ], [ {c['success_rate']:.1f}% ], [ {c['p95_latency_s']:.2f}s ],\n"""
    else:
        typ_content += "  [ (No challenge data recorded) ], [ 0 ], [ 0 ], [ 0 ], [ 0% ], [ 0.00s ],\n"

    typ_content += """
)

#v(14pt)

== 4. Reliability & Architecture Notes
- *Zero Cloud VM Costs:* Ephemeral containers provisioned in-cluster without per-VM billing overhead.
- *Cryptographic Isolation:* Traffic isolation guaranteed via host-level WireGuard cryptographic routing and Linux ipset/iptables packet filtering.
- *Fault Recovery:* Autonomous reconciler periodically verifies active pod state and heals orphaned containers.
"""

    temp_typ = "/tmp/nexus_report_temp.typ"
    with open(temp_typ, "w", encoding="utf-8") as f:
        f.write(typ_content)

    res = subprocess.run([typst_bin, "compile", temp_typ, output_path], capture_output=True, text=True)
    if res.returncode == 0:
        print(f"[+] Successfully compiled PDF to {output_path} via Typst")
    else:
        print(f"Typst compilation failed: {res.stderr}. Using minimal fallback...", file=sys.stderr)
        render_minimal_pdf(metrics, output_path, title)
    try:
        os.remove(temp_typ)
    except OSError:
        pass


def render_minimal_pdf(metrics, output_path, title):
    # Minimal pure Python PDF generator (no external dependencies)
    lines = [
        f"%PDF-1.4",
        f"1 0 obj << /Type /Catalog /Pages 2 0 R >> endobj",
        f"2 0 obj << /Type /Pages /Kids [3 0 R] /Count 1 >> endobj",
        f"3 0 obj << /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Contents 4 0 R /Resources << /Font << /F1 5 0 R >> >> >> endobj",
        f"5 0 obj << /Type /Font /Subtype /Type1 /BaseFont /Helvetica >> endobj",
    ]

    text_stream = [
        "BT",
        "/F1 18 Tf",
        "50 740 Td",
        f"({title}) Tj",
        "/F1 11 Tf",
        "0 -25 Td",
        f"(Generated: {datetime.now(timezone.utc).strftime('%Y-%m-%d %H:%M UTC')}) Tj",
        "0 -30 Td",
        f"(Unique Students: {metrics['unique_students']}) Tj",
        "0 -20 Td",
        f"(Peak Concurrent Sessions: {metrics['peak_concurrent_sessions']}) Tj",
        "0 -20 Td",
        f"(Total Sessions Created: {metrics['sessions_created']} | Ready: {metrics['sessions_ready']} | Failed: {metrics['sessions_failed']}) Tj",
        "0 -20 Td",
        f"(Session Success Rate: {metrics['success_rate']:.1f}%) Tj",
        "0 -20 Td",
        f"(p50 Latency: {metrics['latency_p50']:.2f}s | p95 Latency: {metrics['latency_p95']:.2f}s) Tj",
        "0 -20 Td",
        f"(WireGuard VPN Claims: {metrics['vpn_claimed']} | Self-Healing Repairs: {metrics['reconcile_repairs']}) Tj",
        "ET",
    ]
    stream_content = "\n".join(text_stream).encode("latin-1")
    stream_obj = f"4 0 obj << /Length {len(stream_content)} >>\nstream\n".encode("latin-1") + stream_content + b"\nendstream\nendobj\n"

    with open(output_path, "wb") as f:
        for l in lines:
            f.write(l.encode("latin-1") + b"\n")
        f.write(stream_obj)
        f.write(b"xref\n0 6\n0000000000 65535 f \ntrailer << /Size 6 /Root 1 0 R >>\nstartxref\n9 \n%%EOF\n")
    print(f"[+] Fallback minimal PDF written to {output_path}")


def generate_sample_events(output_file, num_students=350, num_challenges=12):
    import random
    from datetime import timedelta

    base_time = datetime.now(timezone.utc) - timedelta(hours=4)
    challs = [f"web-sqli-{i}" for i in range(1, 4)] + [f"pwn-bof-{i}" for i in range(1, 4)] + [f"rev-crackme-{i}" for i in range(1, 4)] + ["crypto-rsa-oracle", "forensics-memdump", "cloud-iam-escape"]

    events = []
    for s_idx in range(1, num_students + 1):
        uid = f"student-{s_idx:03d}"
        t_vpn = base_time + timedelta(minutes=random.randint(0, 45), seconds=random.randint(0, 59))
        events.append({
            "ts": t_vpn.isoformat(),
            "type": "vpn_claimed",
            "user_id": uid,
            "duration_ms": random.randint(120, 950),
            "status": "claimed",
        })

        num_attempts = random.randint(2, 6)
        for _ in range(num_attempts):
            cid = random.choice(challs)
            sess_id = f"sess-{random.randint(100000, 999999)}"
            t_create = t_vpn + timedelta(minutes=random.randint(5, 180), seconds=random.randint(0, 59))
            events.append({
                "ts": t_create.isoformat(),
                "type": "session_created",
                "user_id": uid,
                "session_id": sess_id,
                "challenge_id": cid,
                "status": "creating",
            })

            # 96% success rate
            is_success = random.random() < 0.96
            spawn_dur_ms = int(random.gauss(5200, 1800))
            spawn_dur_ms = max(1100, min(28000, spawn_dur_ms))
            t_ready = t_create + timedelta(milliseconds=spawn_dur_ms)

            if is_success:
                events.append({
                    "ts": t_ready.isoformat(),
                    "type": "session_ready",
                    "user_id": uid,
                    "session_id": sess_id,
                    "challenge_id": cid,
                    "duration_ms": spawn_dur_ms,
                    "status": "running",
                })
                # Session expires after 30-45 mins
                t_exp = t_ready + timedelta(minutes=random.randint(30, 45))
                events.append({
                    "ts": t_exp.isoformat(),
                    "type": "session_expired",
                    "user_id": uid,
                    "session_id": sess_id,
                    "challenge_id": cid,
                    "status": "expired",
                })
            else:
                events.append({
                    "ts": t_ready.isoformat(),
                    "type": "session_failed",
                    "user_id": uid,
                    "session_id": sess_id,
                    "challenge_id": cid,
                    "duration_ms": spawn_dur_ms,
                    "status": "failed",
                    "detail": "K8s pod unschedulable timeout",
                })

    # Add a few self-healing reconcile repairs
    for _ in range(random.randint(4, 9)):
        t_rep = base_time + timedelta(minutes=random.randint(60, 200))
        events.append({
            "ts": t_rep.isoformat(),
            "type": "reconcile_repair",
            "session_id": f"sess-{random.randint(100000, 999999)}",
            "status": "repaired",
            "detail": "Restored missing ipset grant",
        })

    events.sort(key=lambda x: x["ts"])
    with open(output_file, "w", encoding="utf-8") as f:
        for ev in events:
            f.write(json.dumps(ev) + "\n")
    print(f"[+] Generated {len(events)} sample events for {num_students} students to {output_file}")


def main():
    parser = argparse.ArgumentParser(description="Generate Nexus competition report.")
    parser.add_argument("--input", "-i", default="/var/lib/nexus/events.jsonl", help="Path to events.jsonl")
    parser.add_argument("--pdf", "-p", default="report.pdf", help="Path to output PDF")
    parser.add_argument("--csv", "-c", default="report.csv", help="Path to output CSV")
    parser.add_argument("--title", default="Nexus CTF Competition Report", help="Report Title")
    parser.add_argument("--generate-sample", help="Generate a sample events.jsonl file with simulated student traffic")

    args = parser.parse_args()

    if args.generate_sample:
        generate_sample_events(args.generate_sample)
        return

    events = parse_events(args.input)
    if not events:
        print(f"[-] No events found in {args.input}. Run with --generate-sample sample_events.jsonl to test.", file=sys.stderr)
        sys.exit(1)

    metrics = analyze_events(events)
    export_csv(metrics, args.csv)
    export_pdf(metrics, args.pdf, title=args.title)
    print("[+] Report generation complete.")


if __name__ == "__main__":
    main()
