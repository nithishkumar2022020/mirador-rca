package integration

import (
	"net"
	"net/http"
	"testing"
	"time"
)

func TestValkeyConnectivity(t *testing.T) {
	conn, err := net.DialTimeout("tcp", "localhost:6379", 5*time.Second)
	if err != nil {
		t.Fatalf("Failed to connect to Valkey: %v", err)
	}
	defer conn.Close()
}

func TestWeaviateConnectivity(t *testing.T) {
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get("http://localhost:8080/v1/meta")
	if err != nil {
		t.Fatalf("Failed to connect to Weaviate: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("Weaviate returned status %d", resp.StatusCode)
	}
}
