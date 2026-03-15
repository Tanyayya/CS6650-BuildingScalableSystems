"""
Locust load test for Phase 1 — Synchronous Order Processing.

Usage:
    # Normal operations test (5 concurrent users, 30 seconds)
    locust -f locustfile.py --headless \
        --host http://localhost:8080 \
        -u 5 -r 1 --run-time 30s \
        --html reports/normal_ops.html

    # Flash sale test (20 concurrent users, 60 seconds)
    locust -f locustfile.py --headless \
        --host http://localhost:8080 \
        -u 20 -r 10 --run-time 60s \
        --html reports/flash_sale.html

    # Web UI mode (open http://localhost:8089)
    locust -f locustfile.py --host http://localhost:8080

Learning objectives:
    - Normal ops:  5 users × random 100-500ms wait → ~5-10 req/s
                   5 processor slots × (1 / 3s) = 1.67 completions/slot/sec
                   At steady state: ~8.3 orders/sec capacity vs ~5-10 demand → OK

    - Flash sale:  20 users × random 100-500ms wait → ~40-200 req/s spikes
                   Same processor capacity: 8.3 orders/sec max throughput
                   Result: goroutines pile up, latency climbs, timeouts cascade

Watch for:
    - Response time climbing from ~3s to 10s+ under flash load
    - Failure rate spiking (504 Gateway Timeout from ALB, or connection resets)
    - The 50th vs 95th percentile diverging dramatically
"""

import json
import random
import uuid
from locust import HttpUser, task, between, events
from locust.runners import MasterRunner

# ---------------------------------------------------------------------------
# Sample product catalogue for realistic order payloads
# ---------------------------------------------------------------------------
PRODUCTS = [
    {"product_id": "SHOE-001", "name": "Trail Runner X", "price": 129.99},
    {"product_id": "SHIRT-042", "name": "Merino Wool Tee", "price": 49.99},
    {"product_id": "BAG-007",  "name": "Commuter Pack 20L", "price": 89.99},
    {"product_id": "CAP-003",  "name": "Technical Cap",    "price": 34.99},
    {"product_id": "SOCK-011", "name": "Cushion Crew 3pk", "price": 24.99},
]


def random_order(customer_id: int) -> dict:
    """Build a realistic order payload."""
    num_items = random.randint(1, 3)
    items = []
    for product in random.sample(PRODUCTS, num_items):
        items.append({
            "product_id": product["product_id"],
            "name":       product["name"],
            "quantity":   random.randint(1, 2),
            "price":      product["price"],
        })
    return {
        "order_id":    str(uuid.uuid4()),
        "customer_id": customer_id,
        "status":      "pending",
        "items":       items,
        "created_at":  None,  # server sets this
    }


# ---------------------------------------------------------------------------
# User behaviour
# ---------------------------------------------------------------------------

class NormalShopperUser(HttpUser):
    """
    Simulates a shopper during normal operations.

    Wait time: random 100–500ms between requests (as specified in the lab).
    Each task submits one order via POST /orders/sync and waits for the full
    synchronous response before sending the next request.
    """
    wait_time = between(0.1, 0.5)
    customer_id = 0

    def on_start(self):
        # Assign a stable customer ID to this virtual user.
        NormalShopperUser.customer_id += 1
        self.my_customer_id = NormalShopperUser.customer_id

    @task
    def place_order(self):
        order = random_order(self.my_customer_id)
        with self.client.post(
            "/orders/sync",
            json=order,
            headers={"Content-Type": "application/json"},
            name="/orders/sync",
            catch_response=True,
            timeout=30,  # seconds — will surface as failure if exceeded
        ) as response:
            if response.status_code == 200:
                body = response.json()
                response.success()
                # Log latency reported by the server itself for comparison.
                if "latency_ms" in body:
                    print(f"[order] order_id={body['order_id']} server_latency={body['latency_ms']}")
            elif response.status_code == 402:
                # Payment declined — expected for ~2% of orders.
                response.success()
                print(f"[order] payment declined for customer {self.my_customer_id}")
            else:
                response.failure(
                    f"unexpected status {response.status_code}: {response.text[:200]}"
                )


# ---------------------------------------------------------------------------
# Flash sale variant — same code, more users spawned externally via -u flag
# ---------------------------------------------------------------------------

class FlashSaleUser(NormalShopperUser):
    """
    Flash sale shopper — identical behaviour to NormalShopperUser.
    The difference is purely in how many users Locust spawns:
        Normal:     locust -u 5  -r 1
        Flash sale: locust -u 20 -r 10
    
    Increasing spawn rate to 10 users/second makes the transition abrupt,
    mirroring how a real flash sale announcement drives a spike, not a ramp.
    """
    wait_time = between(0.1, 0.5)


# ---------------------------------------------------------------------------
# Event hooks — print a summary when the test finishes
# ---------------------------------------------------------------------------

@events.quitting.add_listener
def on_quitting(environment, **kwargs):
    stats = environment.stats.total
    total  = stats.num_requests
    failed = stats.num_failures
    success_rate = ((total - failed) / total * 100) if total > 0 else 0

    print("\n" + "="*60)
    print("LOAD TEST SUMMARY")
    print("="*60)
    print(f"  Total requests :  {total}")
    print(f"  Failures       :  {failed}")
    print(f"  Success rate   :  {success_rate:.1f}%")
    print(f"  Median latency :  {stats.median_response_time:.0f}ms")
    print(f"  95th pct       :  {stats.get_response_time_percentile(0.95):.0f}ms")
    print(f"  99th pct       :  {stats.get_response_time_percentile(0.99):.0f}ms")
    print(f"  Peak RPS       :  {stats.max_response_time}ms (max response)")
    print("="*60)

    if success_rate < 95:
        print("\n⚠  SUCCESS RATE BELOW 95% — system degraded under load")
        print("   This is the synchronous processing bottleneck in action.")
        print("   Proceed to Phase 2: implement async processing with SNS+SQS\n")
    else:
        print("\n✓  System held up. Try increasing load or reducing wait_time.\n")
