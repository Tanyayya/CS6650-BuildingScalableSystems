"""
Generate all required graphs for the KV distributed database report.

Install deps:
    pip install pandas matplotlib numpy

Run from project root (where the CSV files are):
    python graphs/plot.py
"""

import os
import pandas as pd
import matplotlib.pyplot as plt
import matplotlib.gridspec as gridspec
import numpy as np

# ---- config ----------------------------------------------------------------

CONFIGS = {
    "LF W=5 R=1": {
        "0.01": "results_lf_w5r1_w01.csv",
        "0.10": "results_lf_w5r1_w10.csv",
        "0.50": "results_lf_w5r1_w50.csv",
        "0.90": "results_lf_w5r1_w90.csv",
    },
    "LF W=1 R=5": {
        "0.01": "results_lf_w1r5_w01.csv",
        "0.10": "results_lf_w1r5_w10.csv",
        "0.50": "results_lf_w1r5_w50.csv",
        "0.90": "results_lf_w1r5_w90.csv",
    },
    "LF W=3 R=3": {
        "0.01": "results_lf_w3r3_w01.csv",
        "0.10": "results_lf_w3r3_w10.csv",
        "0.50": "results_lf_w3r3_w50.csv",
        "0.90": "results_lf_w3r3_w90.csv",
    },
    "Leaderless W=N R=1": {
        "0.01": "results_ll_w01.csv",
        "0.10": "results_ll_w10.csv",
        "0.50": "results_ll_w50.csv",
        "0.90": "results_ll_w90.csv",
    },
}

RATIOS = ["0.01", "0.10", "0.50", "0.90"]
RATIO_LABELS = ["1% write / 99% read", "10% write / 90% read",
                "50% write / 50% read", "90% write / 10% read"]
OUT_DIR = "graphs/output"
os.makedirs(OUT_DIR, exist_ok=True)

# ---- helpers ---------------------------------------------------------------

def load(path):
    if not os.path.exists(path):
        print(f"  [missing] {path}")
        return None
    df = pd.read_csv(path)
    df["latency_ms"] = pd.to_numeric(df["latency_ms"], errors="coerce")
    df["stale"] = df["stale"].astype(str).str.lower() == "true"
    return df

def percentile_label(arr, p):
    return f"p{p}={np.percentile(arr, p):.0f}ms"

# ---- Graph 1: Latency distributions per config, one page per ratio --------

for ratio, label in zip(RATIOS, RATIO_LABELS):
    fig, axes = plt.subplots(2, 4, figsize=(20, 8))
    fig.suptitle(f"Latency Distributions — {label}", fontsize=14, fontweight="bold")

    for col, (cfg_name, files) in enumerate(CONFIGS.items()):
        df = load(files[ratio])

        for row, op in enumerate(["read", "write"]):
            ax = axes[row][col]
            if df is None:
                ax.text(0.5, 0.5, "no data", ha="center", va="center")
                ax.set_title(f"{cfg_name}\n{op}s")
                continue

            data = df[df["op"] == op]["latency_ms"].dropna()
            if len(data) == 0:
                ax.text(0.5, 0.5, "no data", ha="center", va="center")
            else:
                ax.hist(data, bins=50, color="steelblue" if op == "read" else "tomato",
                        edgecolor="white", linewidth=0.3)
                # Mark p50, p95, p99 as vertical lines
                for p, ls in [(50, "--"), (95, "-."), (99, ":")]:
                    pv = np.percentile(data, p)
                    ax.axvline(pv, color="black", linestyle=ls, linewidth=1,
                               label=f"p{p}={pv:.0f}ms")
                ax.legend(fontsize=7)
                ax.set_xlabel("Latency (ms)", fontsize=8)
                ax.set_ylabel("Count", fontsize=8)

            ax.set_title(f"{cfg_name}\n{op}s", fontsize=9)

    plt.tight_layout()
    fname = f"{OUT_DIR}/latency_ratio_{ratio.replace('.','')}.png"
    plt.savefig(fname, dpi=150)
    plt.close()
    print(f"Saved {fname}")

# ---- Graph 2: Stale read counts across configs and ratios -----------------

fig, ax = plt.subplots(figsize=(12, 5))
x = np.arange(len(RATIOS))
width = 0.2

for i, (cfg_name, files) in enumerate(CONFIGS.items()):
    stale_pcts = []
    for ratio in RATIOS:
        df = load(files[ratio])
        if df is None:
            stale_pcts.append(0)
            continue
        reads = df[df["op"] == "read"]
        pct = 100 * reads["stale"].sum() / max(len(reads), 1)
        stale_pcts.append(pct)
    ax.bar(x + i * width, stale_pcts, width, label=cfg_name)

ax.set_xticks(x + width * 1.5)
ax.set_xticklabels(RATIO_LABELS, rotation=15, ha="right")
ax.set_ylabel("Stale reads (%)")
ax.set_title("Stale Read Percentage by Config and Write Ratio")
ax.legend()
plt.tight_layout()
fname = f"{OUT_DIR}/stale_reads.png"
plt.savefig(fname, dpi=150)
plt.close()
print(f"Saved {fname}")

# ---- Graph 3: Read/write interval distribution (time between R and W on same key) --
# We approximate this per-key: for each key, find the time gap between
# consecutive write and read events sorted by row order (proxy for time).

for cfg_name, files in CONFIGS.items():
    for ratio, label in zip(RATIOS, RATIO_LABELS):
        df = load(files[ratio])
        if df is None:
            continue

        # Assign a sequence number as a proxy for time (rows are in arrival order).
        df = df.reset_index(drop=True)
        df["seq"] = df.index

        intervals = []
        for key, grp in df.groupby("key"):
            grp = grp.sort_values("seq")
            writes = grp[grp["op"] == "write"]["seq"].tolist()
            reads  = grp[grp["op"] == "read"]["seq"].tolist()
            # For each read, find the nearest preceding write
            for r_seq in reads:
                preceding = [w for w in writes if w < r_seq]
                if preceding:
                    intervals.append(r_seq - max(preceding))

        if not intervals:
            continue

        fig, ax = plt.subplots(figsize=(8, 4))
        ax.hist(intervals, bins=40, color="mediumpurple", edgecolor="white", linewidth=0.3)
        ax.set_xlabel("Sequence distance (write → read on same key)")
        ax.set_ylabel("Count")
        ax.set_title(f"Read-after-Write Interval — {cfg_name} — {label}")
        plt.tight_layout()
        safe = cfg_name.replace(" ", "_").replace("=", "")
        fname = f"{OUT_DIR}/rw_interval_{safe}_{ratio.replace('.','')}.png"
        plt.savefig(fname, dpi=150)
        plt.close()
        print(f"Saved {fname}")

# ---- Graph 4: Mean latency summary heatmap --------------------------------

for op in ["read", "write"]:
    matrix = []
    for cfg_name, files in CONFIGS.items():
        row = []
        for ratio in RATIOS:
            df = load(files[ratio])
            if df is None:
                row.append(np.nan)
                continue
            vals = df[df["op"] == op]["latency_ms"].dropna()
            row.append(vals.mean() if len(vals) > 0 else np.nan)
        matrix.append(row)

    matrix = np.array(matrix, dtype=float)
    fig, ax = plt.subplots(figsize=(9, 4))
    im = ax.imshow(matrix, aspect="auto", cmap="YlOrRd")
    ax.set_xticks(range(len(RATIOS)))
    ax.set_xticklabels(RATIO_LABELS, rotation=20, ha="right", fontsize=8)
    ax.set_yticks(range(len(CONFIGS)))
    ax.set_yticklabels(list(CONFIGS.keys()), fontsize=9)
    plt.colorbar(im, ax=ax, label="Mean latency (ms)")
    for i in range(len(CONFIGS)):
        for j in range(len(RATIOS)):
            v = matrix[i, j]
            ax.text(j, i, f"{v:.0f}" if not np.isnan(v) else "N/A",
                    ha="center", va="center", fontsize=8, color="black")
    ax.set_title(f"Mean {op} latency (ms) — all configs × ratios")
    plt.tight_layout()
    fname = f"{OUT_DIR}/heatmap_{op}.png"
    plt.savefig(fname, dpi=150)
    plt.close()
    print(f"Saved {fname}")

print("\nAll graphs saved to", OUT_DIR)