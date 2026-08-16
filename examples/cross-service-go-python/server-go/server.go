//go:build ignore

// The users service — a small Go HTTP server. This file is illustrative (excluded from the module
// build by the tag above); graph.json beside it is what graphify would extract from it.
package main

import (
	"encoding/json"
	"net/http"
)

type User struct {
	Name  string `json:"name"`
	Email string `json:"email"`
}

func main() {
	http.HandleFunc("POST /users", CreateUser)
	_ = http.ListenAndServe(":8080", nil)
}

// CreateUser handles POST /users — the operationId createUser in the OpenAPI spec.
func CreateUser(w http.ResponseWriter, r *http.Request) {
	var u User
	_ = json.NewDecoder(r.Body).Decode(&u)
	id := SaveUser(u)
	_ = json.NewEncoder(w).Encode(map[string]string{"id": id})
}

// SaveUser persists a user and returns its id.
func SaveUser(u User) string {
	return "u_123"
}
