"""The orders service's client for the users API (Python). Illustrative; graph.json beside it is
what graphify would extract from it."""

import httpx


def place_order(name: str, email: str) -> str:
    """A business action that needs a user created first."""
    user_id = create_user(name, email)
    return f"order for {user_id}"


def create_user(name: str, email: str) -> str:
    """POST /users — the generated client call for operationId createUser (snake_case here)."""
    resp = httpx.post("http://users:8080/users", json={"name": name, "email": email})
    return resp.json()["id"]
