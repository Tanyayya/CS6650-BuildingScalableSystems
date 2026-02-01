from locust import HttpUser, task, between
import uuid

class AlbumUser(HttpUser):
    wait_time = between(0, 0)  # no think time for max pressure

    @task
    def post_album(self):
        new_id = str(uuid.uuid4())
        self.client.post(
            "/albums",
            json={
                "id": new_id,
                "title": "Load Test Album",
                "artist": "Locust",
                "price": 9.99
            }
        )
