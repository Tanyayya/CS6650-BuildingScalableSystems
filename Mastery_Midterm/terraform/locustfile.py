from locust import FastHttpUser, task, between
import random

SEARCH_TERMS = [
    "electronics", "alpha", "beta", "books",
    "home", "sports", "gamma", "delta",
]

class ProductSearchUser(FastHttpUser):
    wait_time = between(0.1, 0.3)  # minimal wait = maximum pressure

    @task(10)
    def search(self):
        query = random.choice(SEARCH_TERMS)
        with self.client.get(
            f"/products/search?q={query}",
            catch_response=True,
            name="/products/search"
        ) as resp:
            if resp.status_code == 200:
                resp.success()
            else:
                resp.failure(f"HTTP {resp.status_code}")

    @task(1)
    def health(self):
        with self.client.get(
            "/health",
            catch_response=True,
            name="/health"
        ) as resp:
            if resp.status_code == 200:
                resp.success()
            else:
                resp.failure(f"HTTP {resp.status_code}")