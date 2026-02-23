"""
Load Testing for Product Search Service
Test 1 - Baseline:      5 users, 2 minutes
Test 2 - Breaking Point: 20 users, 3 minutes

Run Test 1:
  locust -f locustfile.py --host=http://<IP>:8080 \
    --users 5 --spawn-rate 1 --run-time 2m --headless

Run Test 2:
  locust -f locustfile.py --host=http://<IP>:8080 \
    --users 20 --spawn-rate 2 --run-time 3m --headless

Or open the Locust UI:
  locust -f locustfile.py --host=http://<IP>:8080
  Then go to http://localhost:8089
"""

from locust import FastHttpUser, task, between

# Common search terms that will match products
SEARCH_TERMS = [
    "electronics",
    "alpha",
    "beta",
    "books",
    "home",
    "sports",
    "gamma",
    "delta",
]


class ProductSearchUser(FastHttpUser):
    """
    Minimal wait time to maximize pressure on the service.
    FastHttpUser uses gevent for high concurrency simulation.
    """
    wait_time = between(0.1, 0.5)

    @task(10)
    def search(self):
        """Main search — high frequency"""
        import random
        query = random.choice(SEARCH_TERMS)
        with self.client.get(
            f"/products/search?q={query}",
            catch_response=True,
            name="/products/search"
        ) as resp:
            if resp.status_code == 200:
                data = resp.json()
                # Verify exactly 100 items were checked every time
                if data.get("items_checked") != 100:
                    resp.failure(f"Expected 100 items checked, got {data.get('items_checked')}")
                else:
                    resp.success()
            else:
                resp.failure(f"HTTP {resp.status_code}")

    @task(1)
    def health(self):
        """Health check — low frequency"""
        with self.client.get("/health", catch_response=True, name="/health") as resp:
            if resp.status_code == 200:
                resp.success()
            else:
                resp.failure(f"HTTP {resp.status_code}")