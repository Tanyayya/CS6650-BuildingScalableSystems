"""
Locust load test — Phase 1 (sync) and Phase 2 (async) comparison.

Phase 1 — sync (run first, observe the bottleneck):
    python3 -m locust -f locustfile.py --host http://localhost:8080 \
        --headless -u 5  -r 1  --run-time 30s --class-picker

Phase 2 — async flash sale (observe 100% acceptance rate):
    python3 -m locust -f locustfile.py --host http://localhost:8080 \
        --headless -u 20 -r 10 --run-time 60s --class-picker

Web UI (pick user class interactively):
    python3 -m locust -f locustfile.py --host http://localhost:8080
    open http://localhost:8089
"""

import json
import random
import uuid
from locust import HttpUser, task, between, events

PRODUCTS = [
    {"product_id": "SHOE-001", "name": "Trail Runner X",    "price": 129.99},
    {"product_id": "SHIRT-042","name": "Merino Wool Tee",   "price": 49.99},
    {"product_id": "BAG-007",  "name": "Commuter Pack 20L", "price": 89.99},
    {"product_id": "CAP-003",  "name": "Technical Cap",     "price": 34.99},
    {"product_id": "SOCK-011", "name": "Cushion Crew 3pk",  "price": 24.99},
]

def random_order(customer_id: int) -> dict:
    items = [
        {**p, "quantity": random.randint(1, 2)}
        for p in random.sample(PRODUCTS, random.randint(1, 3))
    ]
    return {
        "order_id":    str(uuid.uuid4()),
        "customer_id": customer_id,
        "status":      "pending",
        "items":       items,
        "created_at":  None,
    }


# ── Phase 1: Synchronous ─────────────────────────────────────────────────────

class SyncOrderUser(HttpUser):
    """
    Phase 1 user — hits POST /orders/sync.
    Holds the connection open for the full 3s payment verification.
    Run with -u 5 (normal) or -u 20 (flash sale) to see the bottleneck.
    """
    wait_time = between(0.1, 0.5)
    _customer_counter = 0

    def on_start(self):
        SyncOrderUser._customer_counter += 1
        self.customer_id = SyncOrderUser._customer_counter

    @task
    def place_order_sync(self):
        order = random_order(self.customer_id)
        with self.client.post(
            "/orders/sync",
            json=order,
            name="POST /orders/sync",
            catch_response=True,
            timeout=35,
        ) as resp:
            if resp.status_code in (200, 402):
                resp.success()
            else:
                resp.failure(f"status {resp.status_code}: {resp.text[:100]}")


# ── Phase 2: Async ───────────────────────────────────────────────────────────

class AsyncOrderUser(HttpUser):
    """
    Phase 2 user — hits POST /orders/async.
    Gets 202 Accepted in <100ms regardless of payment processor load.
    Run the flash sale test (20 users) — watch 100% acceptance rate.
    """
    wait_time = between(0.1, 0.5)
    _customer_counter = 0

    def on_start(self):
        AsyncOrderUser._customer_counter += 1
        self.customer_id = AsyncOrderUser._customer_counter

    @task
    def place_order_async(self):
        order = random_order(self.customer_id)
        with self.client.post(
            "/orders/async",
            json=order,
            name="POST /orders/async",
            catch_response=True,
            timeout=10,  # should respond in <100ms, 10s is very generous
        ) as resp:
            if resp.status_code == 202:
                resp.success()
            else:
                resp.failure(f"status {resp.status_code}: {resp.text[:100]}")


# ── Summary on exit ──────────────────────────────────────────────────────────

@events.quitting.add_listener
def on_quitting(environment, **kwargs):
    stats = environment.stats.total
    if stats.num_requests == 0:
        return

    total   = stats.num_requests
    failed  = stats.num_failures
    success = (total - failed) / total * 100

    print("\n" + "=" * 60)
    print("LOAD TEST SUMMARY")
    print("=" * 60)
    print(f"  Total requests  : {total}")
    print(f"  Failures        : {failed}")
    print(f"  Success rate    : {success:.1f}%")
    print(f"  Median latency  : {stats.median_response_time:.0f}ms")
    print(f"  95th percentile : {stats.get_response_time_percentile(0.95):.0f}ms")
    print("=" * 60)

    if success >= 99.9:
        print("\n  ✓  100% acceptance rate — async decoupling works!\n")
    elif success < 95:
        print("\n  ⚠  Degraded — this is the sync bottleneck in action.\n")
