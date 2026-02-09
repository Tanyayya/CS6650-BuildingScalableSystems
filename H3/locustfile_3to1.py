from locust import HttpUser, task, between
import random

class AlbumUser(FastHttpUser):
    # Add a tiny think time to avoid melting your CPU
    wait_time = between(0.01, 0.05)

    @task(3)
    def get_albums(self):
        self.client.get("/albums")

    @task(1)
    def post_album(self):
        album_id = random.randint(1, 10_000_000)
        self.client.post(
            "/albums",
            json={
                "id": str(album_id),
                "title": "Load Test Album",
                "artist": "Locust",
                "price": 9.99
            }
        )
