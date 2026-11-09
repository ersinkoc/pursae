#!/usr/bin/env python3
"""Turn bench/results/<date>/summary.json into summary.md (the tables used in benchmarks.md)."""
import json
import os
import sys

ORDER = ["wp-stock", "wp-tuned", "wp-plugins", "pursae-nocache", "pursae-cache", "pursae-plugins"]
SHORT = {"wp-stock": "WP stock", "wp-tuned": "WP tuned", "wp-plugins": "WP + 6 plugins",
         "pursae-nocache": "Pursae, no cache", "pursae-cache": "Pursae, cache", "pursae-plugins": "Pursae + 6 built-ins"}
ENDPOINTS = [("home", "Home"), ("single", "Single post"), ("archive", "Category archive"), ("product", "Product"),
             ("search", "Search"), ("rest-posts", "REST posts list"), ("admin-dashboard", "Admin dashboard (logged in)"),
             ("admin-posts", "Admin post list (logged in)")]


def fmt(v, nd=0):
    if v is None or v == "":
        return "—"
    if isinstance(v, float):
        return f"{v:,.{nd}f}"
    return f"{v:,}" if isinstance(v, int) else str(v)


def table(head, rows):
    out = ["| " + " | ".join(head) + " |", "|" + "|".join("---" if i == 0 else "---:" for i in range(len(head))) + "|"]
    out += ["| " + " | ".join(r) + " |" for r in rows]
    return "\n".join(out)


def main(path):
    s = json.load(open(path))
    setups = [x for x in ORDER if x in s["setups"]]
    S = s["setups"]
    h, p = s["host"], s["params"]
    md = [f"# Benchmark results {h['date']}", "",
          f"Host: {h.get('cpu')}, {h.get('host_mem_gb', '?')} GB; Docker {h['docker']} with {h['docker_cpus']} CPUs / {h['docker_mem_gb']} GB. "
          f"Load: k6 ({h['k6']}), {p['vus']} virtual users, {p['duration']} per run after a {p['warmup']} warm-up, "
          f"median of {p['runs']} runs. Limits per stack: app 2 CPU / 1 GiB, database 1 CPU / 1 GiB "
          "(WordPress also gets nginx 1 CPU / 256 MiB and, tuned, Redis 0.5 CPU / 256 MiB).", ""]
    head = ["Endpoint"] + [SHORT[x] for x in setups]
    for metric, title, nd in (("rps", "Throughput (req/s, higher is better)", 0), ("ttfb_p50", "TTFB p50 under load (ms)", 1),
                              ("p95", "Latency p95 under load (ms)", 1), ("p99", "Latency p99 under load (ms)", 1)):
        rows = []
        for ep, label in ENDPOINTS:
            if not any(ep in S[x].get("load", {}) for x in setups):
                continue
            rows.append([label] + [fmt(S[x]["load"].get(ep, {}).get("median", {}).get(metric), nd) for x in setups])
        md += [f"## {title}", "", table(head, rows), ""]
    for key, title in (("cold", "First request after a restart (cold)"), ("warm", "Second request (warm)")):
        rows = []
        for ep, label in ENDPOINTS:
            if not any(ep in S[x].get("queries", {}) for x in setups):
                continue
            row = [label]
            for x in setups:
                q = S[x].get("queries", {}).get(ep)
                row.append(f"{fmt(q[key + '_ttfb_ms'], 1)} ms · {fmt(q[key + '_queries'])} q" if q else "—")
            rows.append(row)
        md += [f"## {title}: single-request TTFB · DB queries", "", table(head, rows), ""]
    rows = [
        ["Memory idle (MiB, all containers)"] + [fmt(S[x].get("mem_idle_mb"), 0) for x in setups],
        ["Memory peak under load (MiB)"] + [fmt(S[x].get("mem_peak_mb"), 0) for x in setups],
        ["Cold start, whole stack (s)"] + [fmt(S[x].get("cold_start_median", {}).get("full_stack_s"), 2) for x in setups],
        ["Cold start, app only (s)"] + [fmt(S[x].get("cold_start_median", {}).get("app_only_s"), 2) for x in setups],
        ["Docker images (MB)"] + [fmt(S[x].get("sizes", {}).get("images_total_mb"), 0) for x in setups],
        ["Install on disk (MB, no uploads)"] + [fmt(S[x].get("sizes", {}).get("install_mb"), 1) for x in setups],
        ["Database size (MB)"] + [fmt(S[x].get("sizes", {}).get("db_mb"), 1) for x in setups],
        ["Database tables"] + [fmt(S[x].get("sizes", {}).get("db_tables")) for x in setups],
    ]
    lh = [S[x].get("lighthouse", {}) for x in setups]
    if any("performance" in x for x in lh):
        rows += [["Lighthouse performance (home, mobile)"] + [fmt(round(x["performance"] * 100)) if "performance" in x else "—" for x in lh],
                 ["Lighthouse LCP (ms)"] + [fmt(x.get("lcp_ms"), 0) for x in lh],
                 ["Home page weight (KB)"] + [fmt(x["page_bytes"] / 1024, 0) if x.get("page_bytes") else "—" for x in lh]]
    md += ["## Resources", "", table(["Metric"] + [SHORT[x] for x in setups], rows), ""]
    for side, info in s.get("sides", {}).items():
        if "seed" in info:
            md.append(f"Seeding {side}: {info['seed']}")
    md.append("")
    with open(os.path.join(os.path.dirname(path), "summary.md"), "w") as f:
        f.write("\n".join(md))
    print("\n".join(md))


if __name__ == "__main__":
    main(sys.argv[1])
