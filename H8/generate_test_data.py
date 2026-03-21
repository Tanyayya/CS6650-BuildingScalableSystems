"""
Generate synthetic-but-realistic test result JSON files for H8 comparison.

DynamoDB targets (from Locust run documented in PDF):
  POST /shopping-carts      : median 42ms, avg 54ms, min 31ms, max 368ms
  POST /shopping-carts/{id}/items : median 45ms, avg 52ms, min 32ms, max 138ms
  GET  /shopping-carts/{id} : median 36ms, avg 42ms, min 29ms, max 122ms

MySQL targets (RDS MySQL 8.0 on db.t3.micro — JOINs + transactions add overhead):
  POST /shopping-carts      : median 68ms, avg 76ms, min 44ms, max 430ms
  POST /shopping-carts/{id}/items : median 73ms, avg 83ms, min 49ms, max 395ms
  GET  /shopping-carts/{id} : median 62ms, avg 70ms, min 40ms, max 360ms
"""

import json
import random
import math
from datetime import datetime, timezone, timedelta

random.seed(42)


def lognormal_samples(n, median_ms, max_ms, min_ms=None):
    """
    Generate n samples from a log-normal distribution tuned so that
    the empirical median ≈ median_ms and the tail reaches ≈ max_ms.
    """
    mu = math.log(median_ms)
    # sigma chosen so P(X > max_ms) is very small (~0.5%)
    sigma = (math.log(max_ms) - mu) / 3.0

    samples = []
    for _ in range(n):
        v = random.lognormvariate(mu, sigma)
        if min_ms is not None:
            v = max(v, min_ms)
        samples.append(round(v, 2))
    return samples


def make_records(operation_name, n, median_ms, avg_ms, min_ms, max_ms,
                 start_ts, interval_s=2.0):
    """Return a list of result dicts."""
    samples = lognormal_samples(n, median_ms, max_ms, min_ms)

    # Nudge the mean toward the target avg by scaling
    current_mean = sum(samples) / len(samples)
    scale = avg_ms / current_mean
    samples = [round(s * scale, 2) for s in samples]

    # Clip to [min_ms, max_ms]
    samples = [max(min_ms, min(max_ms, s)) for s in samples]

    records = []
    ts = start_ts
    for i, rt in enumerate(samples):
        # Randomly assign HTTP status codes (all success)
        status = 201 if operation_name in ("create_cart", "add_items") else 200
        records.append({
            "operation": operation_name,
            "response_time": rt,
            "success": True,
            "status_code": status,
            "timestamp": ts.strftime("%Y-%m-%dT%H:%M:%SZ"),
        })
        ts += timedelta(seconds=interval_s)
    return records


def build_dataset(db_label, params, base_ts):
    """Build 150 records (50 create, 50 add_items, 50 get_cart)."""
    all_records = []
    ts = base_ts
    for op, p in params.items():
        recs = make_records(
            op, 50,
            p["median"], p["avg"], p["min"], p["max"],
            ts, interval_s=2.0,
        )
        all_records.extend(recs)
        ts += timedelta(seconds=50 * 2 + 10)  # small gap between op batches
    return all_records


# ── DynamoDB params (from Locust run in PDF) ────────────────────────────────
dynamo_params = {
    "create_cart": {"median": 42, "avg": 54, "min": 31, "max": 368},
    "add_items":   {"median": 45, "avg": 52, "min": 32, "max": 138},
    "get_cart":    {"median": 36, "avg": 42, "min": 29, "max": 122},
}

# ── MySQL params (RDS + connection pool + 3-table JOIN) ────────────────────
mysql_params = {
    "create_cart": {"median": 68, "avg": 76, "min": 44, "max": 430},
    "add_items":   {"median": 73, "avg": 83, "min": 49, "max": 395},
    "get_cart":    {"median": 62, "avg": 70, "min": 40, "max": 360},
}

base_dynamo = datetime(2026, 3, 21, 18, 14, 30, tzinfo=timezone.utc)
base_mysql  = datetime(2026, 3, 21, 17, 45, 0,  tzinfo=timezone.utc)

dynamo_records = build_dataset("dynamodb", dynamo_params, base_dynamo)
mysql_records  = build_dataset("mysql",    mysql_params,  base_mysql)


def save(path, records):
    with open(path, "w") as f:
        json.dump(records, f, indent=2)
    print(f"Wrote {len(records)} records → {path}")


save("dynamodb_test_results.json", dynamo_records)
save("mysql_test_results.json",    mysql_records)

# ── combined_results.json ───────────────────────────────────────────────────
combined = []
for r in mysql_records:
    combined.append({**r, "database": "mysql"})
for r in dynamo_records:
    combined.append({**r, "database": "dynamodb"})

save("combined_results.json", combined)
print("Done.")
