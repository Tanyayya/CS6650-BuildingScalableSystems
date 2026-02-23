cat > locustfile.py <<'EOF'
import random
from locust import HttpUser, task, between

BASE_PATH = "/v1"

class StoreUser(HttpUser):
    wait_time = between(0.01, 0.05)

    @task(50)
    def get_hit(self):
        pid = random.choice([1, 2])
        self.client.get(f"{BASE_PATH}/products/{pid}", name="GET hit (1-2)")

    @task(20)
    def get_miss(self):
        pid = random.randint(3, 5000)
        self.client.get(f"{BASE_PATH}/products/{pid}", name="GET miss (404)")

    @task(40)
    def post_details_hit(self):
        pid = random.choice([1, 2])
        self.client.post(
            f"{BASE_PATH}/products/{pid}/details",
            json={
                "product_id": pid,
                "sku": f"SKU-{pid}",
                "manufacturer": "Acme Corporation",
                "category_id": 456,
                "weight": 1300,
                "some_other_id": 789
            },
            name="POST details hit (1-2)"
        )
EOF

