from locust import HttpUser, task, between
import random

class AlbumUser(HttpUser):
    wait_time = between(0.1, 0.3)

    @task(5)
    def get_albums(self):
        self.client.get("/albums")

    @task(1)
    def post_album(self):
        album_id = random.randint(100000, 999999)
        self.client.post(
            "/albums",
            json={
                "id": str(album_id),
                "title": "Safe Album",
                "artist": "Locust",
                "price": 9.99
            }
        )
